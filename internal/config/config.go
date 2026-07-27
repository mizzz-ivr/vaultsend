package config

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config はアプリケーション全体で利用する設定値を保持する。
// 環境変数ベースで管理し、Secret値は実行環境のSecret Manager等から注入する。
type Config struct {
	AppEnv              string
	Port                string
	DatabaseURL         string
	AWSRegion           string
	S3Bucket            string
	SQSQueueURL         string
	SESFromEmail        string
	FrontendURL         string
	StripeSecretKey     string
	StripeWebhookSecret string
	StripePriceIDPro    string
	AccessGrantSecret   string
	InternalMetricsToken string

	// HTTPサーバー・middleware関連。
	HTTPRequestTimeout          time.Duration
	HTTPReadHeaderTimeout       time.Duration
	HTTPReadTimeout             time.Duration
	HTTPWriteTimeout            time.Duration
	HTTPIdleTimeout             time.Duration
	InternalMetricsQueryTimeout time.Duration
	HTTPMaxHeaderBytes          int
	HSTSEnabled                 bool
	TrustedProxyCIDRs           []netip.Prefix
	CSRFAllowedOrigins          []string

	UploadURLTTL      time.Duration
	PresignedURLTTL   time.Duration
	AccessGrantTTL    time.Duration
	UploadPartSize    int32
	UploadMaxFileSize int64
	UploadMaxParts    int

	RateLimitRPS        int
	VerifyMaxAttempts   int
	DownloadRateLimit   int
	CleanupInterval     time.Duration
	CleanupBatchSize    int32
	DeletionGracePeriod time.Duration

	// 認証セッション関連。
	SessionTTLHours int
	CookieDomain    string
	CookieSecure    bool
	CookieSameSite  http.SameSite
}

