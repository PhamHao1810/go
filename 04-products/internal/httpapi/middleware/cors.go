package middleware

import (
	"net/http"
	"slices"
)

// CORS cho phép frontend ở domain khác gọi API. origins rỗng = tắt; "*" = mọi origin.
func CORS(origins []string) Middleware {
	allowAll := slices.Contains(origins, "*")
	return func(next http.Handler) http.Handler {
		if len(origins) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && (allowAll || slices.Contains(origins, origin)) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
				h.Set("Access-Control-Expose-Headers", RequestIDHeader)
				if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" { // preflight
					h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					h.Set("Access-Control-Allow-Headers", "Content-Type, "+RequestIDHeader)
					h.Set("Access-Control-Max-Age", "600")
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
