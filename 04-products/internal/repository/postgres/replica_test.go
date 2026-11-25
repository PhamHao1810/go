package postgres

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"golearn/04-products/internal/domain"
)

// Không có standby thật trong test: trỏ "replica" vào chính primary. pg_last_wal_replay_lsn() là NULL trên
// primary → router phải từ chối và đọc primary. Đọc từ standby thật được kiểm tra trên lab (README deploy).
func TestReadRouter_NotAStandbyMeansPrimary(t *testing.T) {
	repo := newTestRepo(t)
	rr := NewReadRouter(repo.pool, repo.pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rr.ok.Store(true) // giả sử lần trước standby ổn
	if err := rr.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rr.ok.Load() {
		t.Fatal("máy không ở chế độ standby mà router vẫn cho đọc")
	}
	// List qua router vẫn chạy đúng.
	repo.ReadFrom(rr)
	if _, err := repo.List(context.Background(), domain.ListFilter{Limit: 5, Sort: domain.SortNewest}); err != nil {
		t.Fatal(err)
	}
}

// Primary chết + "replica" không phải standby → vẫn đọc primary (router không bật nhầm).
func TestReadRouter_PrimaryDownNonStandbyStaysOff(t *testing.T) {
	repo := newTestRepo(t)
	dead, err := ConnectLazy(context.Background(), "postgres://x:x@127.0.0.1:1/x?connect_timeout=1", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer dead.Close()
	rr := NewReadRouter(dead, repo.pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rr.ok.Store(true)
	if err := rr.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rr.ok.Load() {
		t.Fatal("replica không phải standby mà router vẫn cho đọc")
	}
}
