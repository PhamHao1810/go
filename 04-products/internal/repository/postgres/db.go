// Package postgres cài đặt repository bằng PostgreSQL (pgx).
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect tạo connection pool. Tổng maxConns của mọi instance phải < max_connections của Postgres.
func Connect(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	pool, err := ConnectLazy(ctx, url, maxConns)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return pool, nil
}

// ConnectLazy như Connect nhưng không bắt kết nối được ngay (dùng cho standby: standby đang chết không được
// làm API khởi động thất bại, ReadRouter tự đọc primary).
func ConnectLazy(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres config: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = max(maxConns/4, 1) // giữ sẵn connection ấm, tránh trễ khi tải tăng đột ngột
	cfg.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	return pool, nil
}
