package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/vaultsend/internal/store"
)

type internalMetricsStoreStub struct {
	metrics        store.SecurityAuditOutboxMetrics
	err            error
	called         int
	waitForContext bool
}

func (s *internalMetricsStoreStub) GetSecurityAuditOutboxMetrics(ctx context.Context) (store.SecurityAuditOutboxMetrics, error) {
	s.called++
	if s.waitForContext {
		<-ctx.Done()
		return store.SecurityAuditOutboxMetrics{}, ctx.Err()
	}
	return s.metrics, s.err
}

func TestInternalMetricsHandlerReturnsNotFoundWhenDisabled(t *testing.T) {
	storeStub := &internalMetricsStoreStub{}
	handler := InternalMetricsHandler{Store: storeStub}
	req := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: got=%d want=%d", res.Code, http.StatusNotFound)
	}
	if storeStub.called != 0 {
		t.Fatalf("disabled endpoint must not query the store: called=%d", storeStub.called)
	}
}

func TestInternalMetricsHandlerRejectsInvalidBearerWithoutQueryingStore(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{name: "missing"},
		{name: "wrong scheme", header: "Basic token"},
		{name: "wrong token", header: "Bearer wrong-token"},
		{name: "token contains whitespace", header: "Bearer valid-token extra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storeStub := &internalMetricsStoreStub{}
			handler := InternalMetricsHandler{Store: storeStub, BearerToken: "valid-token"}
			req := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			res := httptest.NewRecorder()

			handler.ServeHTTP(res, req)

			if res.Code != http.StatusUnauthorized {
				t.Fatalf("unexpected status: got=%d want=%d", res.Code, http.StatusUnauthorized)
			}
			if got := res.Header().Get("WWW-Authenticate"); got == "" {
				t.Fatal("WWW-Authenticate header is required")
			}
			if storeStub.called != 0 {
				t.Fatalf("unauthorized request must not query the store: called=%d", storeStub.called)
			}
		})
	}
}

func TestInternalMetricsHandlerReturnsPrometheusMetrics(t *testing.T) {
	storeStub := &internalMetricsStoreStub{metrics: store.SecurityAuditOutboxMetrics{
		PendingCount:                  12,
		OldestPendingAgeSeconds:       91.5,
		OldestPendingCreatedTimestamp: 1_722_000_000,
	}}
	handler := InternalMetricsHandler{
		Store:        storeStub,
		BearerToken:  "valid-token",
		QueryTimeout: time.Second,
	}
	req := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status: got=%d want=%d body=%s", res.Code, http.StatusOK, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != internalMetricsContentType {
		t.Fatalf("unexpected Content-Type: %q", got)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("unexpected Cache-Control: %q", got)
	}
	body := res.Body.String()
	for _, expected := range []string{
		"vaultsend_audit_outbox_scrape_success 1",
		"vaultsend_audit_outbox_pending 12",
		"vaultsend_audit_outbox_oldest_pending_age_seconds 91.5",
		"vaultsend_audit_outbox_oldest_pending_created_timestamp_seconds 1.722e+09",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metric %q was not found in body:\n%s", expected, body)
		}
	}
	if storeStub.called != 1 {
		t.Fatalf("unexpected store call count: %d", storeStub.called)
	}
}

func TestInternalMetricsHandlerReturnsServiceUnavailableOnStoreFailure(t *testing.T) {
	storeStub := &internalMetricsStoreStub{err: errors.New("database unavailable")}
	handler := InternalMetricsHandler{Store: storeStub, BearerToken: "valid-token"}
	req := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status: got=%d want=%d", res.Code, http.StatusServiceUnavailable)
	}
	body := res.Body.String()
	if !strings.Contains(body, "vaultsend_audit_outbox_scrape_success 0") {
		t.Fatalf("scrape failure metric was not returned: %s", body)
	}
	if strings.Contains(body, "vaultsend_audit_outbox_pending") {
		t.Fatalf("pending count must not be reported as a valid value on query failure: %s", body)
	}
}

func TestInternalMetricsHandlerAppliesQueryTimeout(t *testing.T) {
	storeStub := &internalMetricsStoreStub{waitForContext: true}
	handler := InternalMetricsHandler{
		Store:        storeStub,
		BearerToken:  "valid-token",
		QueryTimeout: 10 * time.Millisecond,
	}
	req := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status: got=%d want=%d", res.Code, http.StatusServiceUnavailable)
	}
	if storeStub.called != 1 {
		t.Fatalf("unexpected store call count: %d", storeStub.called)
	}
}
