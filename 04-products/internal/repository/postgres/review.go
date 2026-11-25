package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"golearn/04-products/internal/domain"
)

type ReviewRepo struct {
	pool *pgxpool.Pool
	read *ReadRouter // primary không trả lời → List đọc standby
}

func NewReviewRepo(pool *pgxpool.Pool) *ReviewRepo { return &ReviewRepo{pool: pool} }

func (r *ReviewRepo) ReadFrom(rr *ReadRouter) *ReviewRepo {
	r.read = rr
	return r
}

// Create: chỉ khách đã NHẬN hàng (đơn delivered có sản phẩm này) mới đánh giá được, mỗi người 1 lần.
// Điểm tổng (rating_sum/count) cộng trong cùng transaction → không bao giờ lệch với bảng reviews.
func (r *ReviewRepo) Create(ctx context.Context, rv domain.Review) (domain.Review, error) {
	var out domain.Review
	err := inTx(ctx, r.pool, func(q querier) error {
		var bought bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM orders o JOIN order_items i ON i.order_id = o.id
			WHERE o.user_id = $1 AND o.status = 'delivered' AND i.product_id = $2)`, rv.UserID, rv.ProductID).Scan(&bought); err != nil {
			return err
		}
		if !bought {
			return existsIn(ctx, q, rv.ProductID, domain.ErrReviewNotAllowed)
		}
		var err error
		out, err = queryOne[domain.Review](ctx, q, `WITH r AS (
				INSERT INTO reviews (product_id, user_id, rating, content) VALUES ($1, $2, $3, $4)
				RETURNING id, product_id, user_id, rating, content, created_at)
			SELECT r.*, u.name AS user_name FROM r JOIN users u ON u.id = r.user_id`,
			rv.ProductID, rv.UserID, rv.Rating, rv.Content)
		switch {
		case pgCode(err) == uniqueViolation:
			return domain.ErrAlreadyReviewed
		case pgCode(err) == foreignKeyViolation:
			return domain.ErrProductNotFound
		case err != nil:
			return err
		}
		_, err = q.Exec(ctx, `UPDATE products SET rating_sum = rating_sum + $2, rating_count = rating_count + 1 WHERE id = $1`,
			rv.ProductID, rv.Rating)
		return err
	})
	return out, err
}

// List: đánh giá của 1 sản phẩm, mới nhất trước, keyset theo id.
func (r *ReviewRepo) List(ctx context.Context, productID, cursor int64, limit int) (domain.ReviewPage, error) {
	pool := r.pool
	if r.read != nil && r.read.PrimaryDown() {
		pool = r.read.replica
	}
	items, err := queryAll[domain.Review](ctx, db(ctx, pool), `SELECT r.id, r.product_id, r.user_id, u.name AS user_name,
			r.rating, r.content, r.created_at
		FROM reviews r JOIN users u ON u.id = r.user_id
		WHERE r.product_id = $1 AND ($2::bigint = 0 OR r.id < $2) ORDER BY r.id DESC LIMIT $3`, productID, cursor, limit+1)
	if err != nil {
		return domain.ReviewPage{}, err
	}
	page := domain.ReviewPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = page.Items[limit-1].ID
	}
	return page, nil
}
