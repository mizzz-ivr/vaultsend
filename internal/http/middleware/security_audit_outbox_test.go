package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/vaultsend/internal/service"
	"github.com/example/vaultsend/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type outboxAuditServiceStoreStub struct {
	createCalls int
}

func (s *outboxAuditServiceStoreStub) CreateSecurityAuditEvent(_ context.Context, arg store.CreateSecurityAuditEventParams) (store.SecurityAuditEvent, error) {
	s.createCalls++
	return store.SecurityAuditEvent{ID: arg.ID, EventType: arg.EventType, Outcome: arg.Outcome}, nil
}

func (s *outboxAuditServiceStoreStub) ListSecurityAuditEventsByOrganization(context.Context, uuid.UUID, int32, int32) ([]store.SecurityAuditEvent, error) {
	return nil, nil
}

func (s *outboxAuditServiceStoreStub) CountSecurityAuditEventsByOrganization(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}

func (s *outboxAuditServiceStoreStub) GetOrganizationMember(context.Context, uuid.UUID, uuid.UUID) (store.OrganizationMember, error) {
	return store.OrganizationMember{}, store.ErrNotFound
}

func TestSecurityAuditWithOutboxSuppressesDirectSuccessRecord(t *testing.T) {
	storeStub := &outboxAuditServiceStoreStub{}
	auditService := &service.SecurityAuditService{
		Store:      storeStub,
		HMACSecret: []byte("01234567890123456789012345678901"),
		HMACKeyID:  "test-key",
	}
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(SecurityAuditWithOutbox(auditService, nil))
	router.Post("/v1/shipments", func(w http.ResponseWriter, r *http.Request) {
		resourceID := uuid.New()
		prepared, enabled, err := store.PrepareSecurityAuditOutboxEvent(r.Context(), store.SecurityAuditOutboxEvent{
			EventType:    "shipment.create",
			Severity:     "info",
			Outcome:      "success",
			ResourceType: "shipment",
			ResourceID:   &resourceID,
			StatusCode:   http.StatusCreated,
		})
		if err != nil || !enabled || prepared.EventType != "shipment.create" {
			t.Fatalf("outboxイベント準備に失敗しました: enabled=%v err=%v event=%#v", enabled, err, prepared)
		}
		store.MarkSecurityAuditOutboxEnqueued(r.Context())
		w.WriteHeader(http.StatusCreated)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/shipments", nil))
	if response.Code != http.StatusCreated {
		t.Fatalf("status codeが不正です: %d", response.Code)
	}
	if storeStub.createCalls != 0 {
		t.Fatalf("outbox成功イベントが直接記録されました: %d", storeStub.createCalls)
	}
}

func TestSecurityAuditWithOutboxKeepsDirectFailureRecord(t *testing.T) {
	storeStub := &outboxAuditServiceStoreStub{}
	auditService := &service.SecurityAuditService{
		Store:      storeStub,
		HMACSecret: []byte("01234567890123456789012345678901"),
		HMACKeyID:  "test-key",
	}
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(SecurityAuditWithOutbox(auditService, nil))
	router.Post("/v1/shipments", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/shipments", nil))
	if storeStub.createCalls != 1 {
		t.Fatalf("失敗イベントが直接記録されていません: %d", storeStub.createCalls)
	}
}
