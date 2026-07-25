package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var auditKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,49}$`)

type SecurityAuditConfig struct {
	HMACSecret []byte
	HMACKeyID  string
}

func LoadSecurityAuditConfig(appEnv string) (SecurityAuditConfig, error) {
	appEnv = strings.TrimSpace(strings.ToLower(appEnv))
	secret := strings.TrimSpace(os.Getenv("AUDIT_LOG_HMAC_SECRET"))
	keyID := strings.TrimSpace(os.Getenv("AUDIT_LOG_HMAC_KEY_ID"))

	if appEnv == "local" || appEnv == "test" {
		if secret == "" {
			secret = "local-development-audit-hmac-secret-change-me"
		}
		if keyID == "" {
			keyID = "local-v1"
		}
	}
	if secret == "" {
		return SecurityAuditConfig{}, fmt.Errorf("AUDIT_LOG_HMAC_SECRET is required outside local/test")
	}
	if len([]byte(secret)) < 32 {
		return SecurityAuditConfig{}, fmt.Errorf("AUDIT_LOG_HMAC_SECRET must be at least 32 bytes")
	}
	if !auditKeyIDPattern.MatchString(keyID) {
		return SecurityAuditConfig{}, fmt.Errorf("AUDIT_LOG_HMAC_KEY_ID is invalid")
	}
	return SecurityAuditConfig{HMACSecret: []byte(secret), HMACKeyID: keyID}, nil
}
