package config

import (
	"testing"
	"time"
)

func TestLoadSecurityAuditOutboxConfigDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("AUDIT_OUTBOX_POLL_INTERVAL_SEC", "")
	t.Setenv("AUDIT_OUTBOX_BATCH_SIZE", "")
	t.Setenv("AUDIT_OUTBOX_RETENTION_HOURS", "")
	t.Setenv("AUDIT_OUTBOX_CLEANUP_BATCH_SIZE", "")

	cfg, err := LoadSecurityAuditOutboxConfig()
	if err != nil {
		t.Fatalf("設定読み込みに失敗しました: %v", err)
	}
	if cfg.PollInterval != 2*time.Second || cfg.BatchSize != 100 {
		t.Fatalf("既定値が不正です: %#v", cfg)
	}
	if cfg.Retention != 7*24*time.Hour || cfg.CleanupBatchSize != 500 {
		t.Fatalf("retention既定値が不正です: %#v", cfg)
	}
}

func TestLoadSecurityAuditOutboxConfigRejectsInvalidBatchSize(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("AUDIT_OUTBOX_BATCH_SIZE", "0")
	if _, err := LoadSecurityAuditOutboxConfig(); err == nil {
		t.Fatal("不正なbatch sizeが受理されました")
	}
}

func TestLoadSecurityAuditOutboxConfigRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := LoadSecurityAuditOutboxConfig(); err == nil {
		t.Fatal("DATABASE_URL未設定が受理されました")
	}
}
