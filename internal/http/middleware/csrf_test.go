package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSRFAllowsSafeMethodWithoutOrigin(t *testing.T) {
	h := CSRFProtection(CSRFConfig{AllowedOrigins: []string{"https://app.example.go.jp"}})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/v1/shipments", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 got=%d", w.Code)
	}
}

func TestCSRFAllowsCookieRequestFromAllowedOrigin(t *testing.T) {
	h := CSRFProtection(CSRFConfig{AllowedOrigins: []string{"https://app.example.go.jp"}})(okHandler())
	req := httptest.NewRequest(http.MethodDelete, "/v1/shipments/id", nil)
	req.Header.Set("Origin", "https://app.example.go.jp")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 got=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCSRFRejectsCookieRequestWithoutOrigin(t *testing.T) {
	h := CSRFProtection(CSRFConfig{AllowedOrigins: []string{"https://app.example.go.jp"}})(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 got=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "csrf_validation_failed") {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
}

func TestCSRFRejectsDisallowedOrigin(t *testing.T) {
	h := CSRFProtection(CSRFConfig{AllowedOrigins: []string{"https://app.example.go.jp"}})(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/v1/shipments", nil)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 got=%d", w.Code)
	}
}

func TestCSRFRejectsCrossSiteFetchWithoutCookie(t *testing.T) {
	h := CSRFProtection(CSRFConfig{AllowedOrigins: []string{"https://app.example.go.jp"}})(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 got=%d", w.Code)
	}
}

func TestCSRFAllowsServerToServerRequestWithoutCookieOrOrigin(t *testing.T) {
	h := CSRFProtection(CSRFConfig{AllowedOrigins: []string{"https://app.example.go.jp"}})(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/v1/billing/webhook", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 got=%d body=%s", w.Code, w.Body.String())
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}
