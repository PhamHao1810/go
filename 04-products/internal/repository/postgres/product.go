package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"golearn/04-products/internal/domain"
)

const cols = `id, sku, name, description, price, stock, version, category_id, brand_id, rating_sum, rating_count,
	sold_count, created_at, updated_at`

type ProductRepo struct {
	pool    *pgxpool.Pool
	read    *ReadRouter  // nil = mọi truy vấn đọc primary
	standby *ProductRepo // cùng repo nhưng đọc standby: dùng cho Get khi primary không trả lời
}

func NewProductRepo(pool *pgxpool.Pool) *ProductRepo {
	return &ProductRepo{pool: pool}
}

// ReadFrom cho List (search, danh sách) đọc từ standby khi nó theo kịp primary.
func (r *ProductRepo) ReadFrom(rr *ReadRouter) *ProductRepo {
	r.read = rr
	r.standby = &ProductRepo{pool: rr.replica}
	return r
}

// Create tạo sản phẩm kèm 1 biến thể mặc định (cùng sku/giá/kho) trong 1 câu lệnh.
func (r *ProductRepo) Create(ctx context.Context, p domain.Product) (domain.Product, error) {
	return r.one(ctx, `WITH p AS (
			INSERT INTO products (sku, name, description, price, stock, category_id, brand_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+cols+`),
		v AS (INSERT INTO product_variants (product_id, sku, price, stock) SELECT id, sku, price, stock FROM p)
		SELECT `+cols+` FROM p`,
		p.SKU, p.Name, p.Description, p.Price, p.Stock, p.CategoryID, p.BrandID)
}

// Get: chi tiết đầy đủ (ảnh, hãng, danh mục, biến thể, thuộc tính, tag).
func (r *ProductRepo) Get(ctx context.Context, id int64) (domain.Product, error) {
	if r.standby != nil && r.read.PrimaryDown() {
		return r.standby.Get(ctx, id)
	}
	p, err := r.oneWithImages(ctx, `SELECT `+cols+` FROM products WHERE id = $1`, id)
	if err != nil {
		return p, err
	}
	return p, r.loadDetails(ctx, db(ctx, r.pool), &p)
}

func (r *ProductRepo) List(ctx context.Context, f domain.ListFilter) (domain.Page, error) {
	q, err := buildListQuery(f)
	if err != nil {
		return domain.Page{}, err
	}
	if q.empty {
		return domain.Page{Items: []domain.Product{}}, nil
	}
	if r.read == nil {
		return r.list(ctx, db(ctx, r.pool), q, f)
	}
	read := r.read.Pool()
	page, err := r.list(ctx, read, q, f)
	// Standby vừa chết/đang tắt/bị huỷ truy vấn do xung đột replication → đọc lại primary, khách không thấy lỗi.
	if err != nil && read != r.pool && (unavailable(err) || pgCode(err) == "40001") {
		r.read.MarkBehind()
		return r.list(ctx, r.pool, q, f)
	}
	return page, err
}

func (r *ProductRepo) list(ctx context.Context, conn querier, q listQuery, f domain.ListFilter) (domain.Page, error) {
	items, err := queryAll[domain.Product](ctx, conn, q.sql, q.args...)
	if err != nil {
		return domain.Page{}, err
	}
	// Lấy dư 1 dòng để biết còn trang sau hay không mà không cần COUNT(*).
	page := domain.Page{Items: items}
	if len(items) > f.Limit {
		page.Items = items[:f.Limit]
		page.NextCursor = q.next(page.Items[len(page.Items)-1])
	}
	return page, r.loadImages(ctx, conn, page.Items)
}

// Update: optimistic locking — chỉ ghi khi version trong DB còn bằng version client gửi lên.
// 2 admin cùng sửa → người sau nhận 409 thay vì âm thầm ghi đè người trước (lost update).
// price/stock chỉ áp dụng khi sản phẩm có đúng 1 biến thể (ghi vào biến thể đó); nhiều biến thể thì sửa từng biến thể.
func (r *ProductRepo) Update(ctx context.Context, p domain.Product) (domain.Product, error) {
	err := inTx(ctx, r.pool, func(q querier) error {
		// Khoá biến thể TRƯỚC sản phẩm: cùng thứ tự với đặt hàng (biến thể → sản phẩm) nên không deadlock.
		n, err := lockVariants(ctx, q, p.ID)
		if err != nil {
			return err
		}
		tag, err := q.Exec(ctx, `UPDATE products SET sku = $2, name = $3, description = $4, category_id = $8, brand_id = $9,
			price = CASE WHEN $10::int = 1 THEN $5 ELSE price END, stock = CASE WHEN $10::int = 1 THEN $6 ELSE stock END,
			version = version + 1, updated_at = now()
			WHERE id = $1 AND version = $7`,
			p.ID, p.SKU, p.Name, p.Description, p.Price, p.Stock, p.Version, p.CategoryID, p.BrandID, n)
		if err != nil {
			return productWriteErr(err)
		}
		if tag.RowsAffected() == 0 {
			return existsIn(ctx, q, p.ID, domain.ErrVersionConflict)
		}
		if n == 1 { // trigger thấy tổng đã khớp nên không đổi version sản phẩm thêm lần nữa
			_, err = q.Exec(ctx, `UPDATE product_variants SET price = $2, stock = $3, version = version + 1, updated_at = now()
				WHERE product_id = $1`, p.ID, p.Price, p.Stock)
		}
		return err
	})
	if err != nil {
		return domain.Product{}, err
	}
	return r.Get(ctx, p.ID)
}

func (r *ProductRepo) Delete(ctx context.Context, id int64) error {
	return inTx(ctx, r.pool, func(q querier) error {
		if _, err := lockVariants(ctx, q, id); err != nil {
			return err
		}
		tag, err := q.Exec(ctx, `DELETE FROM products WHERE id = $1`, id)
		if err == nil && tag.RowsAffected() == 0 {
			return domain.ErrProductNotFound
		}
		return err
	})
}

// lockVariants khoá mọi biến thể của sản phẩm theo id tăng dần, trả số biến thể.
func lockVariants(ctx context.Context, q querier, productID int64) (int, error) {
	rows, _ := q.Query(ctx, `SELECT id FROM product_variants WHERE product_id = $1 ORDER BY id FOR UPDATE`, productID)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	return len(ids), err
}

// DefaultVariant: biến thể duy nhất của sản phẩm (cho API mua nhanh cũ). Nhiều biến thể → phải chọn qua /orders.
func (r *ProductRepo) DefaultVariant(ctx context.Context, productID int64) (int64, error) {
	rows, _ := db(ctx, r.pool).Query(ctx, `SELECT id FROM product_variants WHERE product_id = $1 ORDER BY id LIMIT 2`, productID)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	switch {
	case err != nil:
		return 0, err
	case len(ids) == 0:
		return 0, domain.ErrProductNotFound
	case len(ids) > 1:
		return 0, domain.ErrProductHasVariants
	}
	return ids[0], nil
}

// SetTags thay toàn bộ tag của sản phẩm (tag chưa có thì tạo).
func (r *ProductRepo) SetTags(ctx context.Context, productID int64, tags []string) error {
	return inTx(ctx, r.pool, func(q querier) error {
		if err := existsIn(ctx, q, productID, nil); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `INSERT INTO tags (name) SELECT unnest($1::text[]) ON CONFLICT (name) DO NOTHING`, tags); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM product_tags WHERE product_id = $1
			AND tag_id NOT IN (SELECT id FROM tags WHERE name = ANY($2))`, productID, tags); err != nil {
			return err
		}
		_, err := q.Exec(ctx, `INSERT INTO product_tags (product_id, tag_id) SELECT $1, id FROM tags WHERE name = ANY($2)
			ON CONFLICT DO NOTHING`, productID, tags)
		return err
	})
}

