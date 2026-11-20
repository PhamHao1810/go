// Điểm khởi động: chỉ đọc config, tạo dependency, ghép các tầng và chạy server (composition root).
// Không chứa logic nghiệp vụ.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"golearn/04-products/internal/config"
	"golearn/04-products/internal/httpapi"
	"golearn/04-products/internal/logger"
	"golearn/04-products/internal/repository/meili"
	"golearn/04-products/internal/repository/postgres"
	"golearn/04-products/internal/repository/rediscache"
	"golearn/04-products/internal/repository/s3store"
	"golearn/04-products/internal/service"
	"golearn/04-products/internal/worker"
	"golearn/04-products/migrations"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" { // HEALTHCHECK của Docker: image distroless không có curl
		os.Exit(healthcheck())
	}
	if len(os.Args) > 1 && os.Args[1] == "reindex" { // dựng lại toàn bộ index Meilisearch: docker compose exec api1 /api reindex
		if err := reindex(); err != nil {
			slog.Error("reindex failed", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func healthcheck() int {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, _ := net.SplitHostPort(addr)
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(cfg.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ----- Hạ tầng -----
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.MigrateOnStart {
		if err := postgres.Migrate(ctx, pool, migrations.FS); err != nil {
			return err
		}
	}
	rdb, err := rediscache.Connect(ctx, cfg.RedisAddr, cfg.RedisPassword)
	if err != nil {
		return err
	}
	defer rdb.Close()
	store, err := s3store.New(s3store.Config(cfg.Storage))
	if err != nil {
		return err
	}
	tx := postgres.NewTxManager(pool)

	// ----- Service (business logic) -----
	assets := service.NewAssetService(postgres.NewAssetRepo(pool), store, tx, log)
	productRepo := postgres.NewProductRepo(pool)
	jobs := []worker.Job{ // OncePer: cả cụm chỉ 1 instance chạy mỗi chu kỳ
		{Name: "asset-gc", Interval: 10 * time.Minute, Run: rediscache.OncePer(rdb, "asset-gc", 10*time.Minute, assets.CollectGarbage)},
	}
	if cfg.DatabaseReadURL != "" {
		replica, err := postgres.ConnectLazy(ctx, cfg.DatabaseReadURL, cfg.DBMaxConns)
		if err != nil {
			return err
		}
		defer replica.Close()
		rr := postgres.NewReadRouter(pool, replica, log)
		productRepo.ReadFrom(rr)
		jobs = append(jobs, worker.Job{Name: "replica-check", Interval: 5 * time.Second, Run: rr.Check}) // mỗi instance tự kiểm tra
	}
	cache := rediscache.NewProductCache(rdb, cfg.CacheTTL)
	catalog := service.NewCatalogService(postgres.NewCatalogRepo(pool), log)
	orders := service.NewOrderService(postgres.NewOrderRepo(pool), cache, log)
	products := service.NewProductService(productRepo, cache, assets, orders, log)
	variants := service.NewVariantService(postgres.NewVariantRepo(pool), cache, log)
	reviews := service.NewReviewService(postgres.NewReviewRepo(pool), cache, log)

	// Search: Meilisearch nếu có cấu hình, lỗi/chậm thì SearchService tự dùng Postgres.
	var engine service.SearchEngine
	if cfg.MeiliURL != "" {
		mc := meili.New(cfg.MeiliURL, cfg.MeiliKey)
		engine = mc
		source := postgres.NewSearchSource(pool)
		sync := service.NewSearchSync(source, mc, catalog, log)
		var ready atomic.Bool
		// "Đã bán" đổi theo từng đơn: đẩy theo lịch thay vì mỗi đơn 1 task Meilisearch (migrations/006).
		// Sắp xếp "bán chạy" trên search trễ tối đa ~10 phút; trang chi tiết luôn đúng.
		jobs = append(jobs, worker.Job{Name: "search-sold-sync", Interval: 10 * time.Minute, Run: func(ctx context.Context) error {
			_, err := source.FlushSold(ctx)
			return err
		}})
		jobs = append(jobs, worker.Job{Name: "search-sync", Interval: time.Second, Run: func(ctx context.Context) error {
			// Tạo index + settings trước lần đồng bộ đầu tiên: Meilisearch tự tạo index khi nhận document nhưng
			// KHÔNG có settings (không lọc/sắp được). Meilisearch chưa lên → lỗi, giây sau thử lại.
			if !ready.Load() {
				if err := mc.Setup(ctx); err != nil {
					return err
				}
				ready.Store(true)
			}
			return sync.Run(ctx)
		}})
	}
	search := service.NewSearchService(engine, productRepo, cache, cfg.SearchTimeout, log)
	authSvc, err := service.NewAuthService(
		postgres.NewUserRepo(pool), postgres.NewRefreshTokenRepo(pool),
		rediscache.NewLoginLimiter(rdb, 10, 15*time.Minute), // 10 lần sai / 15 phút / email
		tx, assets,
		service.AuthConfig{Secret: cfg.JWTSecret, AccessTTL: cfg.AccessTokenTTL, RefreshTTL: cfg.RefreshTokenTTL, BcryptCost: cfg.BcryptCost},
		log,
	)
	if err != nil {
		return err
	}
	if cfg.AdminEmail != "" {
		if err := authSvc.EnsureAdmin(ctx, cfg.AdminEmail, cfg.AdminPassword); err != nil {
			return err
		}
	}

	jobs = append(jobs, worker.Job{Name: "token-purge", Interval: time.Hour,
		Run: rediscache.OncePer(rdb, "token-purge", time.Hour, authSvc.PurgeExpiredTokens)})
	worker.Start(ctx, log, jobs...)

	// ----- HTTP -----
	var draining atomic.Bool
	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Log:            log,
			Products:       products,
			Catalog:        catalog,
			Variants:       variants,
			Orders:         orders,
			Reviews:        reviews,
			Search:         search,
			Auth:           authSvc,
			Assets:         assets,
			AssetURL:       store.URL,
			Ready:          pool.Ping,
			Draining:       &draining,
			RequestTimeout: cfg.RequestTimeout,
			CORSOrigins:    cfg.CORSOrigins,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       90 * time.Second, // > keepalive của Nginx/LB để phía LB đóng trước, tránh lỗi 502
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// Graceful shutdown: /healthz trả 503 để LB ngừng gửi request mới, đợi LB nhận ra,
	// rồi ngừng nhận kết nối và chờ request đang chạy xong (tối đa 10s).
	draining.Store(true)
	log.Info("draining", "delay", cfg.ShutdownDelay.String())
	time.Sleep(cfg.ShutdownDelay)

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// reindex đổ toàn bộ sản phẩm từ Postgres sang Meilisearch (index phụ rồi swap, search không gián đoạn).
func reindex() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.MeiliURL == "" {
		return errors.New("MEILI_URL is not set")
	}
	log := logger.New(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, 4)
	if err != nil {
		return err
	}
	defer pool.Close()
	mc := meili.New(cfg.MeiliURL, cfg.MeiliKey)
	if err := mc.Setup(ctx); err != nil {
		return err
	}
	catalog := service.NewCatalogService(postgres.NewCatalogRepo(pool), log)
	start := time.Now()
	if err := service.NewSearchSync(postgres.NewSearchSource(pool), mc, catalog, log).Reindex(ctx, mc); err != nil {
		return err
	}
	n, err := mc.Stats(ctx)
	log.Info("reindex done", "documents", n, "took", time.Since(start).Round(time.Second).String())
	return err
}
