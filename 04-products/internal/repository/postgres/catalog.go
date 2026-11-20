package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"golearn/04-products/internal/domain"
)

// CatalogRepo: danh mục và hãng — bảng nhỏ (vài trăm/nghìn dòng), đọc cả bảng rồi cache trong service.
type CatalogRepo struct {
	pool *pgxpool.Pool
}

func NewCatalogRepo(pool *pgxpool.Pool) *CatalogRepo { return &CatalogRepo{pool: pool} }

const categoryCols = `id, parent_id, name, slug, path, depth, created_at`

func (r *CatalogRepo) Categories(ctx context.Context) ([]domain.Category, error) {
	return queryAll[domain.Category](ctx, db(ctx, r.pool), `SELECT `+categoryCols+` FROM categories ORDER BY depth, name, id`)
}

func (r *CatalogRepo) CreateCategory(ctx context.Context, c domain.Category) (domain.Category, error) {
	out, err := queryOne[domain.Category](ctx, db(ctx, r.pool), `INSERT INTO categories (parent_id, name, slug)
		VALUES ($1, $2, $3) RETURNING `+categoryCols, c.ParentID, c.Name, c.Slug)
	if pgCode(err) == foreignKeyViolation {
		return domain.Category{}, domain.ErrCategoryNotFound // cha không tồn tại
	}
	return out, catalogErr(err, domain.ErrCategoryNotFound)
}

// UpdateCategory chỉ đổi tên/slug; không đổi cha (path của cả cây con sẽ sai).
func (r *CatalogRepo) UpdateCategory(ctx context.Context, c domain.Category) (domain.Category, error) {
	out, err := queryOne[domain.Category](ctx, db(ctx, r.pool), `UPDATE categories SET name = $2, slug = $3
		WHERE id = $1 RETURNING `+categoryCols, c.ID, c.Name, c.Slug)
	return out, catalogErr(err, domain.ErrCategoryNotFound)
}

func (r *CatalogRepo) DeleteCategory(ctx context.Context, id int64) error {
	tag, err := db(ctx, r.pool).Exec(ctx, `DELETE FROM categories WHERE id = $1`, id)
	if pgCode(err) == foreignKeyViolation {
		return domain.ErrCategoryInUse // còn danh mục con
	}
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrCategoryNotFound
	}
	return err
}

func (r *CatalogRepo) Brands(ctx context.Context) ([]domain.Brand, error) {
	return queryAll[domain.Brand](ctx, db(ctx, r.pool), `SELECT id, name, slug, created_at FROM brands ORDER BY name`)
}

func (r *CatalogRepo) CreateBrand(ctx context.Context, b domain.Brand) (domain.Brand, error) {
	out, err := queryOne[domain.Brand](ctx, db(ctx, r.pool), `INSERT INTO brands (name, slug) VALUES ($1, $2)
		RETURNING id, name, slug, created_at`, b.Name, b.Slug)
	return out, catalogErr(err, domain.ErrBrandNotFound)
}

func (r *CatalogRepo) UpdateBrand(ctx context.Context, b domain.Brand) (domain.Brand, error) {
	out, err := queryOne[domain.Brand](ctx, db(ctx, r.pool), `UPDATE brands SET name = $2, slug = $3 WHERE id = $1
		RETURNING id, name, slug, created_at`, b.ID, b.Name, b.Slug)
	return out, catalogErr(err, domain.ErrBrandNotFound)
}

func (r *CatalogRepo) DeleteBrand(ctx context.Context, id int64) error {
	tag, err := db(ctx, r.pool).Exec(ctx, `DELETE FROM brands WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrBrandNotFound
	}
	return err
}

func catalogErr(err, notFound error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return notFound
	case pgCode(err) == uniqueViolation:
		return domain.ErrSlugTaken
	}
	return err
}
