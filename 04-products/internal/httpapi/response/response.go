// Package response: MỌI response JSON của API đi qua đây để có cùng 1 format.
//
//	Thành công: {"data": {...}}                      hoặc  {"data": [...], "meta": {...}}
//	Lỗi:        {"error": {"code": "...", "message": "...", "details": [...], "request_id": "..."}}
package response

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"golearn/04-products/internal/domain"
	"golearn/04-products/internal/logger"
)

type envelope struct {
	Data  any        `json:"data,omitempty"`
	Meta  any        `json:"meta,omitempty"`
	Error *errorBody `json:"error,omitempty"`
}

type errorBody struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Details   []fieldDetail `json:"details,omitempty"`
	RequestID string        `json:"request_id,omitempty"`
}

type fieldDetail struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // API JSON, không nhúng vào HTML: giữ nguyên "<", ">", "&" thay vì <...
	_ = enc.Encode(v)
}

func OK(w http.ResponseWriter, status int, data any) {
	JSON(w, status, envelope{Data: data})
}

func List(w http.ResponseWriter, data, meta any) {
	JSON(w, http.StatusOK, envelope{Data: data, Meta: meta})
}

func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

var statusOf = map[domain.Kind]int{
	domain.KindInvalid:         http.StatusBadRequest,
	domain.KindNotFound:        http.StatusNotFound,
	domain.KindConflict:        http.StatusConflict,
	domain.KindUnauthenticated: http.StatusUnauthorized,
	domain.KindForbidden:       http.StatusForbidden,
	domain.KindTooManyRequests: http.StatusTooManyRequests,
}

// Error map lỗi → HTTP status + mã lỗi. Lỗi nghiệp vụ trả nguyên message; lỗi không lường trước chỉ trả
// "internal server error" (không lộ chi tiết nội bộ) và được log kèm request_id để tra cứu.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	body := errorBody{Code: "INTERNAL_ERROR", Message: "internal server error"}

	var de *domain.Error
	switch {
	case errors.As(err, &de) && statusOf[de.Kind] != 0:
		status = statusOf[de.Kind]
		body = errorBody{Code: de.Code, Message: de.Message}
		for _, f := range de.Fields {
			body.Details = append(body.Details, fieldDetail{Field: f.Field, Message: f.Message})
		}
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, new(net.Error)), dbUnavailable(err):
		// Quá tải (hết giờ chờ DB) hoặc instance này không tới được DB/storage (lỗi mạng). 503 chứ không 500:
		// Nginx gặp 503 sẽ chuyển request sang instance khác và tạm né instance này; 500 thì không, còn bị
		// least_conn dồn thêm request vì lỗi trả về rất nhanh.
		status = http.StatusServiceUnavailable
		body = errorBody{Code: "SERVICE_UNAVAILABLE", Message: "service temporarily unavailable, retry later"}
	}

	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "1") // client/LB biết thử lại sau 1 giây
	}
	if status >= 500 {
		slog.ErrorContext(r.Context(), "request failed", "err", err, "method", r.Method, "path", r.URL.Path, "status", status)
	}
	body.RequestID = logger.RequestID(r.Context())
	JSON(w, status, envelope{Error: &body})
}

// dbUnavailable: Postgres đang tắt/khởi động lại (SQLSTATE 57P..) hoặc lỗi kết nối (08...). Nhận qua SQLState()
// để tầng HTTP không phụ thuộc thư viện pgx.
func dbUnavailable(err error) bool {
	var se interface{ SQLState() string }
	if !errors.As(err, &se) {
		return false
	}
	code := se.SQLState()
	return strings.HasPrefix(code, "57P") || strings.HasPrefix(code, "08")
}