// SetAttributes thay toàn bộ thuộc tính của sản phẩm (tên thuộc tính/giá trị chưa có thì tạo).
func (r *ProductRepo) SetAttributes(ctx context.Context, productID int64, attrs []domain.AttributeValue) error {
	names := make([]string, len(attrs))
	values := make([]string, len(attrs))
	for i, a := range attrs {
		names[i], values[i] = a.Name, a.Value
	}
	return inTx(ctx, r.pool, func(q querier) error {
		if err := existsIn(ctx, q, productID, nil); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `INSERT INTO attributes (name) SELECT DISTINCT unnest($1::text[]) ON CONFLICT (name) DO NOTHING`, names); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `INSERT INTO attribute_values (attribute_id, value)
			SELECT a.id, x.value FROM unnest($1::text[], $2::text[]) x(name, value) JOIN attributes a ON a.name = x.name
			ON CONFLICT (attribute_id, value) DO NOTHING`, names, values); err != nil {
			return err
		}
		const want = `SELECT av.id FROM unnest($2::text[], $3::text[]) x(name, value)
			JOIN attributes a ON a.name = x.name JOIN attribute_values av ON av.attribute_id = a.id AND av.value = x.value`
		if _, err := q.Exec(ctx, `DELETE FROM product_attribute_values WHERE product_id = $1 AND value_id NOT IN (`+want+`)`,
			productID, names, values); err != nil {
			return err
		}
		_, err := q.Exec(ctx, `INSERT INTO product_attribute_values (product_id, value_id) SELECT $1, id FROM (`+want+`) w
			ON CONFLICT DO NOTHING`, productID, names, values)
		return err
	})
}

