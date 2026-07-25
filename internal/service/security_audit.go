package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/example/vaultsend/internal/store"
	"github.com/google/uuid"
)

const (
	defaultSecurityAuditLimit = int32(50)
	maxSecurityAuditLimit     = int32(100)
	maxSecurityAuditDetails   = 8 * 1024
)

var securityAuditNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,99}$`)

type SecurityAuditStore interface {
	CreateSecurityAuditEvent(ctx context.Context, arg store.CreateSecurityAuditEventParams) (store.SecurityAuditEvent, error)
	ListSecurityAuditEventsByOrganization(ctx context.Context, organizationID uuid.UUID, limit, offset int32) ([]store.SecurityAuditEvent, error)
	CountSecurityAuditEventsByOrganization(ctx context.Context, organizationID uuid.UUID) (int64, error)
	GetOrganizationMember(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) (store.OrganizationMember, error)
}

type SecurityAuditService struct {
	Store      SecurityAuditStore
	HMACSecret []byte
	HMACKeyID  string
	Now        func() time.Time
}

type SecurityAuditInput struct {
	EventType      string
	Severity       string
	Outcome        string
	ActorType      string
	ActorUserID    *uuid.UUID
	OrganizationID *uuid.UUID
	ResourceType   string
	ResourceID     *uuid.UUID
	RequestID      string
	SourceService  string
	HTTPMethod     string
	RoutePattern   string
	StatusCode     int
	ClientIP       string
	UserAgent      string
	Details        map[string]string
}

type SecurityAuditEventOutput struct {
	ID             uuid.UUID      `json:"id"`
	OccurredAt     time.Time      `json:"occurred_at"`
	RecordedAt     time.Time      `json:"recorded_at"`
	EventType      string         `json:"event_type"`
	Severity       string         `json:"severity"`
	Outcome        string         `json:"outcome"`
	ActorType      string         `json:"actor_type"`
	ActorUserID    *uuid.UUID     `json:"actor_user_id,omitempty"`
	ResourceType   *string        `json:"resource_type,omitempty"`
	ResourceID     *uuid.UUID     `json:"resource_id,omitempty"`
	RequestID      *string        `json:"request_id,omitempty"`
	SourceService  string         `json:"source_service"`
	HTTPMethod     *string        `json:"http_method,omitempty"`
	RoutePattern   *string        `json:"route_pattern,omitempty"`
	StatusCode     *int32         `json:"status_code,omitempty"`
	Details        map[string]any `json:"details"`
	IntegrityKeyID string         `json:"integrity_key_id"`
	IntegrityValid bool           `json:"integrity_valid"`
}

type SecurityAuditListOutput struct {
	Items  []SecurityAuditEventOutput `json:"items"`
	Total  int64                      `json:"total"`
	Limit  int32                      `json:"limit"`
	Offset int32                      `json:"offset"`
}

func (s *SecurityAuditService) Record(ctx context.Context, in SecurityAuditInput) (store.SecurityAuditEvent, error) {
	if s.Store == nil {
		return store.SecurityAuditEvent{}, errors.New("security audit store is required")
	}
	if len(s.HMACSecret) < 32 {
		return store.SecurityAuditEvent{}, errors.New("security audit HMAC secret must be at least 32 bytes")
	}
	keyID := strings.TrimSpace(s.HMACKeyID)
	if keyID == "" || len(keyID) > 50 {
		return store.SecurityAuditEvent{}, errors.New("security audit HMAC key id is invalid")
	}

	normalized, err := normalizeSecurityAuditInput(in)
	if err != nil {
		return store.SecurityAuditEvent{}, err
	}
	details, err := canonicalAuditDetails(normalized.Details)
	if err != nil {
		return store.SecurityAuditEvent{}, err
	}

	eventID := uuid.New()
	occurredAt := s.now()
	clientIPHMAC := optionalHMAC(s.HMACSecret, "client_ip", normalized.ClientIP)
	userAgentHMAC := optionalHMAC(s.HMACSecret, "user_agent", normalized.UserAgent)
	params := store.CreateSecurityAuditEventParams{
		ID:             eventID,
		OccurredAt:     occurredAt,
		EventType:      normalized.EventType,
		Severity:       normalized.Severity,
		Outcome:        normalized.Outcome,
		ActorType:      normalized.ActorType,
		ActorUserID:    normalized.ActorUserID,
		OrganizationID: normalized.OrganizationID,
		ResourceType:   optionalString(normalized.ResourceType),
		ResourceID:     normalized.ResourceID,
		RequestID:      optionalString(normalized.RequestID),
		SourceService:  normalized.SourceService,
		HTTPMethod:     optionalString(normalized.HTTPMethod),
		RoutePattern:   optionalString(normalized.RoutePattern),
		StatusCode:     optionalStatusCode(normalized.StatusCode),
		ClientIPHMAC:   clientIPHMAC,
		UserAgentHMAC:  userAgentHMAC,
		Details:        details,
		IntegrityKeyID: keyID,
	}
	params.IntegrityHMAC, err = s.integrityHMAC(params)
	if err != nil {
		return store.SecurityAuditEvent{}, err
	}

	event, err := s.Store.CreateSecurityAuditEvent(ctx, params)
	if err != nil {
		return store.SecurityAuditEvent{}, fmt.Errorf("create security audit event: %w", err)
	}
	log.Printf(
		"event=security_audit_persisted audit_event_id=%s audit_event_type=%s outcome=%s severity=%s request_id=%s",
		event.ID,
		event.EventType,
		event.Outcome,
		event.Severity,
		stringValue(event.RequestID),
	)
	return event, nil
}

func (s *SecurityAuditService) ListOrganizationEvents(ctx context.Context, actorUserID, organizationID uuid.UUID, limit, offset int32) (SecurityAuditListOutput, error) {
	if actorUserID == uuid.Nil {
		return SecurityAuditListOutput{}, &APIError{Status: 401, Code: "unauthorized", Message: "ログインが必要です"}
	}
	if organizationID == uuid.Nil {
		return SecurityAuditListOutput{}, &APIError{Status: 400, Code: "invalid_org_id", Message: "organization id が不正です"}
	}
	member, err := s.Store.GetOrganizationMember(ctx, organizationID, actorUserID)
	if errors.Is(err, store.ErrNotFound) {
		return SecurityAuditListOutput{}, &APIError{Status: 403, Code: "forbidden", Message: "organization へのアクセス権がありません"}
	}
	if err != nil {
		return SecurityAuditListOutput{}, fmt.Errorf("get organization member for audit events: %w", err)
	}
	if member.Role != "owner" && member.Role != "admin" {
		return SecurityAuditListOutput{}, &APIError{Status: 403, Code: "forbidden", Message: "監査ログの閲覧には admin 以上の権限が必要です"}
	}

	if limit <= 0 {
		limit = defaultSecurityAuditLimit
	}
	if limit > maxSecurityAuditLimit {
		return SecurityAuditListOutput{}, &APIError{Status: 400, Code: "invalid_limit", Message: "limit は100以下で指定してください"}
	}
	if offset < 0 {
		return SecurityAuditListOutput{}, &APIError{Status: 400, Code: "invalid_offset", Message: "offset が不正です"}
	}

	rows, err := s.Store.ListSecurityAuditEventsByOrganization(ctx, organizationID, limit, offset)
	if err != nil {
		return SecurityAuditListOutput{}, fmt.Errorf("list security audit events: %w", err)
	}
	total, err := s.Store.CountSecurityAuditEventsByOrganization(ctx, organizationID)
	if err != nil {
		return SecurityAuditListOutput{}, fmt.Errorf("count security audit events: %w", err)
	}

	items := make([]SecurityAuditEventOutput, 0, len(rows))
	for _, row := range rows {
		details := map[string]any{}
		if len(row.Details) > 0 {
			_ = json.Unmarshal(row.Details, &details)
		}
		items = append(items, SecurityAuditEventOutput{
			ID:             row.ID,
			OccurredAt:     row.OccurredAt,
			RecordedAt:     row.RecordedAt,
			EventType:      row.EventType,
			Severity:       row.Severity,
			Outcome:        row.Outcome,
			ActorType:      row.ActorType,
			ActorUserID:    row.ActorUserID,
			ResourceType:   row.ResourceType,
			ResourceID:     row.ResourceID,
			RequestID:      row.RequestID,
			SourceService:  row.SourceService,
			HTTPMethod:     row.HTTPMethod,
			RoutePattern:   row.RoutePattern,
			StatusCode:     row.StatusCode,
			Details:        details,
			IntegrityKeyID: row.IntegrityKeyID,
			IntegrityValid: s.VerifyIntegrity(row),
		})
	}
	return SecurityAuditListOutput{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (s *SecurityAuditService) VerifyIntegrity(event store.SecurityAuditEvent) bool {
	if len(s.HMACSecret) < 32 || event.IntegrityKeyID != strings.TrimSpace(s.HMACKeyID) {
		return false
	}
	details, err := canonicalRawJSON(event.Details)
	if err != nil {
		return false
	}
	params := store.CreateSecurityAuditEventParams{
		ID:             event.ID,
		OccurredAt:     event.OccurredAt,
		EventType:      event.EventType,
		Severity:       event.Severity,
		Outcome:        event.Outcome,
		ActorType:      event.ActorType,
		ActorUserID:    event.ActorUserID,
		OrganizationID: event.OrganizationID,
		ResourceType:   event.ResourceType,
		ResourceID:     event.ResourceID,
		RequestID:      event.RequestID,
		SourceService:  event.SourceService,
		HTTPMethod:     event.HTTPMethod,
		RoutePattern:   event.RoutePattern,
		StatusCode:     event.StatusCode,
		ClientIPHMAC:   event.ClientIPHMAC,
		UserAgentHMAC:  event.UserAgentHMAC,
		Details:        details,
		IntegrityKeyID: event.IntegrityKeyID,
	}
	expected, err := s.integrityHMAC(params)
	if err != nil {
		return false
	}
	actual, err := hex.DecodeString(event.IntegrityHMAC)
	if err != nil {
		return false
	}
	expectedBytes, err := hex.DecodeString(expected)
	if err != nil {
		return false
	}
	return hmac.Equal(actual, expectedBytes)
}

func (s *SecurityAuditService) integrityHMAC(params store.CreateSecurityAuditEventParams) (string, error) {
	details, err := canonicalRawJSON(params.Details)
	if err != nil {
		return "", err
	}
	canonical := struct {
		ID               string          `json:"id"`
		OccurredAt       string          `json:"occurred_at"`
		EventType        string          `json:"event_type"`
		Severity         string          `json:"severity"`
		Outcome          string          `json:"outcome"`
		ActorType        string          `json:"actor_type"`
		ActorUserID      string          `json:"actor_user_id"`
		OrganizationID   string          `json:"organization_id"`
		ResourceType     string          `json:"resource_type"`
		ResourceID       string          `json:"resource_id"`
		RequestID        string          `json:"request_id"`
		SourceService    string          `json:"source_service"`
		HTTPMethod       string          `json:"http_method"`
		RoutePattern     string          `json:"route_pattern"`
		StatusCode       int32           `json:"status_code"`
		ClientIPHMAC     string          `json:"client_ip_hmac"`
		UserAgentHMAC    string          `json:"user_agent_hmac"`
		Details          json.RawMessage `json:"details"`
		IntegrityKeyID   string          `json:"integrity_key_id"`
	}{
		ID:             params.ID.String(),
		OccurredAt:     params.OccurredAt.UTC().Format(time.RFC3339Nano),
		EventType:      params.EventType,
		Severity:       params.Severity,
		Outcome:        params.Outcome,
		ActorType:      params.ActorType,
		ActorUserID:    uuidValue(params.ActorUserID),
		OrganizationID: uuidValue(params.OrganizationID),
		ResourceType:   stringValue(params.ResourceType),
		ResourceID:     uuidValue(params.ResourceID),
		RequestID:      stringValue(params.RequestID),
		SourceService:  params.SourceService,
		HTTPMethod:     stringValue(params.HTTPMethod),
		RoutePattern:   stringValue(params.RoutePattern),
		StatusCode:     int32Value(params.StatusCode),
		ClientIPHMAC:   stringValue(params.ClientIPHMAC),
		UserAgentHMAC:  stringValue(params.UserAgentHMAC),
		Details:        details,
		IntegrityKeyID: params.IntegrityKeyID,
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal security audit integrity payload: %w", err)
	}
	return hmacSHA256Hex(s.HMACSecret, payload), nil
}

func normalizeSecurityAuditInput(in SecurityAuditInput) (SecurityAuditInput, error) {
	in.EventType = strings.TrimSpace(strings.ToLower(in.EventType))
	in.Severity = strings.TrimSpace(strings.ToLower(in.Severity))
	in.Outcome = strings.TrimSpace(strings.ToLower(in.Outcome))
	in.ActorType = strings.TrimSpace(strings.ToLower(in.ActorType))
	in.ResourceType = strings.TrimSpace(strings.ToLower(in.ResourceType))
	in.RequestID = trimToLength(in.RequestID, 100)
	in.SourceService = strings.TrimSpace(strings.ToLower(in.SourceService))
	in.HTTPMethod = trimToLength(strings.ToUpper(in.HTTPMethod), 10)
	in.RoutePattern = trimToLength(in.RoutePattern, 200)
	in.ClientIP = strings.TrimSpace(in.ClientIP)
	in.UserAgent = strings.TrimSpace(in.UserAgent)

	if !securityAuditNamePattern.MatchString(in.EventType) {
		return SecurityAuditInput{}, errors.New("security audit event type is invalid")
	}
	if in.Severity != "info" && in.Severity != "warning" && in.Severity != "critical" {
		return SecurityAuditInput{}, errors.New("security audit severity is invalid")
	}
	if in.Outcome != "success" && in.Outcome != "denied" && in.Outcome != "failure" {
		return SecurityAuditInput{}, errors.New("security audit outcome is invalid")
	}
	if in.ActorType != "user" && in.ActorType != "anonymous" && in.ActorType != "recipient" && in.ActorType != "system" && in.ActorType != "webhook" {
		return SecurityAuditInput{}, errors.New("security audit actor type is invalid")
	}
	if in.ActorType == "user" && (in.ActorUserID == nil || *in.ActorUserID == uuid.Nil) {
		return SecurityAuditInput{}, errors.New("security audit user actor requires actor user id")
	}
	if in.ResourceType != "" && !securityAuditNamePattern.MatchString(in.ResourceType) {
		return SecurityAuditInput{}, errors.New("security audit resource type is invalid")
	}
	if in.SourceService != "api" && in.SourceService != "mail-worker" && in.SourceService != "cleanup-worker" {
		return SecurityAuditInput{}, errors.New("security audit source service is invalid")
	}
	if in.StatusCode != 0 && (in.StatusCode < 100 || in.StatusCode > 599) {
		return SecurityAuditInput{}, errors.New("security audit status code is invalid")
	}
	return in, nil
}

func canonicalAuditDetails(details map[string]string) (json.RawMessage, error) {
	if details == nil {
		details = map[string]string{}
	}
	for key, value := range details {
		cleanKey := strings.TrimSpace(key)
		if cleanKey == "" || len(cleanKey) > 100 || cleanKey != key {
			return nil, errors.New("security audit detail key is invalid")
		}
		if len(value) > 500 {
			return nil, errors.New("security audit detail value is too long")
		}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return nil, fmt.Errorf("marshal security audit details: %w", err)
	}
	if len(payload) > maxSecurityAuditDetails {
		return nil, errors.New("security audit details are too large")
	}
	return canonicalRawJSON(payload)
}

func canonicalRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode security audit details: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("security audit details must be a JSON object")
	}
	payload, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("canonicalize security audit details: %w", err)
	}
	return payload, nil
}

func optionalHMAC(secret []byte, domain, value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	hash := hmacSHA256Hex(secret, []byte(domain+"\x00"+value))
	return &hash
}

func hmacSHA256Hex(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func optionalStatusCode(value int) *int32 {
	if value == 0 {
		return nil
	}
	converted := int32(value)
	return &converted
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func uuidValue(value *uuid.UUID) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func int32Value(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

func trimToLength(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func (s *SecurityAuditService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
