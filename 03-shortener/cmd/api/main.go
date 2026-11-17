// Điểm khởi động API. File này làm việc của AppModule + main.ts trong NestJS:
// đọc config, kết nối DB/Redis, ghép các dependency, chạy server, tắt êm (graceful shutdown).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"golearn/03-shortener/internal/config"
	"golearn/03-shortener/internal/httpx"
	"golearn/03-shortener/internal/link"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load()

	// ctx bị huỷ khi nhận Ctrl+C hoặc SIGTERM (Kubernetes gửi SIGTERM khi tắt pod).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Connection pool. Bài tập giai đoạn 4: chỉnh MaxConns, MaxConnLifetime... qua pgxpool.ParseConfig.
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}

	// Ghép dependency bằng tay: đây chính là "DI container" của Go.
	svc := link.NewService(link.NewPostgresRepo(pool), link.NewRedisCache(rdb, cfg.CacheTTL), log)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	link.NewHandler(svc, cfg.BaseURL, log).Register(mux)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: httpx.Chain(mux, httpx.Recover(log), httpx.Logger(log)),
		// Luôn đặt timeout: server mặc định KHÔNG có timeout, dễ bị cạn kết nối khi tải cao.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
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

	// Graceful shutdown: ngừng nhận request mới, chờ request đang chạy xong (tối đa 10s).
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