// existsIn: sản phẩm không tồn tại → ErrProductNotFound; tồn tại → errIfExists (nil = không lỗi).
func existsIn(ctx context.Context, q querier, id int64, errIfExists error) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM products WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return domain.ErrProductNotFound
	}
	return errIfExists
}

type attrRow struct {
	Name  string
	Value string
}

// loadDetails: hãng, chuỗi danh mục, biến thể, thuộc tính, tag của 1 sản phẩm (chỉ dùng cho trang chi tiết).
func (r *ProductRepo) loadDetails(ctx context.Context, q querier, p *domain.Product) error {
	if p.BrandID != nil {
		b, err := queryOne[domain.Brand](ctx, q, `SELECT id, name, slug, created_at FROM brands WHERE id = $1`, *p.BrandID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			p.Brand = &b
		}
	}
	p.Categories = []domain.Category{}
	if p.CategoryID != nil {
		cats, err := queryAll[domain.Category](ctx, q, `SELECT a.id, a.parent_id, a.name, a.slug, a.path, a.depth, a.created_at
			FROM categories c JOIN categories a ON a.id = ANY(string_to_array(c.path, '/')::bigint[])
			WHERE c.id = $1 ORDER BY a.depth`, *p.CategoryID)
		if err != nil {
			return err
		}
		p.Categories = cats
	}
	variants, err := queryAll[domain.Variant](ctx, q, `SELECT `+variantCols+` FROM product_variants WHERE product_id = $1 ORDER BY id`, p.ID)
	if err != nil {
		return err
	}
	p.Variants = variants
	attrs, err := queryAll[attrRow](ctx, q, `SELECT a.name, av.value FROM product_attribute_values pav
		JOIN attribute_values av ON av.id = pav.value_id JOIN attributes a ON a.id = av.attribute_id
		WHERE pav.product_id = $1 ORDER BY a.name, av.value`, p.ID)
	if err != nil {
		return err
	}
	p.Attributes = make([]domain.AttributeValue, len(attrs))
	for i, a := range attrs {
		p.Attributes[i] = domain.AttributeValue{Name: a.Name, Value: a.Value}
	}
	rows, _ := q.Query(ctx, `SELECT t.name FROM product_tags pt JOIN tags t ON t.id = pt.tag_id
		WHERE pt.product_id = $1 ORDER BY t.name`, p.ID)
	p.Tags, err = pgx.CollectRows(rows, pgx.RowTo[string])
	return err
}

