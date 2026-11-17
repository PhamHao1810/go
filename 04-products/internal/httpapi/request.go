package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"golearn/04-products/internal/domain"
	"golearn/04-products/internal/httpapi/middleware"
)

// Guards: các "cổng" quyền dùng khi đăng ký route (xem Register của từng handler).
type Guards struct {
	User  middleware.Middleware // đã đăng nhập (mọi vai trò)
	Admin middleware.Middleware // chỉ admin
}

// handle đăng ký route kèm guard (nếu có): handle(mux, "POST /x", h.create, g.Admin).
func handle(mux *http.ServeMux, pattern string, h http.HandlerFunc, mws ...middleware.Middleware) {
	mux.Handle(pattern, middleware.Chain(h, mws...))
}

// actor: người đang gọi API. Chỉ gọi trong handler đã có guard → luôn có.
func actor(r *http.Request) domain.Principal {
	p, _ := middleware.PrincipalFrom(r.Context())
	return p
}

const maxBodyBytes = 1 << 20

// decode đọc JSON chặt chẽ: giới hạn 1MB, từ chối field lạ (bắt lỗi gõ sai tên field phía client).
func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return domain.Invalid("BODY_TOO_LARGE", "request body must not exceed 1MB")
		}
		return domain.Invalid("INVALID_JSON", "invalid JSON body: "+err.Error())
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		return 0, domain.Invalid("INVALID_ID", name+" must be a positive integer")
	}
	return id, nil
}
