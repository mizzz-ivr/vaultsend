package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadSecurityAuditOutboxConfigDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("DATABASE_URL_FILE", "")
	t.Setenv("AUDIT_OUTBOX_POLL_INTERVAL_SEC", "")
	t.Setenv("AUDIT_OUTBOX_BATCH_SIZE", "")
	t.Setenv("AUDIT_OUTBOX_RETENTION_HOURS", "")
	t.Setenv("AUDIT_OUTBOX_CLEANUP_BATCH_SIZE", "")

	cfg, err := LoadSecurityAuditOutboxConfig()
	if err != nil {
		t.Fatalf("設定読み込みに失敗しました: %v", err)
	}
	if cfg.DatabaseURL != "postgres://example" {
		t.Fatalf("DATABASE_URLが反映されていません: %q", cfg.DatabaseURL)
	}
	if cfg.PollInterval != 2*time.Second || cfg.BatchSize != 100 {
		t.Fatalf("既定値が不正です: %#v", cfg)
	}
	if cfg.Retention != 7*24*time.Hour || cfg.CleanupBatchSize != 500 {
		t.Fatalf("retention既定値が不正です: %#v", cfg)
	}
}

func TestLoadSecurityAuditOutboxConfigReadsDatabaseURLFile(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "database-url")
	if err := os.WriteFile(secretPath, []byte("postgres://worker-secret\n"), 0o600); err != nil {
		t.Fatalf("Secretファイルを作成できません: %v", err)
	}
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_URL_FILE", secretPath)

	cfg, err := LoadSecurityAuditOutboxConfig()
	if err != nil {
		t.Fatalf("Secretファイル設定の読み込みに失敗しました: %v", err)
	}
	if cfg.DatabaseURL != "postgres://worker-secret" {
		t.Fatalf("Secretファイルの値が不正です: %q", cfg.DatabaseURL)
	}
}

func TestLoadSecurityAuditOutboxConfigRejectsDatabaseURLAndFileTogether(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "database-url")
	if err := os.WriteFile(secretPath, []byte("postgres://worker-secret"), 0o600); err != nil {
		t.Fatalf("Secretファイルを作成できません: %v", err)
	}
	t.Setenv("DATABASE_URL", "postgres://inline")
	t.Setenv("DATABASE_URL_FILE", secretPath)

	_, err := LoadSecurityAuditOutboxConfig()
	if err == nil || !strings.Contains(err.Error(), "cannot be set at the same time") {
		t.Fatalf("同時指定が拒否されていません: %v", err)
	}
}

func TestLoadSecurityAuditOutboxConfigRejectsEmptyDatabaseURLFile(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "database-url")
	if err := os.WriteFile(secretPath, []byte("\n"), 0o600); err != nil {
		t.Fatalf("Secretファイルを作成できません: %v", err)
	}
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_URL_FILE", secretPath)

	if _, err := LoadSecurityAuditOutboxConfig(); err == nil {
		t.Fatal("空のSecretファイルが受理されました")
	}
}

func TestLoadSecurityAuditOutboxConfigRejectsOversizedDatabaseURLFile(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "database-url")
	if err := os.WriteFile(secretPath, []byte(strings.Repeat("x", maxSecretFileBytes+1)), 0o600); err != nil {
		t.Fatalf("Secretファイルを作成できません: %v", err)
	}
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_URL_FILE", secretPath)

	if _, err := LoadSecurityAuditOutboxConfig(); err == nil {
		t.Fatal("上限超過のSecretファイルが受理されました")
	}
}

func TestLoadSecurityAuditOutboxConfigRejectsInvalidBatchSize(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("DATABASE_URL_FILE", "")
	t.Setenv("AUDIT_OUTBOX_BATCH_SIZE", "0")
	if _, err := LoadSecurityAuditOutboxConfig(); err == nil {
		t.Fatal("不正なbatch sizeが受理されました")
	}
}

func TestLoadSecurityAuditOutboxConfigRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_URL_FILE", "")
	if _, err := LoadSecurityAuditOutboxConfig(); err == nil {
		t.Fatal("DATABASE_URL未設定が受理されました")
	}
}
