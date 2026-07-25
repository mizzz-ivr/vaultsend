package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimitBlocksAfterLimit(t *testing.T) {
	limiter := NewInMemoryRateLimiter()
	h := RateLimit(limiter, RateLimitConfig{PerMinuteLimit: 2, VerifyLimit: 1})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/shipments/x", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 got=%d", w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/shipments/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429 got=%d", w.Code)
	}
}

func TestRateLimitIgnoresSpoofedForwardedForFromUntrustedPeer(t *testing.T) {
	limiter := NewInMemoryRateLimiter()
	h := RateLimit(limiter, RateLimitConfig{PerMinuteLimit: 1, VerifyLimit: 1})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodGet, "/v1/shipments/x", nil)
	first.RemoteAddr = "203.0.113.10:1234"
	first.Header.Set("X-Forwarded-For", "198.51.100.1")
	firstRecorder := httptest.NewRecorder()
	h.ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("want 200 got=%d", firstRecorder.Code)
	}

	second := httptest.NewRequest(http.MethodGet, "/v1/shipments/x", nil)
	second.RemoteAddr = "203.0.113.10:5678"
	second.Header.Set("X-Forwarded-For", "198.51.100.2")
	secondRecorder := httptest.NewRecorder()
	h.ServeHTTP(secondRecorder, second)
	if secondRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed X-Forwarded-For must not bypass rate limit: got=%d", secondRecorder.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := SecurityHeaders(SecurityHeadersConfig{EnableHSTS: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	expected := map[string]string{
		"Cache-Control":                     "no-store",
		"Content-Security-Policy":           "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'; sandbox",
		"Cross-Origin-Opener-Policy":        "same-origin",
		"Cross-Origin-Resource-Policy":      "same-site",
		"Permissions-Policy":                "camera=(), geolocation=(), microphone=(), payment=(), usb=()",
		"Referrer-Policy":                   "no-referrer",
		"Strict-Transport-Security":         "max-age=31536000; includeSubDomains",
		"X-Content-Type-Options":            "nosniff",
		"X-Frame-Options":                   "DENY",
		"X-Permitted-Cross-Domain-Policies": "none",
	}
	for key, want := range expected {
		if got := w.Header().Get(key); got != want {
			t.Fatalf("%s: want=%q got=%q", key, want, got)
		}
	}
}

func TestSecurityHeadersOmitsHSTSWhenDisabled(t *testing.T) {
	h := SecurityHeaders(SecurityHeadersConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS must be disabled in local/test: %q", got)
	}
}
