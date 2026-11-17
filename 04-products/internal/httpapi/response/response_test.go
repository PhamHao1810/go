package response

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"golearn/04-products/internal/domain"
)

// Không tới được DB/storage → 503 để Nginx chuyển request sang instance khác; bug thật vẫn 500.
func TestErrorStatus(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"lỗi mạng tới DB (bị bọc như pgx)", fmt.Errorf("failed to connect: %w", dial), http.StatusServiceUnavailable},
		{"hết giờ chờ", fmt.Errorf("acquire: %w", context.DeadlineExceeded), http.StatusServiceUnavailable},
		{"lỗi nghiệp vụ", domain.ErrOutOfStock, http.StatusConflict},
		{"DB đang tắt (57P01)", fmt.Errorf("query: %w", sqlErr("57P01")), http.StatusServiceUnavailable},
		{"lỗi SQL thường (trùng khoá)", sqlErr("23505"), http.StatusInternalServerError},
		{"bug", errors.New("nil map"), http.StatusInternalServerError},
	} {
		w := httptest.NewRecorder()
		Error(w, httptest.NewRequest("GET", "/", nil), tc.err)
		if w.Code != tc.want {
			t.Errorf("%s: status %d, muốn %d", tc.name, w.Code, tc.want)
		}
	}
}

// sqlErr giả lập lỗi có SQLSTATE như *pgconn.PgError.
type sqlErr string

func (e sqlErr) Error() string    { return "sql " + string(e) }
func (e sqlErr) SQLState() string { return string(e) }
