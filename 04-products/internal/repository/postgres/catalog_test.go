package postgres

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golearn/04-products/internal/domain"
)

// Test tích hợp cho catalog/đơn hàng/đánh giá/outbox — chạy với Postgres thật (xem newTestRepo).

type catalogFixture struct {
	products *ProductRepo
	variants *VariantRepo
	orders   *OrderRepo
	reviews  *ReviewRepo
	catalog  *CatalogRepo
	users    *UserRepo
	source   *SearchSource
}

func newCatalogFixture(t *testing.T) catalogFixture {
	repo := newTestRepo(t)
	pool := repo.pool
	return catalogFixture{products: repo, variants: NewVariantRepo(pool), orders: NewOrderRepo(pool), reviews: NewReviewRepo(pool),
		catalog: NewCatalogRepo(pool), users: NewUserRepo(pool), source: NewSearchSource(pool)}
}

func (f catalogFixture) product(t *testing.T, price, stock int64) (domain.Product, int64) {
	t.Helper()
	ctx := context.Background()
	p, err := f.products.Create(ctx, domain.Product{SKU: uniq(), Name: "Áo thun", Price: price, Stock: stock})
	if err != nil {
		t.Fatal(err)
	}
	vid, err := f.products.DefaultVariant(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p, vid
}

func TestPostgres_ConcurrentOrdersNeverOversell(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	p, vid := f.product(t, 1000, 50)
	u := newUser(t, f.users)

	var ok, outOfStock atomic.Int64
	var wg sync.WaitGroup
	for range 200 { // 200 người tranh 50 món
		wg.Go(func() {
			_, _, err := f.orders.Place(ctx, u.ID, []domain.OrderLine{{VariantID: vid, Qty: 1}})
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

	got, _ := f.products.Get(ctx, p.ID)
	if ok.Load() != 50 || outOfStock.Load() != 150 || got.Stock != 0 || got.SoldCount != 50 {
		t.Errorf("thành công=%d hết hàng=%d stock=%d sold=%d, muốn 50/150/0/50", ok.Load(), outOfStock.Load(), got.Stock, got.SoldCount)
	}
}

// Đơn nhiều món chéo nhau (A+B và B+A) chạy song song: phải không có deadlock và tổng kho của sản phẩm
// (do trigger tính) phải luôn bằng tổng kho các biến thể.
func TestPostgres_CrossOrdersNoDeadlock(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	pa, va1 := f.product(t, 100, 1000)
	pb, vb1 := f.product(t, 200, 1000)
	va2, err := f.variants.Create(ctx, domain.Variant{ProductID: pa.ID, SKU: uniq(), Color: "Đỏ", Price: 90, Stock: 1000})
	if err != nil {
		t.Fatal(err)
	}
	vb2, err := f.variants.Create(ctx, domain.Variant{ProductID: pb.ID, SKU: uniq(), Color: "Xanh", Price: 250, Stock: 1000})
	if err != nil {
		t.Fatal(err)
	}
	u := newUser(t, f.users)
	all := []int64{va1, va2.ID, vb1, vb2.ID}

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			lines := []domain.OrderLine{}
			for _, i := range rand.Perm(len(all))[:3] {
				lines = append(lines, domain.OrderLine{VariantID: all[i], Qty: 1})
			}
			if _, _, err := f.orders.Place(ctx, u.ID, lines); err != nil {
				t.Error(err) // deadlock sẽ hiện ra ở đây (SQLSTATE 40P01)
			}
		})
	}
	wg.Wait()

	for _, pid := range []int64{pa.ID, pb.ID} {
		p, _ := f.products.Get(ctx, pid)
		var sum, minPrice int64 = 0, 1 << 62
		for _, v := range p.Variants {
			sum += v.Stock
			minPrice = min(minPrice, v.Price)
		}
		if p.Stock != sum || p.Price != minPrice {
			t.Errorf("sản phẩm %d: stock=%d price=%d, tổng biến thể stock=%d min price=%d", pid, p.Stock, p.Price, sum, minPrice)
		}
		if p.Stock+p.SoldCount != 2000 {
			t.Errorf("sản phẩm %d: stock %d + đã bán %d != 2000", pid, p.Stock, p.SoldCount)
		}
	}
}

func TestPostgres_CancelRestocksOnce(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	p, vid := f.product(t, 500, 10)
	u, other := newUser(t, f.users), newUser(t, f.users)

	o, _, err := f.orders.Place(ctx, u.ID, []domain.OrderLine{{VariantID: vid, Qty: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if o.Total != 2000 || len(o.Items) != 1 || o.Items[0].UnitPrice != 500 {
		t.Fatalf("đơn sai: %+v", o)
	}
	cancel := domain.AllowedFrom(domain.OrderCancelled)
	if _, _, err := f.orders.SetStatus(ctx, o.ID, other.ID, domain.OrderCancelled, cancel); !errors.Is(err, domain.ErrOrderNotFound) {
		t.Errorf("người khác huỷ được đơn: err = %v", err)
	}
	if _, _, err := f.orders.SetStatus(ctx, o.ID, u.ID, domain.OrderCancelled, cancel); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.orders.SetStatus(ctx, o.ID, u.ID, domain.OrderCancelled, cancel); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("huỷ 2 lần: err = %v", err)
	}
	got, _ := f.products.Get(ctx, p.ID)
	if got.Stock != 10 || got.SoldCount != 0 {
		t.Errorf("sau huỷ: stock=%d sold=%d, muốn 10/0 (trả kho đúng 1 lần)", got.Stock, got.SoldCount)
	}
}

func TestPostgres_ReviewOnlyAfterDelivery(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	p, vid := f.product(t, 100, 10)
	u := newUser(t, f.users)

	if _, err := f.reviews.Create(ctx, domain.Review{ProductID: p.ID, UserID: u.ID, Rating: 5}); !errors.Is(err, domain.ErrReviewNotAllowed) {
		t.Errorf("chưa mua mà đánh giá được: err = %v", err)
	}
	o, _, err := f.orders.Place(ctx, u.ID, []domain.OrderLine{{VariantID: vid, Qty: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.reviews.Create(ctx, domain.Review{ProductID: p.ID, UserID: u.ID, Rating: 5}); !errors.Is(err, domain.ErrReviewNotAllowed) {
		t.Errorf("chưa nhận hàng mà đánh giá được: err = %v", err)
	}
	if _, _, err := f.orders.SetStatus(ctx, o.ID, 0, domain.OrderShipped, domain.AllowedFrom(domain.OrderShipped)); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("pending → shipped (bỏ qua paid) phải bị chặn: err = %v", err)
	}
	for _, s := range []domain.OrderStatus{domain.OrderPaid, domain.OrderShipped, domain.OrderDelivered} {
		if _, _, err := f.orders.SetStatus(ctx, o.ID, 0, s, domain.AllowedFrom(s)); err != nil {
			t.Fatal(s, err)
		}
	}
	if _, err := f.reviews.Create(ctx, domain.Review{ProductID: p.ID, UserID: u.ID, Rating: 4, Content: "Tốt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reviews.Create(ctx, domain.Review{ProductID: p.ID, UserID: u.ID, Rating: 1}); !errors.Is(err, domain.ErrAlreadyReviewed) {
		t.Errorf("đánh giá 2 lần: err = %v", err)
	}
	got, _ := f.products.Get(ctx, p.ID)
	page, _ := f.reviews.List(ctx, p.ID, 0, 10)
	if got.RatingCount != 1 || got.Rating() != 4 || len(page.Items) != 1 || page.Items[0].UserName != "T" {
		t.Errorf("rating=%v count=%d reviews=%+v", got.Rating(), got.RatingCount, page.Items)
	}
}

func TestPostgres_VariantsAggregateAndKeepLastOne(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	p, vid := f.product(t, 300, 5)

	v, err := f.variants.Create(ctx, domain.Variant{ProductID: p.ID, SKU: uniq(), Color: "Đen", Size: "XL", Price: 250, Stock: 7})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := f.products.Get(ctx, p.ID)
	if got.Price != 250 || got.Stock != 12 || len(got.Variants) != 2 {
		t.Errorf("sau khi thêm biến thể: price=%d stock=%d variants=%d, muốn 250/12/2", got.Price, got.Stock, len(got.Variants))
	}
	// Sản phẩm nhiều biến thể: sửa price/stock ở sản phẩm bị bỏ qua (phải sửa từng biến thể).
	got.Price, got.Stock = 1, 1
	got, err = f.products.Update(ctx, got)
	if err != nil || got.Price != 250 || got.Stock != 12 {
		t.Errorf("Update sản phẩm nhiều biến thể đổi giá/kho: price=%d stock=%d err=%v", got.Price, got.Stock, err)
	}
	if _, err := f.variants.Update(ctx, domain.Variant{ID: v.ID, ProductID: p.ID, SKU: v.SKU, Price: 1, Stock: 1, Version: 99}); !errors.Is(err, domain.ErrVariantConflict) {
		t.Errorf("sửa biến thể bằng version cũ: err = %v", err)
	}
	if err := f.variants.Delete(ctx, p.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.variants.Delete(ctx, p.ID, vid); !errors.Is(err, domain.ErrLastVariant) {
		t.Errorf("xoá biến thể cuối: err = %v", err)
	}
	got, _ = f.products.Get(ctx, p.ID)
	if got.Price != 300 || got.Stock != 5 {
		t.Errorf("sau khi xoá biến thể: price=%d stock=%d, muốn 300/5", got.Price, got.Stock)
	}
}

func TestPostgres_CategoryTreeFilterAndSearchDocs(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	tag := uniq()
	root, err := f.catalog.CreateCategory(ctx, domain.Category{Name: "Thời trang", Slug: "tt-" + strings.ToLower(tag)})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := f.catalog.CreateCategory(ctx, domain.Category{ParentID: &root.ID, Name: "Áo thun", Slug: "at-" + strings.ToLower(tag)})
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Path != root.Path+"/"+strconv.FormatInt(leaf.ID, 10) || leaf.Depth != 1 {
		t.Errorf("path=%q depth=%d", leaf.Path, leaf.Depth)
	}
	if err := f.catalog.DeleteCategory(ctx, root.ID); !errors.Is(err, domain.ErrCategoryInUse) {
		t.Errorf("xoá danh mục còn con: err = %v", err)
	}
	brand, err := f.catalog.CreateBrand(ctx, domain.Brand{Name: "Hãng " + tag, Slug: "b-" + strings.ToLower(tag)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.products.Create(ctx, domain.Product{SKU: uniq(), Name: "x", Price: 1, CategoryID: ptr(int64(1 << 60))}); !errors.Is(err, domain.ErrCategoryNotFound) {
		t.Errorf("danh mục không tồn tại: err = %v", err)
	}
	p, err := f.products.Create(ctx, domain.Product{SKU: uniq(), Name: "Áo thun " + tag, Price: 100, Stock: 3,
		CategoryID: &leaf.ID, BrandID: &brand.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.products.SetTags(ctx, p.ID, []string{"hot", "mới"}); err != nil {
		t.Fatal(err)
	}
	if err := f.products.SetAttributes(ctx, p.ID, []domain.AttributeValue{{Name: "Chất liệu", Value: "Cotton"}}); err != nil {
		t.Fatal(err)
	}

	// Lọc theo danh mục CHA phải ra sản phẩm ở danh mục con.
	page, err := f.products.List(ctx, domain.ListFilter{CategoryID: &root.ID, Sort: domain.SortNewest, Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != p.ID {
		t.Errorf("lọc theo danh mục cha: %d sản phẩm, err=%v", len(page.Items), err)
	}

	got, _ := f.products.Get(ctx, p.ID)
	if got.Brand == nil || got.Brand.ID != brand.ID || len(got.Categories) != 2 || got.Categories[0].ID != root.ID ||
		!slices.Equal(got.Tags, []string{"hot", "mới"}) || len(got.Attributes) != 1 {
		t.Errorf("chi tiết thiếu quan hệ: brand=%v cats=%v tags=%v attrs=%v", got.Brand, got.Categories, got.Tags, got.Attributes)
	}

	// Outbox phải có sản phẩm này (do trigger), document ghép đủ hãng/tag/thuộc tính.
	var seen []int64
	for {
		n, err := f.source.DrainOutbox(ctx, 10_000, func(_ context.Context, ids []int64) error {
			seen = append(seen, ids...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	if !slices.Contains(seen, p.ID) {
		t.Fatal("outbox không có sản phẩm vừa tạo")
	}
	docs, err := f.source.Docs(ctx, []int64{p.ID})
	if err != nil || len(docs) != 1 {
		t.Fatalf("docs=%v err=%v", docs, err)
	}
	d := docs[0]
	if d.Brand != brand.Name || !slices.Equal(d.Tags, []string{"hot", "mới"}) || !slices.Equal(d.Attributes, []string{"Chất liệu: Cotton"}) ||
		d.NameASCII != "Ao thun "+tag || !d.InStock || d.CategoryID == nil || *d.CategoryID != leaf.ID {
		t.Errorf("document sai: %+v", d)
	}
}

func ptr[T any](v T) *T { return &v }

// Duyệt danh mục cha bằng cách trộn N dòng đầu của từng danh mục con: đi hết các trang phải ra đủ, không trùng,
// đúng thứ tự — cho cả sắp mới nhất và sắp theo giá, có lọc kèm.
func TestPostgres_CategoryBrowsePaginatesAcrossSubtree(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	tag := strings.ToLower(uniq())
	root, err := f.catalog.CreateCategory(ctx, domain.Category{Name: "Gốc", Slug: "g-" + tag})
	if err != nil {
		t.Fatal(err)
	}
	var leaves []int64
	for i := range 3 {
		c, err := f.catalog.CreateCategory(ctx, domain.Category{ParentID: &root.ID, Name: "Lá", Slug: "l" + strconv.Itoa(i) + "-" + tag})
		if err != nil {
			t.Fatal(err)
		}
		leaves = append(leaves, c.ID)
	}
	for i := range 40 {
		cat := leaves[i%3]
		if i%7 == 0 {
			cat = root.ID // có cả sản phẩm gắn thẳng vào danh mục cha
		}
		if _, err := f.products.Create(ctx, domain.Product{SKU: uniq(), Name: "x", Price: int64(100 + i%9), Stock: int64(i % 4), CategoryID: &cat}); err != nil {
			t.Fatal(err)
		}
	}
	minPrice := int64(102)
	for _, flt := range []domain.ListFilter{
		{Sort: domain.SortNewest},
		{Sort: domain.SortPriceAsc},
		{Sort: domain.SortPriceDesc, InStock: true, MinPrice: &minPrice},
	} {
		flt.CategoryID, flt.Limit = &root.ID, 7
		var got []domain.Product
		for range 20 {
			page, err := f.products.List(ctx, flt)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, page.Items...)
			if page.NextCursor == "" {
				break
			}
			flt.Cursor = page.NextCursor
		}
		want := 0
		for i := range 40 {
			if (!flt.InStock || i%4 > 0) && (flt.MinPrice == nil || int64(100+i%9) >= *flt.MinPrice) {
				want++
			}
		}
		seen := map[int64]bool{}
		for i, p := range got {
			if seen[p.ID] {
				t.Fatalf("%s: trùng sản phẩm %d", flt.Sort, p.ID)
			}
			seen[p.ID] = true
			if i > 0 {
				a, b := got[i-1], p
				ok := true
				switch flt.Sort {
				case domain.SortNewest:
					ok = a.CreatedAt.After(b.CreatedAt) || (a.CreatedAt.Equal(b.CreatedAt) && a.ID > b.ID)
				case domain.SortPriceAsc:
					ok = a.Price < b.Price || (a.Price == b.Price && a.ID < b.ID)
				case domain.SortPriceDesc:
					ok = a.Price > b.Price || (a.Price == b.Price && a.ID > b.ID)
				}
				if !ok {
					t.Fatalf("%s: sai thứ tự ở vị trí %d", flt.Sort, i)
				}
			}
		}
		if len(got) != want {
			t.Errorf("%s: %d sản phẩm, muốn %d", flt.Sort, len(got), want)
		}
	}
}

// Đơn hàng chỉ đổi kho/đã bán: không được đẩy search ngay (mỗi đơn 1 task Meilisearch → search timeout khi tải cao).
// Hết hàng (in_stock đổi) thì phải đẩy; "đã bán" đi đường FlushSold theo lịch.
func TestPostgres_OrdersOnlyOutboxWhenIndexedFieldsChange(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	p, vid := f.product(t, 1000, 2)
	u := newUser(t, f.users)
	drain := func() []int64 {
		var seen []int64
		for {
			n, err := f.source.DrainOutbox(ctx, 10_000, func(_ context.Context, ids []int64) error {
				seen = append(seen, ids...)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				return seen
			}
		}
	}
	order := func() {
		if _, _, err := f.orders.Place(ctx, u.ID, []domain.OrderLine{{VariantID: vid, Qty: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	drain()

	order() // kho 2 → 1: vẫn còn hàng
	if slices.Contains(drain(), p.ID) {
		t.Error("đơn hàng không làm đổi trường nào trong index mà vẫn ghi outbox")
	}
	if _, err := f.source.FlushSold(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(drain(), p.ID) {
		t.Error("FlushSold phải đẩy sản phẩm vừa đổi số đã bán")
	}

	order() // kho 1 → 0: in_stock đổi
	if !slices.Contains(drain(), p.ID) {
		t.Error("hết hàng phải ghi outbox để search lọc 'còn hàng' đúng")
	}
}
