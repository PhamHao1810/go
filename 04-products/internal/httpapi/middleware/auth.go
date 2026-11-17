package middleware

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"golearn/04-products/internal/domain"
	"golearn/04-products/internal/httpapi/response"
)

type TokenVerifier interface {
	VerifyAccess(token string) (domain.Principal, error)
}

type principalKey struct{}

// PrincipalFrom lấy người dùng đã xác thực; chỉ có trong handler được bọc bởi Authenticate.
func PrincipalFrom(ctx context.Context) (domain.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(domain.Principal)
	return p, ok
}

// Authenticate (tương đương Guard của NestJS): bắt buộc header "Authorization: Bearer <access token>" hợp lệ.
// roles rỗng = mọi người đã đăng nhập; có roles = chỉ những vai trò đó (sai vai trò → 403, chưa đăng nhập → 401).
func Authenticate(v TokenVerifier, roles ...domain.Role) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || token == "" {
				w.Header().Set("WWW-Authenticate", "Bearer")
				response.Error(w, r, domain.ErrUnauthenticated)
				return
			}
			p, err := v.VerifyAccess(token)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				response.Error(w, r, err)
				return
			}
			if len(roles) > 0 && !slices.Contains(roles, p.Role) {
				response.Error(w, r, domain.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
		})
	}
}
