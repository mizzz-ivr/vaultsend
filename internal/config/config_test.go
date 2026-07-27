package config

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestLoadRejectsInsecureCookieOutsideLocal(t *testing.T) {
	setBaseEnv(t, "production", "https://app.example.go.jp")
	t.Setenv("COOKIE_SECURE", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE must be true") {
		t.Fatalf("expected secure cookie error, got=%v", err)
	}
}

func TestLoadRejectsHTTPFrontendOutsideLocal(t *testing.T) {
	setBaseEnv(t, "production", "http://app.example.go.jp")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "FRONTEND_URL must use https") {
		t.Fatalf("expected https error, got=%v", err)
	}
}

func TestLoadRejectsDisabledHSTSOutsideLocal(t *testing.T) {
	setBaseEnv(t, "production", "https://app.example.go.jp")
	t.Setenv("HSTS_ENABLED", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "HSTS_ENABLED must be true") {
		t.Fatalf("expected HSTS error, got=%v", err)
	}
}

func TestLoadRejectsSameSiteNoneWithoutSecure(t *testing.T) {
	setBaseEnv(t, "local", "http://localhost:3000")
	t.Setenv("COOKIE_SAMESITE", "none")
	t.Setenv("COOKIE_SECURE", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "requires COOKIE_SECURE=true") {
		t.Fatalf("expected SameSite validation error, got=%v", err)
	}
}

func TestLoadParsesTrustedProxiesOriginsAndServerLimits(t *testing.T) {
	setBaseEnv(t, "local", "http://localhost:3000/path")
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 2001:db8::/32")
	t.Setenv("CSRF_ALLOWED_ORIGINS", "https://admin.example.go.jp,https://app.example.go.jp")
	t.Setenv("HTTP_READ_HEADER_TIMEOUT_SEC", "7")
	t.Setenv("HTTP_READ_TIMEOUT_SEC", "17")
	t.Setenv("HTTP_WRITE_TIMEOUT_SEC", "37")
	t.Setenv("HTTP_IDLE_TIMEOUT_SEC", "67")
	t.Setenv("HTTP_MAX_HEADER_BYTES", "65536")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 {
		t.Fatalf("unexpected trusted proxies: %v", cfg.TrustedProxyCIDRs)
	}
	if cfg.TrustedProxyCIDRs[0] != netip.MustParsePrefix("10.0.0.0/8") {
		t.Fatalf("unexpected first proxy: %v", cfg.TrustedProxyCIDRs[0])
	}
	if len(cfg.CSRFAllowedOrigins) != 3 {
		t.Fatalf("unexpected origins: %v", cfg.CSRFAllowedOrigins)
	}
	if cfg.HTTPReadHeaderTimeout != 7*time.Second ||
		cfg.HTTPReadTimeout != 17*time.Second ||
		cfg.HTTPWriteTimeout != 37*time.Second ||
		cfg.HTTPIdleTimeout != 67*time.Second ||
		cfg.HTTPMaxHeaderBytes != 65536 {
		t.Fatalf("unexpected HTTP limits: %+v", cfg)
	}
	if cfg.HSTSEnabled {
		t.Fatal("HSTS should be disabled by default in local")
	}
	if cfg.CookieSecure {
		t.Fatal("secure cookie should be disabled by default in local")
	}
	if cfg.CookieSameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected SameSite: %v", cfg.CookieSameSite)
	}
}

func TestLoadRejectsInvalidTrustedProxyCIDR(t *testing.T) {
	setBaseEnv(t, "local", "http://localhost:3000")
	t.Setenv("TRUSTED_PROXY_CIDRS", "not-a-cidr")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXY_CIDRS") {
		t.Fatalf("expected CIDR error, got=%v", err)
	}
}

func setBaseEnv(t *testing.T, appEnv, frontendURL string) {
	t.Helper()

	required := map[string]string{
		"APP_ENV":               appEnv,
		"DATABASE_URL":          "postgres://user:pass@localhost:5432/vaultsend",
		"AWS_REGION":            "ap-northeast-1",
		"S3_BUCKET":             "vaultsend-test",
		"SQS_QUEUE_URL":         "https://sqs.ap-northeast-1.amazonaws.com/123456789012/test",
		"SES_FROM_EMAIL":        "noreply@example.go.jp",
		"FRONTEND_URL":          frontendURL,
		"STRIPE_SECRET_KEY":     "sk_test_example",
		"STRIPE_WEBHOOK_SECRET": "whsec_example",
		"STRIPE_PRICE_ID_PRO":   "price_example",
		"ACCESS_GRANT_SECRET":   "01234567890123456789012345678901",
	}
	for key, value := range required {
		t.Setenv(key, value)
	}

	optional := []string{
		"COOKIE_DOMAIN",
		"COOKIE_SECURE",
		"COOKIE_SAMESITE",
		"HSTS_ENABLED",
		"TRUSTED_PROXY_CIDRS",
		"CSRF_ALLOWED_ORIGINS",
		"SESSION_TTL_HOURS",
		"HTTP_REQUEST_TIMEOUT_SEC",
		"HTTP_READ_HEADER_TIMEOUT_SEC",
		"HTTP_READ_TIMEOUT_SEC",
		"HTTP_WRITE_TIMEOUT_SEC",
		"HTTP_IDLE_TIMEOUT_SEC",
		"HTTP_MAX_HEADER_BYTES",
		"INTERNAL_METRICS_TOKEN",
		"INTERNAL_METRICS_QUERY_TIMEOUT_SEC",
		"UPLOAD_URL_TTL_SEC",
		"PRESIGNED_URL_TTL",
		"ACCESS_GRANT_TTL_SEC",
		"RATE_LIMIT_RPS",
		"VERIFY_MAX_ATTEMPTS",
		"DOWNLOAD_RATE_LIMIT",
		"CLEANUP_INTERVAL_SEC",
		"CLEANUP_BATCH_SIZE",
		"DELETION_GRACE_PERIOD_HOURS",
	}
	for _, key := range optional {
		t.Setenv(key, "")
	}
}
