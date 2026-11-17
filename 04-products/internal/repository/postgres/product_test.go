package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golearn/04-products/internal/domain"
	"golearn/04-products/migrations"
)

// Integration test với Postgres thật: nơi chứng minh chống bán âm kho, chống ghi đè, phân trang đúng.
// Cần: docker compose -f 04-products/docker-compose.yml up -d
//
//	$env:TEST_DATABASE_URL="postgres://app:app@localhost:5433/products?sslmode=disable"; go test ./04-products/... -count=1
func newTestRepo(t *testing.T) *ProductRepo {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL chưa đặt")
	}
	ctx := context.Background()
	pool, err := Connect(ctx, url, 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatal(err)
	}
	return NewProductRepo(pool)
}

func uniq() string { return fmt.Sprintf("T%d", time.Now().UnixNano()) }

func TestPostgres_ConcurrentPurchaseNeverOversells(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	p, err := repo.Create(ctx, domain.Product{SKU: uniq(), Name: "Hàng hot", Price: 1, Stock: 50})
	if err != nil {
		t.Fatal(err)
	}

	var ok, outOfStock atomic.Int64
	var wg sync.WaitGroup
	for range 200 { // 200 người tranh 50 món
		wg.Go(func() {
			_, err := repo.Purchase(ctx, p.ID, 1)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, domain.ErrOutOfStock):
				outOfStock.Add(1)
			default:
				t.Error(err)
			}
		})
	}
	wg.Wait()

	got, _ := repo.Get(ctx, p.ID)
	if ok.Load() != 50 || outOfStock.Load() != 150 || got.Stock != 0 {
		t.Errorf("thành công=%d hết hàng=%d stock=%d, muốn 50/150/0", ok.Load(), outOfStock.Load(), got.Stock)
	}
}

func TestPostgres_ConcurrentUpdateOnlyOneWins(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	p, err := repo.Create(ctx, domain.Product{SKU: uniq(), Name: "Gốc", Price: 1, Stock: 1})
	if err != nil {
		t.Fatal(err)
	}

	var ok, conflict atomic.Int64
	var wg sync.WaitGroup
	for i := range 20 { // 20 admin cùng sửa từ version 1
		wg.Go(func() {
			u := p
			u.Name = fmt.Sprintf("Sửa bởi %d", i)
			_, err := repo.Update(ctx, u)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, domain.ErrVersionConflict):
				conflict.Add(1)
			default:
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 1 || conflict.Load() != 19 {
		t.Errorf("thành công=%d conflict=%d, muốn 1/19", ok.Load(), conflict.Load())
	}
	if _, err := repo.Update(ctx, domain.Product{ID: -1, SKU: "x", Name: "x", Version: 1}); !errors.Is(err, domain.ErrProductNotFound) {
		t.Errorf("update id không tồn tại: err = %v", err)
	}
	if _, err := repo.Create(ctx, domain.Product{SKU: p.SKU, Name: "trùng", Price: 1}); !errors.Is(err, domain.ErrSKUTaken) {
		t.Errorf("trùng SKU: err = %v", err)
	}
}

// Đi hết các trang với mọi kiểu sort + filter: không trùng, không sót, đúng thứ tự.
func TestPostgres_ListFiltersAndPaginates(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	tag := uniq() // từ duy nhất để chỉ khớp dữ liệu của test này
	prices := []int64{500, 100, 300, 100, 900, 700, 300}
	for i, price := range prices {
		stock := int64(i % 2) // một nửa hết hàng
		if _, err := repo.Create(ctx, domain.Product{SKU: fmt.Sprintf("%s-%d", tag, i), Name: "Giày " + tag, Price: price, Stock: stock}); err != nil {
			t.Fatal(err)
		}
	}

	walk := func(f domain.ListFilter) []domain.Product {
		f.Query, f.Limit = tag, 2
		var all []domain.Product
		for range 20 {
			page, err := repo.List(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, page.Items...)
			if page.NextCursor == "" {
				return all
			}
			f.Cursor = page.NextCursor
		}
		t.Fatal("phân trang không dừng")
		return nil
	}

	asc := walk(domain.ListFilter{Sort: domain.SortPriceAsc})
	if len(asc) != len(prices) {
		t.Fatalf("price asc: %d dòng, muốn %d", len(asc), len(prices))
	}
	seen := map[int64]bool{}
	for i, p := range asc {
		if seen[p.ID] {
			t.Errorf("trùng id %d giữa các trang", p.ID)
		}
		seen[p.ID] = true
		if i > 0 && p.Price < asc[i-1].Price {
			t.Errorf("sai thứ tự giá: %d sau %d", p.Price, asc[i-1].Price)
		}
	}

	if got := walk(domain.ListFilter{Sort: domain.SortNewest}); len(got) != len(prices) || got[0].CreatedAt.Before(got[len(got)-1].CreatedAt) {
		t.Errorf("newest: %d dòng hoặc sai thứ tự thời gian", len(got))
	}
	if got := walk(domain.ListFilter{Sort: domain.SortRelevance}); len(got) != len(prices) {
		t.Errorf("relevance: %d dòng, muốn %d", len(got), len(prices))
	}

	min, max := int64(300), int64(700)
	got := walk(domain.ListFilter{Sort: domain.SortPriceDesc, MinPrice: &min, MaxPrice: &max, InStock: true})
	for _, p := range got {
		if p.Price < min || p.Price > max || p.Stock == 0 {
			t.Errorf("lọt filter: price=%d stock=%d", p.Price, p.Stock)
		}
	}
	var want int
	for i, price := range prices {
		if price >= min && price <= max && i%2 == 1 { // i lẻ = còn hàng
			want++
		}
	}
	if len(got) != want {
		t.Errorf("filter giá + còn hàng: %d dòng, muốn %d", len(got), want)
	}
}
