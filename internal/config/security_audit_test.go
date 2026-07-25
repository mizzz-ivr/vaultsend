package config

import "testing"

func TestLoadSecurityAuditConfigUsesLocalDefaults(t *testing.T) {
	t.Setenv("AUDIT_LOG_HMAC_SECRET", "")
	t.Setenv("AUDIT_LOG_HMAC_KEY_ID", "")
	cfg, err := LoadSecurityAuditConfig("local")
	if err != nil {
		t.Fatalf("local既定値の読込に失敗しました: %v", err)
	}
	if len(cfg.HMACSecret) < 32 {
		t.Fatal("local既定secretが短すぎます")
	}
	if cfg.HMACKeyID != "local-v1" {
		t.Fatalf("unexpected key id: %s", cfg.HMACKeyID)
	}
}

func TestLoadSecurityAuditConfigRequiresProductionSecret(t *testing.T) {
	t.Setenv("AUDIT_LOG_HMAC_SECRET", "")
	t.Setenv("AUDIT_LOG_HMAC_KEY_ID", "prod-v1")
	if _, err := LoadSecurityAuditConfig("production"); err == nil {
		t.Fatal("本番でsecret未設定を拒否していません")
	}
}

func TestLoadSecurityAuditConfigRejectsShortSecret(t *testing.T) {
	t.Setenv("AUDIT_LOG_HMAC_SECRET", "too-short")
	t.Setenv("AUDIT_LOG_HMAC_KEY_ID", "prod-v1")
	if _, err := LoadSecurityAuditConfig("production"); err == nil {
		t.Fatal("短いsecretを拒否していません")
	}
}

func TestLoadSecurityAuditConfigRejectsInvalidKeyID(t *testing.T) {
	t.Setenv("AUDIT_LOG_HMAC_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("AUDIT_LOG_HMAC_KEY_ID", "invalid key id")
	if _, err := LoadSecurityAuditConfig("production"); err == nil {
		t.Fatal("不正なkey idを拒否していません")
	}
}
