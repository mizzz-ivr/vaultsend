package middleware

import (
	"log"
	"net/http"
	"net/netip"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// RequestID は各リクエストに一意IDを付与する。
func RequestID(next http.Handler) http.Handler {
	return chimw.RequestID(next)
}

// Recovery は panic を捕捉して 500 を返す。
func Recovery(next http.Handler) http.Handler {
	return chimw.Recoverer(next)
}

// Timeout はハンドラの最大処理時間を制限する。
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return chimw.Timeout(d)
}

// RequestLogger はトークンや生IPを記録せず、追跡可能なアクセスログを出力する。
func RequestLogger(trustedProxyCIDRs []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			reqID := chimw.GetReqID(r.Context())
			log.Printf(
				"event=http_request request_id=%s method=%s endpoint=%q status=%d bytes=%d duration_ms=%d client_ip_hash=%s",
				reqID,
				r.Method,
				normalizedRateLimitEndpoint(r.Method, r.URL.Path),
				ww.Status(),
				ww.BytesWritten(),
				time.Since(start).Milliseconds(),
				ClientIPHashFromRequest(r, trustedProxyCIDRs),
			)
		})
	}
}
