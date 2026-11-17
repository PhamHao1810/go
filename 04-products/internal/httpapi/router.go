// Package httpapi là tầng giao tiếp HTTP (transport): route, DTO, handler, phân quyền theo route.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"golearn/04-products/internal/domain"
	"golearn/04-products/internal/httpapi/middleware"
	"golearn/04-products/internal/httpapi/response"
)

type Deps struct {
	Log            *slog.Logger
	Products       ProductService
	Auth           AuthService
	Assets         AssetService
	AssetURL       URLFunc                     // key trong storage → URL công khai
	Ready          func(context.Context) error // kiểm tra phụ thuộc bắt buộc (DB) cho /readyz
	Draining       *atomic.Bool                // true khi đang tắt: /healthz trả 503 để LB ngừng gửi request
	RequestTimeout time.Duration
	CORSOrigins    []string
}

var errRouteNotFound = &domain.Error{Kind: domain.KindNotFound, Code: "ROUTE_NOT_FOUND", Message: "route not found"}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	// Liveness + dùng cho load balancer: process còn sống và không đang tắt.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if d.Draining.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	// Readiness: DB có trả lời không (Kubernetes dùng để ngừng gửi traffic khi DB mất kết nối).
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := d.Ready(ctx); err != nil {
			d.Log.WarnContext(ctx, "not ready", "err", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// Mỗi resource 1 handler, tự khai báo route + quyền. Thêm bảng/tính năng mới = thêm 1 dòng ở đây.
	g := Guards{
		User:  middleware.Authenticate(d.Auth),
		Admin: middleware.Authenticate(d.Auth, domain.RoleAdmin),
	}
	NewAuthHandler(d.Auth, d.AssetURL).Register(mux, g)
	NewProductHandler(d.Products, d.AssetURL).Register(mux, g)
	NewAssetHandler(d.Assets).Register(mux, g)

	// Route không tồn tại cũng trả JSON đúng format thay vì text "404 page not found" của Go.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { response.Error(w, r, errRouteNotFound) })

	return middleware.Chain(mux,
		middleware.RequestID(),                   // ngoài cùng: mọi thứ phía trong đều có request_id
		middleware.AccessLog(d.Log, time.Second), // thấy được cả status 500 do Recover trả
		middleware.Recover(d.Log),
		middleware.CORS(d.CORSOrigins),
		middleware.Timeout(d.RequestTimeout),
	)
}
