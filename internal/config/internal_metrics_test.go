package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadParsesInternalMetricsConfig(t *testing.T) {
	setBaseEnv(t, "local", "http://localhost:3000")
	t.Setenv("INTERNAL_METRICS_TOKEN", "01234567890123456789012345678901")
	t.Setenv("INTERNAL_METRICS_QUERY_TIMEOUT_SEC", "5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InternalMetricsToken != "01234567890123456789012345678901" {
		t.Fatalf("unexpected metrics token: %q", cfg.InternalMetricsToken)
	}
	if cfg.InternalMetricsQueryTimeout != 5*time.Second {
		t.Fatalf("unexpected metrics query timeout: %s", cfg.InternalMetricsQueryTimeout)
	}
}

func TestLoadAllowsDisabledInternalMetrics(t *testing.T) {
	setBaseEnv(t, "production", "https://app.example.go.jp")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InternalMetricsToken != "" {
		t.Fatalf("metrics must be disabled when token is empty: %q", cfg.InternalMetricsToken)
	}
}

func TestLoadRejectsShortInternalMetricsToken(t *testing.T) {
	setBaseEnv(t, "local", "http://localhost:3000")
	t.Setenv("INTERNAL_METRICS_TOKEN", "too-short")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("expected token length error, got=%v", err)
	}
}

func TestLoadRejectsInternalMetricsTokenWithWhitespace(t *testing.T) {
	setBaseEnv(t, "local", "http://localhost:3000")
	t.Setenv("INTERNAL_METRICS_TOKEN", "0123456789012345678901234567890 token")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "must not contain whitespace") {
		t.Fatalf("expected token whitespace error, got=%v", err)
	}
}

func TestLoadRejectsInvalidInternalMetricsQueryTimeout(t *testing.T) {
	setBaseEnv(t, "local", "http://localhost:3000")
	t.Setenv("INTERNAL_METRICS_QUERY_TIMEOUT_SEC", "0")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "INTERNAL_METRICS_QUERY_TIMEOUT_SEC") {
		t.Fatalf("expected query timeout error, got=%v", err)
	}
}
