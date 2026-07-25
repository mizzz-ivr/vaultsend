package middleware

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// CSRFConfig はCookie認証リクエストで許可するWeb Originを保持する。
type CSRFConfig struct {
	AllowedOrigins []string
}

// CSRFProtection はFetch MetadataとOriginを用いて、Cookie認証された更新系リクエストを保護する。
// Cookieを利用しないサーバー間通信はOriginがない場合に限り許可する。
func CSRFProtection(cfg CSRFConfig) func(http.Handler) http.Handler {
	allowedOrigins := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, origin := range cfg.AllowedOrigins {
		if normalized, ok := normalizeOrigin(origin); ok {
			allowedOrigins[normalized] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
				writeCSRFError(w, r)
				return
			}

			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" {
				normalized, ok := normalizeOrigin(origin)
				if !ok {
					writeCSRFError(w, r)
					return
				}
				if _, allowed := allowedOrigins[normalized]; !allowed {
					writeCSRFError(w, r)
					return
				}
			}

			cookie, cookieErr := r.Cookie(SessionCookieName)
			if cookieErr == nil && strings.TrimSpace(cookie.Value) != "" && origin == "" {
				writeCSRFError(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func normalizeOrigin(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", false
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), true
}

func writeCSRFError(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":      "csrf_validation_failed",
		"code":       "csrf_validation_failed",
		"message":    "リクエストの送信元を確認できません",
		"request_id": chimw.GetReqID(r.Context()),
	})
}
