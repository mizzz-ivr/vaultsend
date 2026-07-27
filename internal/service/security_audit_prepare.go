package service

import (
	"errors"
	"strings"

	"github.com/example/vaultsend/internal/store"
	"github.com/google/uuid"
)

// Prepare は監査入力を正規化し、HMAC付きの永続化パラメータへ変換する。
// DB書き込みは行わないため、業務トランザクション内のoutbox INSERTに利用できる。
func (s *SecurityAuditService) Prepare(in SecurityAuditInput) (store.CreateSecurityAuditEventParams, error) {
	if len(s.HMACSecret) < 32 {
		return store.CreateSecurityAuditEventParams{}, errors.New("security audit HMAC secret must be at least 32 bytes")
	}
	keyID := strings.TrimSpace(s.HMACKeyID)
	if keyID == "" || len(keyID) > 50 {
		return store.CreateSecurityAuditEventParams{}, errors.New("security audit HMAC key id is invalid")
	}

	normalized, err := normalizeSecurityAuditInput(in)
	if err != nil {
		return store.CreateSecurityAuditEventParams{}, err
	}
	details, err := canonicalAuditDetails(normalized.Details)
	if err != nil {
		return store.CreateSecurityAuditEventParams{}, err
	}

	params := store.CreateSecurityAuditEventParams{
		ID:             uuid.New(),
		OccurredAt:     s.now(),
		EventType:      normalized.EventType,
		Severity:       normalized.Severity,
		Outcome:        normalized.Outcome,
		ActorType:      normalized.ActorType,
		ActorUserID:    normalized.ActorUserID,
		OrganizationID: normalized.OrganizationID,
		ResourceType:   auditOptionalString(normalized.ResourceType),
		ResourceID:     normalized.ResourceID,
		RequestID:      auditOptionalString(normalized.RequestID),
		SourceService:  normalized.SourceService,
		HTTPMethod:     auditOptionalString(normalized.HTTPMethod),
		RoutePattern:   auditOptionalString(normalized.RoutePattern),
		StatusCode:     optionalStatusCode(normalized.StatusCode),
		ClientIPHMAC:   optionalHMAC(s.HMACSecret, "client_ip", normalized.ClientIP),
		UserAgentHMAC:  optionalHMAC(s.HMACSecret, "user_agent", normalized.UserAgent),
		Details:        details,
		IntegrityKeyID: keyID,
	}
	params.IntegrityHMAC, err = s.integrityHMAC(params)
	if err != nil {
		return store.CreateSecurityAuditEventParams{}, err
	}
	return params, nil
}