func Load() (Config, error) {
	cfg := Config{
		AppEnv:                     getEnv("APP_ENV", "local"),
		Port:                       getEnv("PORT", "8080"),
		DatabaseURL:                os.Getenv("DATABASE_URL"),
		AWSRegion:                  os.Getenv("AWS_REGION"),
		S3Bucket:                   os.Getenv("S3_BUCKET"),
		SQSQueueURL:                os.Getenv("SQS_QUEUE_URL"),
		SESFromEmail:               os.Getenv("SES_FROM_EMAIL"),
		FrontendURL:                strings.TrimSpace(os.Getenv("FRONTEND_URL")),
		StripeSecretKey:            os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret:        os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripePriceIDPro:           os.Getenv("STRIPE_PRICE_ID_PRO"),
		AccessGrantSecret:          strings.TrimSpace(os.Getenv("ACCESS_GRANT_SECRET")),
		InternalMetricsToken:       strings.TrimSpace(os.Getenv("INTERNAL_METRICS_TOKEN")),
		HTTPRequestTimeout:         30 * time.Second,
		HTTPReadHeaderTimeout:      5 * time.Second,
		HTTPReadTimeout:            15 * time.Second,
		HTTPWriteTimeout:           35 * time.Second,
		HTTPIdleTimeout:            60 * time.Second,
		InternalMetricsQueryTimeout: 3 * time.Second,
		HTTPMaxHeaderBytes:         32 * 1024,
		HSTSEnabled:                true,
		UploadURLTTL:               15 * time.Minute,
		PresignedURLTTL:            60 * time.Second,
		AccessGrantTTL:             10 * time.Minute,
		UploadPartSize:             8 * 1024 * 1024,
		UploadMaxFileSize:          10 * 1024 * 1024 * 1024,
		UploadMaxParts:             1000,
		RateLimitRPS:               100,
		VerifyMaxAttempts:          5,
		DownloadRateLimit:          10,
		CleanupInterval:            3 * time.Minute,
		CleanupBatchSize:           100,
		DeletionGracePeriod:        24 * time.Hour,
		SessionTTLHours:            24 * 7,
		CookieDomain:               strings.TrimSpace(os.Getenv("COOKIE_DOMAIN")),
		CookieSecure:               true,
		CookieSameSite:             http.SameSiteLaxMode,
	}

	if cfg.AppEnv == "local" || cfg.AppEnv == "test" {
		cfg.CookieSecure = false
		cfg.HSTSEnabled = false
		if cfg.AccessGrantSecret == "" {
			cfg.AccessGrantSecret = "local-development-access-grant-secret-change-me"
		}
	}

	if v := os.Getenv("COOKIE_SECURE"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid COOKIE_SECURE: %q", v)
		}
		cfg.CookieSecure = parsed
	}
	if v := os.Getenv("HSTS_ENABLED"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid HSTS_ENABLED: %q", v)
		}
		cfg.HSTSEnabled = parsed
	}
	if v := os.Getenv("COOKIE_SAMESITE"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "lax":
			cfg.CookieSameSite = http.SameSiteLaxMode
		case "strict":
			cfg.CookieSameSite = http.SameSiteStrictMode
		case "none":
			cfg.CookieSameSite = http.SameSiteNoneMode
		default:
			return Config{}, fmt.Errorf("invalid COOKIE_SAMESITE: %q", v)
		}
	}
	if cfg.CookieSameSite == http.SameSiteNoneMode && !cfg.CookieSecure {
		return Config{}, fmt.Errorf("COOKIE_SAMESITE=none requires COOKIE_SECURE=true")
	}

	if v := os.Getenv("TRUSTED_PROXY_CIDRS"); strings.TrimSpace(v) != "" {
		prefixes, err := parseCIDRs(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid TRUSTED_PROXY_CIDRS: %w", err)
		}
		cfg.TrustedProxyCIDRs = prefixes
	}

	var err error
	if cfg.SessionTTLHours, err = positiveIntEnv("SESSION_TTL_HOURS", cfg.SessionTTLHours); err != nil {
		return Config{}, err
	}
	if cfg.HTTPRequestTimeout, err = positiveDurationSecondsEnv("HTTP_REQUEST_TIMEOUT_SEC", cfg.HTTPRequestTimeout); err != nil {
		return Config{}, err
	}
	if cfg.HTTPReadHeaderTimeout, err = positiveDurationSecondsEnv("HTTP_READ_HEADER_TIMEOUT_SEC", cfg.HTTPReadHeaderTimeout); err != nil {
		return Config{}, err
	}
	if cfg.HTTPReadTimeout, err = positiveDurationSecondsEnv("HTTP_READ_TIMEOUT_SEC", cfg.HTTPReadTimeout); err != nil {
		return Config{}, err
	}
	if cfg.HTTPWriteTimeout, err = positiveDurationSecondsEnv("HTTP_WRITE_TIMEOUT_SEC", cfg.HTTPWriteTimeout); err != nil {
		return Config{}, err
	}
	if cfg.HTTPIdleTimeout, err = positiveDurationSecondsEnv("HTTP_IDLE_TIMEOUT_SEC", cfg.HTTPIdleTimeout); err != nil {
		return Config{}, err
	}
	if cfg.InternalMetricsQueryTimeout, err = positiveDurationSecondsEnv("INTERNAL_METRICS_QUERY_TIMEOUT_SEC", cfg.InternalMetricsQueryTimeout); err != nil {
		return Config{}, err
	}
	if cfg.HTTPMaxHeaderBytes, err = positiveIntEnv("HTTP_MAX_HEADER_BYTES", cfg.HTTPMaxHeaderBytes); err != nil {
		return Config{}, err
	}
	if cfg.UploadURLTTL, err = positiveDurationSecondsEnv("UPLOAD_URL_TTL_SEC", cfg.UploadURLTTL); err != nil {
		return Config{}, err
	}
	if cfg.PresignedURLTTL, err = positiveDurationSecondsEnv("PRESIGNED_URL_TTL", cfg.PresignedURLTTL); err != nil {
		return Config{}, err
	}
	if cfg.AccessGrantTTL, err = positiveDurationSecondsEnv("ACCESS_GRANT_TTL_SEC", cfg.AccessGrantTTL); err != nil {
		return Config{}, err
	}
	if cfg.RateLimitRPS, err = positiveIntEnv("RATE_LIMIT_RPS", cfg.RateLimitRPS); err != nil {
		return Config{}, err
	}
	if cfg.VerifyMaxAttempts, err = positiveIntEnv("VERIFY_MAX_ATTEMPTS", cfg.VerifyMaxAttempts); err != nil {
		return Config{}, err
	}
	if cfg.DownloadRateLimit, err = positiveIntEnv("DOWNLOAD_RATE_LIMIT", cfg.DownloadRateLimit); err != nil {
		return Config{}, err
	}
	if cfg.CleanupInterval, err = positiveDurationSecondsEnv("CLEANUP_INTERVAL_SEC", cfg.CleanupInterval); err != nil {
		return Config{}, err
	}
	cleanupBatchSize, err := positiveIntEnv("CLEANUP_BATCH_SIZE", int(cfg.CleanupBatchSize))
	if err != nil {
		return Config{}, err
	}
	cfg.CleanupBatchSize = int32(cleanupBatchSize)
	deletionGraceHours, err := positiveIntEnv("DELETION_GRACE_PERIOD_HOURS", int(cfg.DeletionGracePeriod/time.Hour))
	if err != nil {
		return Config{}, err
	}
	cfg.DeletionGracePeriod = time.Duration(deletionGraceHours) * time.Hour

	if cfg.InternalMetricsToken != "" {
		if len(cfg.InternalMetricsToken) < 32 {
			return Config{}, fmt.Errorf("INTERNAL_METRICS_TOKEN must be at least 32 bytes when configured")
		}
		if strings.ContainsAny(cfg.InternalMetricsToken, " \t\r\n") {
			return Config{}, fmt.Errorf("INTERNAL_METRICS_TOKEN must not contain whitespace")
		}
	}

	missing := make([]string, 0)
	if cfg.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if cfg.AWSRegion == "" {
		missing = append(missing, "AWS_REGION")
	}
	if cfg.S3Bucket == "" {
		missing = append(missing, "S3_BUCKET")
	}
	if cfg.SQSQueueURL == "" {
		missing = append(missing, "SQS_QUEUE_URL")
	}
	if cfg.SESFromEmail == "" {
		missing = append(missing, "SES_FROM_EMAIL")
	}
	if cfg.FrontendURL == "" {
		missing = append(missing, "FRONTEND_URL")
	}
	if cfg.StripeSecretKey == "" {
		missing = append(missing, "STRIPE_SECRET_KEY")
	}
	if cfg.StripeWebhookSecret == "" {
		missing = append(missing, "STRIPE_WEBHOOK_SECRET")
	}
	if cfg.StripePriceIDPro == "" {
		missing = append(missing, "STRIPE_PRICE_ID_PRO")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required envs: %v", missing)
	}

	frontendOrigin, frontendScheme, err := configuredOrigin(cfg.FrontendURL)
	if err != nil {
		return Config{}, fmt.Errorf("invalid FRONTEND_URL: %w", err)
	}
	origins := []string{frontendOrigin}
	if raw := strings.TrimSpace(os.Getenv("CSRF_ALLOWED_ORIGINS")); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			origin, _, originErr := configuredOrigin(strings.TrimSpace(item))
			if originErr != nil {
				return Config{}, fmt.Errorf("invalid CSRF_ALLOWED_ORIGINS: %w", originErr)
			}
			origins = append(origins, origin)
		}
	}
	cfg.CSRFAllowedOrigins = uniqueStrings(origins)

	if cfg.AppEnv != "local" && cfg.AppEnv != "test" {
		if !cfg.CookieSecure {
			return Config{}, fmt.Errorf("COOKIE_SECURE must be true outside local/test")
		}
		if !cfg.HSTSEnabled {
			return Config{}, fmt.Errorf("HSTS_ENABLED must be true outside local/test")
		}
		if frontendScheme != "https" {
			return Config{}, fmt.Errorf("FRONTEND_URL must use https outside local/test")
		}
	}

	return cfg, nil
}

func positiveIntEnv(key string, fallback int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid %s: %q", key, v)
	}
	return n, nil
}

func positiveDurationSecondsEnv(key string, fallback time.Duration) (time.Duration, error) {
	seconds, err := positiveIntEnv(key, int(fallback/time.Second))
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
}

func parseCIDRs(raw string) ([]netip.Prefix, error) {
	parts := strings.Split(raw, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", value, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("at least one CIDR is required")
	}
	return prefixes, nil
}

func configuredOrigin(raw string) (origin string, scheme string, err error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return "", "", fmt.Errorf("origin must include scheme and host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), strings.ToLower(parsed.Scheme), nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
