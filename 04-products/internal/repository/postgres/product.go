package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"golearn/04-products/internal/domain"
)

const cols = `id, sku, name, description, price, stock, version, created_at, updated_at`

type ProductRepo struct {
	pool *pgxpool.Pool
	read *ReadRouter // nil = mọi truy vấn đọc primary
}

func NewProductRepo(pool *pgxpool.Pool) *ProductRepo {
	return &ProductRepo{pool: pool}
}

// ReadFrom cho List (search, danh sách) đọc từ standby khi nó theo kịp primary.
func (r *ProductRepo) ReadFrom(rr *ReadRouter) *ProductRepo {
	r.read = rr
	return r
}

func (r *ProductRepo) Create(ctx context.Context, p domain.Product) (domain.Product, error) {
	return r.one(ctx, `INSERT INTO products (sku, name, description, price, stock)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+cols,
		p.SKU, p.Name, p.Description, p.Price, p.Stock)
}

func (r *ProductRepo) Get(ctx context.Context, id int64) (domain.Product, error) {
	return r.oneWithImages(ctx, `SELECT `+cols+` FROM products WHERE id = $1`, id)
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
func (r *ProductRepo) Update(ctx context.Context, p domain.Product) (domain.Product, error) {
	out, err := r.oneWithImages(ctx, `UPDATE products
		SET sku = $2, name = $3, description = $4, price = $5, stock = $6, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $7 RETURNING `+cols,
		p.ID, p.SKU, p.Name, p.Description, p.Price, p.Stock, p.Version)
	if errors.Is(err, domain.ErrProductNotFound) {
		return domain.Product{}, r.existsOr(ctx, p.ID, domain.ErrVersionConflict)
	}
	return out, err
}

func (r *ProductRepo) Delete(ctx context.Context, id int64) error {
	tag, err := db(ctx, r.pool).Exec(ctx, `DELETE FROM products WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrProductNotFound
	}
	return err
}

// Purchase trừ kho trong 1 câu UPDATE nguyên tử: Postgres khoá dòng, 1000 người mua món còn 5 cái thì đúng
// 5 người thành công, không bao giờ âm kho. Tăng version để admin đang sửa bằng version cũ nhận 409
// thay vì ghi đè số tồn kho vừa bị trừ.
func (r *ProductRepo) Purchase(ctx context.Context, id, qty int64) (domain.Product, error) {
	out, err := r.oneWithImages(ctx, `UPDATE products
		SET stock = stock - $2, version = version + 1, updated_at = now()
		WHERE id = $1 AND stock >= $2 RETURNING `+cols, id, qty)
	if errors.Is(err, domain.ErrProductNotFound) {
		return domain.Product{}, r.existsOr(ctx, id, domain.ErrOutOfStock)
	}
	return out, err
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

// existsOr: UPDATE không khớp dòng nào → sản phẩm không tồn tại (404) hoặc điều kiện sai (409).
func (r *ProductRepo) existsOr(ctx context.Context, id int64, errIfExists error) error {
	var exists bool
	if err := db(ctx, r.pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM products WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errIfExists
	}
	return domain.ErrProductNotFound
}

func (r *ProductRepo) one(ctx context.Context, sql string, args ...any) (domain.Product, error) {
	p, err := queryOne[domain.Product](ctx, db(ctx, r.pool), sql, args...)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Product{}, domain.ErrProductNotFound
	case pgCode(err) == uniqueViolation: // trùng sku
		return domain.Product{}, domain.ErrSKUTaken
	}
	return p, err
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
