package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"golearn/04-products/internal/domain"
)

// Bản giả trong bộ nhớ, mô phỏng đúng ngữ nghĩa của SQL thật (version, tồn kho).
type memRepo struct {
	mu     sync.Mutex
	items  map[int64]domain.Product
	nextID int64
	gets   int
	lists  []domain.ListFilter
}

func newMemRepo() *memRepo { return &memRepo{items: map[int64]domain.Product{}} }

func (m *memRepo) Create(_ context.Context, p domain.Product) (domain.Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	p.ID, p.Version = m.nextID, 1
	m.items[p.ID] = p
	return p, nil
}

func (m *memRepo) Get(_ context.Context, id int64) (domain.Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	p, ok := m.items[id]
	if !ok {
		return domain.Product{}, domain.ErrProductNotFound
	}
	return p, nil
}

func (m *memRepo) List(_ context.Context, f domain.ListFilter) (domain.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lists = append(m.lists, f)
	return domain.Page{}, nil
}

func (m *memRepo) Update(_ context.Context, p domain.Product) (domain.Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.items[p.ID]
	if !ok {
		return domain.Product{}, domain.ErrProductNotFound
	}
	if cur.Version != p.Version {
		return domain.Product{}, domain.ErrVersionConflict
	}
	p.Version++
	m.items[p.ID] = p
	return p, nil
}

func (m *memRepo) Delete(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[id]; !ok {
		return domain.ErrProductNotFound
	}
	delete(m.items, id)
	return nil
}

func (m *memRepo) Purchase(_ context.Context, id, qty int64) (domain.Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.items[id]
	if !ok {
		return domain.Product{}, domain.ErrProductNotFound
	}
	if p.Stock < qty {
		return domain.Product{}, domain.ErrOutOfStock
	}
	p.Stock -= qty
	p.Version++
	m.items[id] = p
	return p, nil
}

func (m *memRepo) AddImage(_ context.Context, productID, assetID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.items[productID]
	if !ok {
		return domain.ErrProductNotFound
	}
	p.Images = append(p.Images, domain.Image{AssetID: assetID, Key: "k", Position: len(p.Images)})
	m.items[productID] = p
	return nil
}

func (m *memRepo) RemoveImage(context.Context, int64, int64) error { return nil }

type memCache struct {
	mu    sync.Mutex
	m     map[int64]domain.Product
	lists map[string]domain.Page
}

func (c *memCache) Get(_ context.Context, id int64) (domain.Product, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.m[id]
	return p, ok, nil
}

func (c *memCache) Set(_ context.Context, p domain.Product) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[p.ID] = p
	return nil
}

func (c *memCache) Delete(_ context.Context, id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, id)
	return nil
}

func (c *memCache) GetList(_ context.Context, k string) (domain.Page, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.lists[k]
	return p, ok, nil
}

func (c *memCache) SetList(_ context.Context, k string, p domain.Page) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lists == nil {
		c.lists = map[string]domain.Page{}
	}
	c.lists[k] = p
	return nil
}

type fakeClaimer struct{ err error }

func (c fakeClaimer) Claim(_ context.Context, _ domain.Principal, id int64) (domain.Asset, error) {
	return domain.Asset{ID: id, Status: domain.AssetReady}, c.err
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func newTestService() (*ProductService, *memRepo) {
	repo := newMemRepo()
	return NewProductService(repo, &memCache{m: map[int64]domain.Product{}}, fakeClaimer{}, discard), repo
}

func TestAddImage_ClaimsAssetAndInvalidatesCache(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()
	p, _ := svc.Create(ctx, domain.Product{SKU: "A", Name: "Áo", Price: 1})
	_, _ = svc.Get(ctx, p.ID) // đưa vào cache

	if _, err := svc.AddImage(ctx, domain.Principal{UserID: 1}, p.ID, 9); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(ctx, p.ID); len(got.Images) != 1 {
		t.Errorf("sau khi gắn ảnh, Get trả %d ảnh, muốn 1 (cache phải bị xoá)", len(got.Images))
	}

	svc.assets = fakeClaimer{err: domain.ErrAssetNotUploaded}
	if _, err := svc.AddImage(ctx, domain.Principal{UserID: 1}, p.ID, 10); !errors.Is(err, domain.ErrAssetNotUploaded) {
		t.Errorf("file chưa upload mà vẫn gắn được: err = %v", err)
	}
	if n := len(repo.items[p.ID].Images); n != 1 {
		t.Errorf("repo có %d ảnh, muốn 1", n)
	}
}

func TestGet_CachesAndInvalidatesOnWrite(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()
	p, err := svc.Create(ctx, domain.Product{SKU: "A1", Name: "Áo thun", Price: 100, Stock: 5})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := svc.Get(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if repo.gets != 1 {
		t.Fatalf("repo.Get gọi %d lần, muốn 1 (các lần sau phải lấy từ cache)", repo.gets)
	}

	if _, err := svc.Purchase(ctx, p.ID, 2); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(ctx, p.ID); got.Stock != 3 {
		t.Errorf("sau khi mua, stock = %d, muốn 3 (cache phải bị xoá)", got.Stock)
	}
}

func TestValidationStopsBeforeRepository(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()
	var de *domain.Error

	if _, err := svc.Create(ctx, domain.Product{}); !errors.As(err, &de) || de.Code != "VALIDATION_FAILED" {
		t.Errorf("Create rỗng: err = %v", err)
	}
	if _, err := svc.Update(ctx, domain.Product{ID: 1, SKU: "A", Name: "x"}); !errors.As(err, &de) || de.Fields[0].Field != "version" {
		t.Errorf("Update thiếu version: err = %v", err)
	}
	if _, err := svc.Purchase(ctx, 1, 0); !errors.As(err, &de) || de.Fields[0].Field != "quantity" {
		t.Errorf("Purchase 0: err = %v", err)
	}
	if _, err := svc.List(ctx, domain.ListFilter{Limit: 500}); !errors.As(err, &de) {
		t.Errorf("List limit 500: err = %v", err)
	}
	if len(repo.items) != 0 || len(repo.lists) != 0 {
		t.Error("input sai vẫn chạm tới repository")
	}

	if _, err := svc.List(ctx, domain.ListFilter{Query: "áo"}); err != nil {
		t.Fatal(err)
	}
	if f := repo.lists[0]; f.Sort != domain.SortRelevance || f.Limit != domain.DefaultLimit {
		t.Errorf("filter tới repo chưa được chuẩn hoá: %+v", f)
	}
}

// 200 người cùng search 1 từ khoá → chỉ 1 truy vấn DB; bộ lọc khác (giá, trang...) không dùng nhầm kết quả.
func TestList_CachesSameFilterOnly(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = svc.List(ctx, domain.ListFilter{Query: "giày"}) }()
	}
	wg.Wait()
	if n := len(repo.lists); n != 1 {
		t.Fatalf("200 request cùng bộ lọc chạm DB %d lần, muốn 1", n)
	}
	minPrice := int64(1000)
	_, _ = svc.List(ctx, domain.ListFilter{Query: "giày", MinPrice: &minPrice})
	_, _ = svc.List(ctx, domain.ListFilter{Query: "dép"})
	if n := len(repo.lists); n != 3 {
		t.Fatalf("bộ lọc khác phải truy vấn riêng, DB bị gọi %d lần, muốn 3", n)
	}
}
