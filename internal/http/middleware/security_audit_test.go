package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/vaultsend/internal/service"
	"github.com/example/vaultsend/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type securityAuditRecorderStub struct {
	calls int
	input service.SecurityAuditInput
	err   error
}

func (s *securityAuditRecorderStub) Record(_ context.Context, in service.SecurityAuditInput) (store.SecurityAuditEvent, error) {
	s.calls++
	s.input = in
	return store.SecurityAuditEvent{ID: uuid.New(), EventType: in.EventType, Outcome: in.Outcome}, s.err
}

func TestSecurityAuditRecordsSanitizedRouteAndDeniedOutcome(t *testing.T) {
	recorder := &securityAuditRecorderStub{}
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(SecurityAudit(recorder, nil))
	router.Post("/v1/access/{token}/verify", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/access/sensitive-secret-token/verify", nil)
	req.RemoteAddr = "203.0.113.10:443"
	req.Header.Set("User-Agent", "security-audit-test")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if recorder.calls != 1 {
		t.Fatalf("監査記録回数が不正です: %d", recorder.calls)
	}
	if recorder.input.EventType != "access.verify" || recorder.input.Outcome != "denied" {
		t.Fatalf("監査イベント分類が不正です: %#v", recorder.input)
	}
	if recorder.input.RoutePattern != "/v1/access/{token}/verify" {
		t.Fatalf("route patternが不正です: %s", recorder.input.RoutePattern)
	}
	if strings.Contains(recorder.input.RoutePattern, "sensitive-secret-token") {
		t.Fatal("監査ログへaccess tokenが残っています")
	}
	if recorder.input.ClientIP != "203.0.113.10" {
		t.Fatalf("接続元IPの受け渡しが不正です: %s", recorder.input.ClientIP)
	}
}

func TestSecurityAuditUsesExplicitActorAndResourceAttributes(t *testing.T) {
	recorder := &securityAuditRecorderStub{}
	actorID := uuid.New()
	organizationID := uuid.New()
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(SecurityAudit(recorder, nil))
	router.Post("/v1/orgs", func(w http.ResponseWriter, r *http.Request) {
		SetSecurityAuditActorUserID(r.Context(), actorID)
		SetSecurityAuditOrganizationID(r.Context(), organizationID)
		SetSecurityAuditResource(r.Context(), "organization", organizationID)
		SetSecurityAuditDetail(r.Context(), "source", "test")
		w.WriteHeader(http.StatusCreated)
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/orgs", nil))

	if recorder.calls != 1 {
		t.Fatalf("監査記録回数が不正です: %d", recorder.calls)
	}
	if recorder.input.ActorType != "user" || recorder.input.ActorUserID == nil || *recorder.input.ActorUserID != actorID {
		t.Fatalf("actor属性が反映されていません: %#v", recorder.input)
	}
	if recorder.input.OrganizationID == nil || *recorder.input.OrganizationID != organizationID {
		t.Fatalf("organization属性が反映されていません: %#v", recorder.input)
	}
	if recorder.input.ResourceID == nil || *recorder.input.ResourceID != organizationID || recorder.input.ResourceType != "organization" {
		t.Fatalf("resource属性が反映されていません: %#v", recorder.input)
	}
	if recorder.input.Details["source"] != "test" || recorder.input.Details["schema_version"] != "1" {
		t.Fatalf("detailsが反映されていません: %#v", recorder.input.Details)
	}
}

func TestSecurityAuditSkipsUnclassifiedReadOnlyRoute(t *testing.T) {
	recorder := &securityAuditRecorderStub{}
	router := chi.NewRouter()
	router.Use(SecurityAudit(recorder, nil))
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.calls != 0 {
		t.Fatalf("対象外routeが監査記録されました: %d", recorder.calls)
	}
}
