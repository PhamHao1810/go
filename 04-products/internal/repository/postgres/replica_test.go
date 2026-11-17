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
