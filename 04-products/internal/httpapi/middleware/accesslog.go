package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// AccessLog ghi log theo mức độ: lỗi 5xx → ERROR, chậm (> slow) → WARN, còn lại → DEBUG.
// Ở hàng chục nghìn RPS, log mọi request ở mức INFO rất tốn CPU/IO; bật LOG_LEVEL=debug khi cần soi.
func AccessLog(log *slog.Logger, slow time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			d := time.Since(start)

			level := slog.LevelDebug
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case d > slow:
				level = slog.LevelWarn
			}
			log.Log(r.Context(), level, "http request",
				"method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", d.Milliseconds())
		})
	}
}

// statusRecorder nhúng (embed) http.ResponseWriter để ghi nhớ status code đã trả.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap cho http.ResponseController truy cập writer gốc (Flush, SetWriteDeadline...).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
