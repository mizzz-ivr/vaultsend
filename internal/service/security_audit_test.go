package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/example/vaultsend/internal/store"
	"github.com/google/uuid"
)

type securityAuditStoreStub struct {
	created    []store.SecurityAuditEvent
	lastParams store.CreateSecurityAuditEventParams
	memberRole string
	memberErr  error
}

func (s *securityAuditStoreStub) CreateSecurityAuditEvent(_ context.Context, arg store.CreateSecurityAuditEventParams) (store.SecurityAuditEvent, error) {
	s.lastParams = arg
	event := store.SecurityAuditEvent{
		ID:             arg.ID,
		OccurredAt:     arg.OccurredAt,
		RecordedAt:     arg.OccurredAt.Add(time.Millisecond),
		EventType:      arg.EventType,
		Severity:       arg.Severity,
		Outcome:        arg.Outcome,
		ActorType:      arg.ActorType,
		ActorUserID:    arg.ActorUserID,
		OrganizationID: arg.OrganizationID,
		ResourceType:   arg.ResourceType,
		ResourceID:     arg.ResourceID,
		RequestID:      arg.RequestID,
		SourceService:  arg.SourceService,
		HTTPMethod:     arg.HTTPMethod,
		RoutePattern:   arg.RoutePattern,
		StatusCode:     arg.StatusCode,
		ClientIPHMAC:   arg.ClientIPHMAC,
		UserAgentHMAC:  arg.UserAgentHMAC,
		Details:        arg.Details,
		IntegrityKeyID: arg.IntegrityKeyID,
		IntegrityHMAC:  arg.IntegrityHMAC,
	}
	s.created = append(s.created, event)
	return event, nil
}

func (s *securityAuditStoreStub) ListSecurityAuditEventsByOrganization(_ context.Context, organizationID uuid.UUID, limit, offset int32) ([]store.SecurityAuditEvent, error) {
	items := make([]store.SecurityAuditEvent, 0)
	for _, event := range s.created {
		if event.OrganizationID != nil && *event.OrganizationID == organizationID {
			items = append(items, event)
		}
	}
	start := int(offset)
	if start >= len(items) {
		return []store.SecurityAuditEvent{}, nil
	}
	end := start + int(limit)
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], nil
}

func (s *securityAuditStoreStub) CountSecurityAuditEventsByOrganization(_ context.Context, organizationID uuid.UUID) (int64, error) {
	var total int64
	for _, event := range s.created {
		if event.OrganizationID != nil && *event.OrganizationID == organizationID {
			total++
		}
	}
	return total, nil
}

func (s *securityAuditStoreStub) GetOrganizationMember(_ context.Context, orgID uuid.UUID, userID uuid.UUID) (store.OrganizationMember, error) {
	if s.memberErr != nil {
		return store.OrganizationMember{}, s.memberErr
	}
	return store.OrganizationMember{OrganizationID: orgID, UserID: userID, Role: s.memberRole}, nil
}

func TestSecurityAuditRecordPseudonymizesAndSignsEvent(t *testing.T) {
	stub := &securityAuditStoreStub{memberRole: "admin"}
	fixed := time.Date(2026, 7, 25, 12, 0, 0, 123456000, time.UTC)
	service := &SecurityAuditService{
		Store:      stub,
		HMACSecret: []byte("0123456789abcdef0123456789abcdef"),
		HMACKeyID:  "test-v1",
		Now:        func() time.Time { return fixed },
	}
	actorID := uuid.New()
	orgID := uuid.New()
	resourceID := uuid.New()
	event, err := service.Record(context.Background(), SecurityAuditInput{
		EventType:      "organization.member.add",
		Severity:       "warning",
		Outcome:        "success",
		ActorType:      "user",
		ActorUserID:    &actorID,
		OrganizationID: &orgID,
		ResourceType:   "user",
		ResourceID:     &resourceID,
		RequestID:      "request-1",
		SourceService:  "api",
		HTTPMethod:     "POST",
		RoutePattern:   "/v1/orgs/{id}/members",
		StatusCode:     201,
		ClientIP:       "203.0.113.10",
		UserAgent:      "audit-test-agent",
		Details:        map[string]string{"role": "member"},
	})
	if err != nil {
		t.Fatalf("監査イベント記録に失敗しました: %v", err)
	}
	if stub.lastParams.ClientIPHMAC == nil || *stub.lastParams.ClientIPHMAC == "203.0.113.10" || len(*stub.lastParams.ClientIPHMAC) != 64 {
		t.Fatalf("接続元IPが正しく仮名化されていません: %#v", stub.lastParams.ClientIPHMAC)
	}
	if stub.lastParams.UserAgentHMAC == nil || *stub.lastParams.UserAgentHMAC == "audit-test-agent" || len(*stub.lastParams.UserAgentHMAC) != 64 {
		t.Fatalf("User-Agentが正しく仮名化されていません: %#v", stub.lastParams.UserAgentHMAC)
	}
	if !service.VerifyIntegrity(event) {
		t.Fatal("未変更イベントのintegrity HMAC検証に失敗しました")
	}
	tampered := event
	tampered.Outcome = "failure"
	if service.VerifyIntegrity(tampered) {
		t.Fatal("改ざんしたイベントのintegrity HMAC検証が成功しました")
	}
	var details map[string]string
	if err := json.Unmarshal(stub.lastParams.Details, &details); err != nil {
		t.Fatalf("detailsのdecodeに失敗しました: %v", err)
	}
	if details["role"] != "member" {
		t.Fatalf("detailsが保持されていません: %#v", details)
	}
}

func TestSecurityAuditListOrganizationEventsRequiresAdmin(t *testing.T) {
	stub := &securityAuditStoreStub{memberRole: "member"}
	service := &SecurityAuditService{
		Store:      stub,
		HMACSecret: []byte("0123456789abcdef0123456789abcdef"),
		HMACKeyID:  "test-v1",
	}
	_, err := service.ListOrganizationEvents(context.Background(), uuid.New(), uuid.New(), 50, 0)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 {
		t.Fatalf("memberの監査ログ閲覧を403で拒否していません: %v", err)
	}
}

func TestSecurityAuditListOrganizationEventsReturnsIntegrityStatus(t *testing.T) {
	stub := &securityAuditStoreStub{memberRole: "owner"}
	fixed := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	service := &SecurityAuditService{
		Store:      stub,
		HMACSecret: []byte("0123456789abcdef0123456789abcdef"),
		HMACKeyID:  "test-v1",
		Now:        func() time.Time { return fixed },
	}
	actorID := uuid.New()
	orgID := uuid.New()
	if _, err := service.Record(context.Background(), SecurityAuditInput{
		EventType:      "organization.create",
		Severity:       "info",
		Outcome:        "success",
		ActorType:      "user",
		ActorUserID:    &actorID,
		OrganizationID: &orgID,
		ResourceType:   "organization",
		ResourceID:     &orgID,
		SourceService:  "api",
	}); err != nil {
		t.Fatalf("監査イベント記録に失敗しました: %v", err)
	}
	out, err := service.ListOrganizationEvents(context.Background(), actorID, orgID, 50, 0)
	if err != nil {
		t.Fatalf("監査ログ一覧取得に失敗しました: %v", err)
	}
	if out.Total != 1 || len(out.Items) != 1 {
		t.Fatalf("監査ログ件数が不正です: total=%d items=%d", out.Total, len(out.Items))
	}
	if !out.Items[0].IntegrityValid {
		t.Fatal("正常な監査イベントがintegrity_valid=falseです")
	}
}
