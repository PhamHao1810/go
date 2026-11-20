package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"golearn/04-products/internal/domain"
)

const variantCols = `id, product_id, sku, color, size, price, stock, version, created_at, updated_at`

// VariantRepo: biến thể của sản phẩm. Mọi thay đổi giá/kho ở đây được trigger tổng hợp lên products.price/stock.
type VariantRepo struct {
	pool *pgxpool.Pool
}

func NewVariantRepo(pool *pgxpool.Pool) *VariantRepo { return &VariantRepo{pool: pool} }

func (r *VariantRepo) Create(ctx context.Context, v domain.Variant) (domain.Variant, error) {
	out, err := queryOne[domain.Variant](ctx, db(ctx, r.pool), `INSERT INTO product_variants (product_id, sku, color, size, price, stock)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+variantCols, v.ProductID, v.SKU, v.Color, v.Size, v.Price, v.Stock)
	return out, variantErr(err)
}

// Update: optimistic locking theo version của biến thể (giống products).
func (r *VariantRepo) Update(ctx context.Context, v domain.Variant) (domain.Variant, error) {
	out, err := queryOne[domain.Variant](ctx, db(ctx, r.pool), `UPDATE product_variants
		SET sku = $3, color = $4, size = $5, price = $6, stock = $7, version = version + 1, updated_at = now()
		WHERE id = $1 AND product_id = $2 AND version = $8 RETURNING `+variantCols,
		v.ID, v.ProductID, v.SKU, v.Color, v.Size, v.Price, v.Stock, v.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := db(ctx, r.pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM product_variants WHERE id = $1 AND product_id = $2)`,
			v.ID, v.ProductID).Scan(&exists); err != nil {
			return domain.Variant{}, err
		}
		if exists {
			return domain.Variant{}, domain.ErrVariantConflict
		}
		return domain.Variant{}, domain.ErrVariantNotFound
	}
	return out, variantErr(err)
}

// Delete không cho xoá biến thể cuối cùng (sản phẩm phải mua được). Khoá mọi biến thể của sản phẩm trước
// để 2 request xoá 2 biến thể cuối cùng cùng lúc không lọt cả hai.
func (r *VariantRepo) Delete(ctx context.Context, productID, variantID int64) error {
	return inTx(ctx, r.pool, func(q querier) error {
		n, err := lockVariants(ctx, q, productID)
		if err != nil {
			return err
		}
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM product_variants WHERE id = $1 AND product_id = $2)`,
			variantID, productID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return domain.ErrVariantNotFound
		}
		if n <= 1 {
			return domain.ErrLastVariant
		}
		_, err = q.Exec(ctx, `DELETE FROM product_variants WHERE id = $1`, variantID)
		return err
	})
}

func variantErr(err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.ErrVariantNotFound
	case pgCode(err) == uniqueViolation:
		return domain.ErrSKUTaken
	case pgCode(err) == foreignKeyViolation:
		return domain.ErrProductNotFound
	}
	return err
}
