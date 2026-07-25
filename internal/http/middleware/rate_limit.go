package middleware

import (
	"log"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

const defaultRateLimiterMaxEntries = 100000

// RateLimitConfig はレート制限の設定を保持する。
type RateLimitConfig struct {
	PerMinuteLimit    int
	VerifyLimit       int
	TrustedProxyCIDRs []netip.Prefix
}

type rateCounter struct {
	Count     int
	WindowEnd time.Time
}

// InMemoryRateLimiter は単一プロセス向けの固定窓レート制限実装。
type InMemoryRateLimiter struct {
	mu         sync.Mutex
	counters   map[string]rateCounter
	now        func() time.Time
	maxEntries int
}

func NewInMemoryRateLimiter(maxEntries ...int) *InMemoryRateLimiter {
	limit := defaultRateLimiterMaxEntries
	if len(maxEntries) > 0 && maxEntries[0] > 0 {
		limit = maxEntries[0]
	}
	return &InMemoryRateLimiter{
		counters:   map[string]rateCounter{},
		now:        time.Now,
		maxEntries: limit,
	}
}

func (l *InMemoryRateLimiter) allow(key string, limit int, window time.Duration) bool {
	if limit <= 0 {
		return true
	}
	now := l.now().UTC()

	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.counters[key]
	if !ok || !now.Before(entry.WindowEnd) {
		if !ok && len(l.counters) >= l.maxEntries {
			l.removeExpired(now)
			if len(l.counters) >= l.maxEntries {
				return false
			}
		}
		l.counters[key] = rateCounter{Count: 1, WindowEnd: now.Add(window)}
		return true
	}
	if entry.Count >= limit {
		return false
	}
	entry.Count++
	l.counters[key] = entry
	return true
}

func (l *InMemoryRateLimiter) removeExpired(now time.Time) {
	for key, entry := range l.counters {
		if !now.Before(entry.WindowEnd) {
			delete(l.counters, key)
		}
	}
}

// RateLimit は信頼済みプロキシを考慮したIP+エンドポイント単位で制御する。
func RateLimit(limiter *InMemoryRateLimiter, cfg RateLimitConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			endpoint := normalizedRateLimitEndpoint(r.Method, r.URL.Path)
			limit := cfg.PerMinuteLimit
			if endpoint == "POST /v1/access/{token}/verify" {
				limit = cfg.VerifyLimit
			}

			ip := ClientIP(r, cfg.TrustedProxyCIDRs)
			if ip == "" {
				ip = "unknown"
			}
			key := ip + "|" + endpoint
			if !limiter.allow(key, limit, time.Minute) {
				reqID := chimw.GetReqID(r.Context())
				log.Printf(
					"event=rate_limit_hit request_id=%s client_ip_hash=%s endpoint=%s limit=%d",
					reqID,
					ClientIPHash(ip),
					endpoint,
					limit,
				)
				w.Header().Set("Retry-After", "60")
				http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func normalizedRateLimitEndpoint(method, path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) >= 2 && segments[0] == "v1" {
		switch segments[1] {
		case "access":
			if len(segments) == 3 {
				return method + " /v1/access/{token}"
			}
			if len(segments) == 4 && segments[3] == "verify" {
				return method + " /v1/access/{token}/verify"
			}
		case "files":
			if len(segments) == 4 && segments[3] == "download-url" {
				return method + " /v1/files/{id}/download-url"
			}
		case "uploads":
			if len(segments) == 4 && segments[3] == "complete" {
				return method + " /v1/uploads/{id}/complete"
			}
		case "shipments":
			if len(segments) == 3 {
				return method + " /v1/shipments/{id}"
			}
			if len(segments) == 4 {
				return method + " /v1/shipments/{id}/" + segments[3]
			}
		case "orgs":
			if len(segments) == 3 {
				return method + " /v1/orgs/{id}"
			}
			if len(segments) == 4 {
				return method + " /v1/orgs/{id}/" + segments[3]
			}
			if len(segments) >= 5 {
				return method + " /v1/orgs/{id}/" + segments[3] + "/{resource_id}"
			}
		}
	}
	return method + " " + path
}
