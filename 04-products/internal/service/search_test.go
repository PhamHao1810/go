package service

import (
	"context"
	"errors"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"golearn/04-products/internal/domain"
)

type fakeEngine struct {
	mu    sync.Mutex
	calls int
	err   error
	delay time.Duration
}

func (e *fakeEngine) Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	select {
	case <-time.After(e.delay):
	case <-ctx.Done():
		return domain.SearchResult{}, ctx.Err()
	}
	if e.err != nil {
		return domain.SearchResult{}, e.err
	}
	return domain.SearchResult{Hits: []domain.SearchHit{{ID: 1, Name: q.Q}}, Engine: "meilisearch"}, nil
}
func (e *fakeEngine) Upsert(context.Context, []domain.SearchDoc) error { return nil }
func (e *fakeEngine) Delete(context.Context, []int64) error            { return nil }

type memSearchCache struct {
	mu sync.Mutex
	m  map[string]domain.SearchResult
}

func (c *memSearchCache) GetSearch(_ context.Context, k string) (domain.SearchResult, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.m[k]
	return r, ok, nil
}
func (c *memSearchCache) SetSearch(_ context.Context, k string, r domain.SearchResult) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = r
	return nil
}

func newSearch(engine SearchEngine) (*SearchService, *memRepo) {
	repo := newMemRepo()
	return NewSearchService(engine, repo, &memSearchCache{m: map[string]domain.SearchResult{}}, 50*time.Millisecond, discard), repo
}

func TestSearch_UsesEngineAndCaches(t *testing.T) {
	e := &fakeEngine{}
	svc, _ := newSearch(e)
	ctx := context.Background()
	for range 3 {
		r, err := svc.Search(ctx, domain.SearchQuery{Q: "áo"})
		if err != nil || r.Engine != "meilisearch" {
			t.Fatalf("r=%+v err=%v", r, err)
		}
	}
	if e.calls != 1 {
		t.Errorf("engine gọi %d lần, muốn 1 (lần sau lấy cache)", e.calls)
	}
}

// Engine quá tải (chậm hơn timeout): báo bận, KHÔNG fallback (Postgres cùng máy sẽ càng ngợp), không mở breaker.
func TestSearch_SlowEngineIsBusyNotFallback(t *testing.T) {
	e := &fakeEngine{delay: time.Second} // quá timeout 50ms
	svc, repo := newSearch(e)
	for _, q := range []string{"áo", "quần"} {
		if _, err := svc.Search(context.Background(), domain.SearchQuery{Q: q}); !errors.Is(err, domain.ErrSearchBusy) {
			t.Errorf("q=%s: err = %v, muốn SEARCH_BUSY", q, err)
		}
	}
	if e.calls != 2 || len(repo.lists) != 0 {
		t.Errorf("engine gọi %d lần (muốn 2: không mở breaker), postgres %d lần (muốn 0)", e.calls, len(repo.lists))
	}
}

func TestSearch_FallsBackAndOpensBreaker(t *testing.T) {
	for name, e := range map[string]*fakeEngine{
		"engine chết": {err: errors.New("connection refused")},
		// Máy search chết hẳn: không ai trả lời, dial hết giờ (khác engine bận) → vẫn phải fallback.
		"máy search chết": {err: &url.Error{Op: "Post", URL: "http://10.10.0.4:7700/indexes/products/search",
			Err: &net.OpError{Op: "dial", Net: "tcp", Err: dialTimeout{}}}},
	} {
		t.Run(name, func(t *testing.T) {
			svc, repo := newSearch(e)
			ctx := context.Background()
			r, err := svc.Search(ctx, domain.SearchQuery{Q: "áo"})
			if err != nil || r.Engine != "postgres" || len(repo.lists) != 1 || repo.lists[0].Query != "áo" {
				t.Fatalf("không fallback sang Postgres: r=%+v err=%v lists=%v", r, err, repo.lists)
			}
			// Query khác (không trúng cache): breaker đang mở → không gọi engine nữa.
			if _, err := svc.Search(ctx, domain.SearchQuery{Q: "quần"}); err != nil {
				t.Fatal(err)
			}
			if e.calls != 1 {
				t.Errorf("engine gọi %d lần, muốn 1 (breaker phải bỏ qua engine)", e.calls)
			}
			// Postgres chỉ phục vụ trang 1.
			if _, err := svc.Search(ctx, domain.SearchQuery{Q: "giày", Page: 2}); !errors.Is(err, domain.ErrSearchUnavailable) {
				t.Errorf("trang 2 khi fallback: err = %v", err)
			}
		})
	}
}

type dialTimeout struct{}

func (dialTimeout) Error() string { return "i/o timeout" }
func (dialTimeout) Timeout() bool { return true }

func TestSearch_ValidatesBeforeEngine(t *testing.T) {
	e := &fakeEngine{}
	svc, _ := newSearch(e)
	var de *domain.Error
	for _, q := range []domain.SearchQuery{{Limit: 51}, {Page: 60, Limit: 20}, {Page: 1 << 62, Limit: 4}, {Sort: "name"}, {MinPrice: ptr(int64(10)), MaxPrice: ptr(int64(5))}} {
		if _, err := svc.Search(context.Background(), q); !errors.As(err, &de) || de.Code != "VALIDATION_FAILED" {
			t.Errorf("q=%+v: err = %v", q, err)
		}
	}
	if e.calls != 0 {
		t.Error("input sai vẫn gọi engine")
	}
}

func TestFoldVietnamese(t *testing.T) {
	for in, want := range map[string]string{"Điện thoại": "Dien thoai", "Áo thun nữ": "Ao thun nu", "Giày đỏ": "Giay do", "abc 123": "abc 123"} {
		if got := domain.FoldVietnamese(in); got != want {
			t.Errorf("FoldVietnamese(%q) = %q, muốn %q", in, got, want)
		}
	}
}

func ptr[T any](v T) *T { return &v }
