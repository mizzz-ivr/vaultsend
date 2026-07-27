package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type SecurityAuditOutboxConfig struct {
	DatabaseURL      string
	PollInterval     time.Duration
	BatchSize        int32
	Retention        time.Duration
	CleanupBatchSize int32
}

func LoadSecurityAuditOutboxConfig() (SecurityAuditOutboxConfig, error) {
	databaseURL, err := secretEnvOrFile("DATABASE_URL", "DATABASE_URL_FILE")
	if err != nil {
		return SecurityAuditOutboxConfig{}, err
	}
	cfg := SecurityAuditOutboxConfig{
		DatabaseURL:      strings.TrimSpace(databaseURL),
		PollInterval:     2 * time.Second,
		BatchSize:        100,
		Retention:        7 * 24 * time.Hour,
		CleanupBatchSize: 500,
	}
	if cfg.DatabaseURL == "" {
		return SecurityAuditOutboxConfig{}, fmt.Errorf("DATABASE_URL or DATABASE_URL_FILE is required")
	}

	if cfg.PollInterval, err = positiveDurationSecondsEnv("AUDIT_OUTBOX_POLL_INTERVAL_SEC", cfg.PollInterval); err != nil {
		return SecurityAuditOutboxConfig{}, err
	}
	batchSize, err := positiveIntEnv("AUDIT_OUTBOX_BATCH_SIZE", int(cfg.BatchSize))
	if err != nil {
		return SecurityAuditOutboxConfig{}, err
	}
	cfg.BatchSize = int32(batchSize)
	retentionHours, err := positiveIntEnv("AUDIT_OUTBOX_RETENTION_HOURS", int(cfg.Retention/time.Hour))
	if err != nil {
		return SecurityAuditOutboxConfig{}, err
	}
	cfg.Retention = time.Duration(retentionHours) * time.Hour
	cleanupBatchSize, err := positiveIntEnv("AUDIT_OUTBOX_CLEANUP_BATCH_SIZE", int(cfg.CleanupBatchSize))
	if err != nil {
		return SecurityAuditOutboxConfig{}, err
	}
	cfg.CleanupBatchSize = int32(cleanupBatchSize)
	return cfg, nil
}

var _ = os.Getenv
