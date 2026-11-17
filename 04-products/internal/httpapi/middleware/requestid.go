package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"golearn/04-products/internal/logger"
)

const RequestIDHeader = "X-Request-ID"

// RequestID nhận X-Request-ID từ client/LB (nếu hợp lệ) hoặc tự sinh, gắn vào ctx (để log) và header trả về.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validID(id) {
				id = newID()
			}
			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(logger.WithRequestID(r.Context(), id)))
		})
	}
}

// Chỉ nhận ký tự an toàn, tối đa 64: chặn client chèn chuỗi rác/xuống dòng vào log.
func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
