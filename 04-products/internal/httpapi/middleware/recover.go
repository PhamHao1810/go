package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"golearn/04-products/internal/httpapi/response"
)

// Recover bắt panic để 1 request lỗi không làm sập cả server; trả 500 đúng format kèm request_id.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler { // net/http dùng panic này để chủ động huỷ response; phải để nó đi tiếp
					panic(rec)
				}
				log.ErrorContext(r.Context(), "panic", "err", rec, "stack", string(debug.Stack()))
				response.Error(w, r, fmt.Errorf("panic: %v", rec))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
