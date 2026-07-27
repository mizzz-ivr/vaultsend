package handler

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/example/vaultsend/internal/store"
	chimw "github.com/go-chi/chi/v5/middleware"
)

const (
	internalMetricsContentType = "text/plain; version=0.0.4; charset=utf-8"
	defaultMetricsQueryTimeout = 3 * time.Second
)

// SecurityAuditOutboxMetricsStore は内部監視APIが利用する最小Store契約。
type SecurityAuditOutboxMetricsStore interface {
	GetSecurityAuditOutboxMetrics(ctx context.Context) (store.SecurityAuditOutboxMetrics, error)
}

// InternalMetricsHandler はトークン保護されたPrometheus text formatを返す。
type InternalMetricsHandler struct {
	Store        SecurityAuditOutboxMetricsStore
	BearerToken  string
	QueryTimeout time.Duration
}

func (h InternalMetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(h.BearerToken) == "" {
		http.NotFound(w, r)
		return
	}
	if !validInternalMetricsBearer(r.Header.Get("Authorization"), h.BearerToken) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="vaultsend-internal-metrics"`)
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", internalMetricsContentType)
	w.Header().Set("Cache-Control", "no-store")

	timeout := h.QueryTimeout
	if timeout <= 0 {
		timeout = defaultMetricsQueryTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	if h.Store == nil {
		handleInternalMetricsFailure(w, r, errors.New("metrics store is not configured"))
		return
	}
	metrics, err := h.Store.GetSecurityAuditOutboxMetrics(ctx)
	if err != nil {
		handleInternalMetricsFailure(w, r, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `# HELP vaultsend_audit_outbox_scrape_success Whether the audit outbox metrics query succeeded.
# TYPE vaultsend_audit_outbox_scrape_success gauge
vaultsend_audit_outbox_scrape_success 1
# HELP vaultsend_audit_outbox_pending Number of unprocessed security audit outbox events.
# TYPE vaultsend_audit_outbox_pending gauge
vaultsend_audit_outbox_pending %d
# HELP vaultsend_audit_outbox_oldest_pending_age_seconds Age in seconds of the oldest unprocessed security audit outbox event.
# TYPE vaultsend_audit_outbox_oldest_pending_age_seconds gauge
vaultsend_audit_outbox_oldest_pending_age_seconds %g
# HELP vaultsend_audit_outbox_oldest_pending_created_timestamp_seconds Unix timestamp of the oldest unprocessed security audit outbox event. Zero when no event is pending.
# TYPE vaultsend_audit_outbox_oldest_pending_created_timestamp_seconds gauge
vaultsend_audit_outbox_oldest_pending_created_timestamp_seconds %g
`, metrics.PendingCount, metrics.OldestPendingAgeSeconds, metrics.OldestPendingCreatedTimestamp)
}

func handleInternalMetricsFailure(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf(
		"event=internal_metrics_query_failed request_id=%s error=%q",
		chimw.GetReqID(r.Context()),
		err.Error(),
	)
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprint(w, `# HELP vaultsend_audit_outbox_scrape_success Whether the audit outbox metrics query succeeded.
# TYPE vaultsend_audit_outbox_scrape_success gauge
vaultsend_audit_outbox_scrape_success 0
`)
}

func validInternalMetricsBearer(headerValue, expectedToken string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(headerValue, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(headerValue, prefix))
	expected := strings.TrimSpace(expectedToken)
	if provided == "" || strings.ContainsAny(provided, " \t\r\n") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}
