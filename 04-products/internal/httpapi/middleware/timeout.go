package middleware

import (
	"context"
	"net/http"
	"time"
)

// Timeout gắn hạn chót vào ctx của request. Khi quá tải, request không xếp hàng chờ connection DB mãi
// mà fail nhanh (503) — giữ latency ổn định thay vì sập dây chuyền.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