// productWriteErr: lỗi ràng buộc khi ghi products → lỗi nghiệp vụ rõ ràng.
func productWriteErr(err error) error {
	switch {
	case pgCode(err) == uniqueViolation:
		return domain.ErrSKUTaken
	case pgCode(err) == foreignKeyViolation && strings.Contains(pgConstraint(err), "category"):
		return domain.ErrCategoryNotFound
	case pgCode(err) == foreignKeyViolation && strings.Contains(pgConstraint(err), "brand"):
		return domain.ErrBrandNotFound
	}
	return err
}

// AddImage gắn ảnh vào cuối danh sách, tối đa domain.MaxProductImages. Gắn lại ảnh đã có → không làm gì (idempotent).
// ponytail: 2 request gắn ảnh cùng lúc cho 1 sản phẩm đang có 9 ảnh có thể ra 11 ảnh; cần chặt thì khoá dòng products.
func (r *ProductRepo) AddImage(ctx context.Context, productID, assetID int64) error {
	q := db(ctx, r.pool)
	tag, err := q.Exec(ctx, `INSERT INTO product_images (product_id, asset_id, position)
		SELECT $1, $2, COALESCE(MAX(position) + 1, 0) FROM product_images WHERE product_id = $1
		HAVING COUNT(*) < $3
		ON CONFLICT (product_id, asset_id) DO NOTHING`, productID, assetID, domain.MaxProductImages)
	if pgCode(err) == foreignKeyViolation {
		return domain.ErrProductNotFound
	}
	if err != nil || tag.RowsAffected() == 1 {
		return err
	}
	var attached bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM product_images WHERE product_id = $1 AND asset_id = $2)`,
		productID, assetID).Scan(&attached); err != nil {
		return err
	}
	if attached {
		return nil
	}
	return domain.ErrTooManyImages
}

// RemoveImage chỉ gỡ liên kết; file trở thành rác và được job dọn rác xoá sau.
func (r *ProductRepo) RemoveImage(ctx context.Context, productID, assetID int64) error {
	tag, err := db(ctx, r.pool).Exec(ctx, `DELETE FROM product_images WHERE product_id = $1 AND asset_id = $2`, productID, assetID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrImageNotFound
	}
	return err
}

type imageRow struct {
	ProductID int64
	AssetID   int64
	Key       string
	Position  int
}

// loadImages lấy ảnh của CẢ trang trong 1 query (WHERE product_id = ANY(...)), tránh N+1 query.
func (r *ProductRepo) loadImages(ctx context.Context, conn querier, items []domain.Product) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, len(items))
	for i, p := range items {
		ids[i] = p.ID
	}
	rows, err := queryAll[imageRow](ctx, conn, `
		SELECT pi.product_id, pi.asset_id, a.key, pi.position
		FROM product_images pi JOIN assets a ON a.id = pi.asset_id
		WHERE pi.product_id = ANY($1) ORDER BY pi.product_id, pi.position, pi.asset_id`, ids)
	if err != nil {
		return err
	}
	byProduct := make(map[int64][]domain.Image, len(items))
	for _, row := range rows {
		byProduct[row.ProductID] = append(byProduct[row.ProductID], domain.Image{AssetID: row.AssetID, Key: row.Key, Position: row.Position})
	}
	for i := range items {
		items[i].Images = byProduct[items[i].ID]
	}
	return nil
}

func (r *ProductRepo) one(ctx context.Context, sql string, args ...any) (domain.Product, error) {
	p, err := queryOne[domain.Product](ctx, db(ctx, r.pool), sql, args...)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Product{}, domain.ErrProductNotFound
	}
	if err != nil {
		return domain.Product{}, productWriteErr(err)
	}
	return p, nil
}

func (r *ProductRepo) oneWithImages(ctx context.Context, sql string, args ...any) (domain.Product, error) {
	p, err := r.one(ctx, sql, args...)
	if err != nil {
		return p, err
	}
	items := []domain.Product{p}
	err = r.loadImages(ctx, db(ctx, r.pool), items)
	return items[0], err
}
