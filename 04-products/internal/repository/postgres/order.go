package postgres

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"golearn/04-products/internal/domain"
)

type OrderRepo struct {
	pool *pgxpool.Pool
}

func NewOrderRepo(pool *pgxpool.Pool) *OrderRepo { return &OrderRepo{pool: pool} }

const orderCols = `id, user_id, status, total, version, created_at, updated_at`

type variantInfo struct {
	ID        int64
	ProductID int64
	Name      string
	Color     string
	Size      string
}

// Place đặt hàng trong 1 transaction: trừ kho từng biến thể bằng UPDATE nguyên tử (stock >= qty), thiếu 1 món
// là rollback cả đơn.
//
// Thứ tự khoá toàn hệ thống (chống deadlock): TẤT CẢ biến thể cần dùng (id tăng dần) → rồi mới tới sản phẩm
// (id tăng dần, trigger khoá khi tổng hợp kho). Nếu khoá xen kẽ "biến thể A1 → sản phẩm A → biến thể A2" thì
// đơn khác đang giữ A2 và chờ sản phẩm A sẽ tạo vòng chờ — test TestPostgres_CrossOrdersNoDeadlock bắt lỗi này.
// Giá lấy từ dòng biến thể vừa khoá: không ai đổi giá được giữa lúc đọc và lúc chốt đơn.
// Trả thêm id các sản phẩm bị ảnh hưởng để service xoá cache.
func (r *OrderRepo) Place(ctx context.Context, userID int64, lines []domain.OrderLine) (domain.Order, []int64, error) {
	ids := make([]int64, len(lines))
	for i, l := range lines {
		ids[i] = l.VariantID
	}
	var order domain.Order
	var products []int64
	err := inTx(ctx, r.pool, func(q querier) error {
		infos, err := queryAll[variantInfo](ctx, q, `SELECT v.id, v.product_id, p.name, v.color, v.size
			FROM product_variants v JOIN products p ON p.id = v.product_id WHERE v.id = ANY($1)`, ids)
		if err != nil {
			return err
		}
		if len(infos) != len(lines) {
			return domain.ErrVariantNotFound
		}
		if err := lockVariantIDs(ctx, q, ids); err != nil {
			return err
		}
		qty := make(map[int64]int64, len(lines))
		for _, l := range lines {
			qty[l.VariantID] = l.Qty
		}
		slices.SortFunc(infos, func(a, b variantInfo) int {
			return cmp.Or(cmp.Compare(a.ProductID, b.ProductID), cmp.Compare(a.ID, b.ID))
		})

		var total int64
		items := make([]domain.OrderItem, len(infos))
		for i, v := range infos {
			n := qty[v.ID]
			var price int64
			err := q.QueryRow(ctx, `UPDATE product_variants SET stock = stock - $2, version = version + 1, updated_at = now()
				WHERE id = $1 AND stock >= $2 RETURNING price`, v.ID, n).Scan(&price)
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrOutOfStock
			}
			if err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `UPDATE products SET sold_count = sold_count + $2 WHERE id = $1`, v.ProductID, n); err != nil {
				return err
			}
			items[i] = domain.OrderItem{VariantID: &v.ID, ProductID: &v.ProductID, Name: itemName(v), Qty: n, UnitPrice: price}
			total += n * price
			if len(products) == 0 || products[len(products)-1] != v.ProductID {
				products = append(products, v.ProductID)
			}
		}

		order, err = queryOne[domain.Order](ctx, q, `INSERT INTO orders (user_id, total) VALUES ($1, $2) RETURNING `+orderCols, userID, total)
		if err != nil {
			return err
		}
		vids, pids, names, qtys, prices := make([]int64, len(items)), make([]int64, len(items)), make([]string, len(items)),
			make([]int64, len(items)), make([]int64, len(items))
		for i, it := range items {
			vids[i], pids[i], names[i], qtys[i], prices[i] = *it.VariantID, *it.ProductID, it.Name, it.Qty, it.UnitPrice
		}
		_, err = q.Exec(ctx, `INSERT INTO order_items (order_id, variant_id, product_id, name, qty, unit_price)
			SELECT $1, * FROM unnest($2::bigint[], $3::bigint[], $4::text[], $5::bigint[], $6::bigint[])`,
			order.ID, vids, pids, names, qtys, prices)
		order.Items = items
		return err
	})
	return order, products, err
}

func itemName(v variantInfo) string {
	opts := make([]string, 0, 2)
	for _, o := range []string{v.Color, v.Size} {
		if o != "" {
			opts = append(opts, o)
		}
	}
	if len(opts) == 0 {
		return v.Name
	}
	return v.Name + " (" + strings.Join(opts, ", ") + ")"
}

func (r *OrderRepo) Get(ctx context.Context, id int64) (domain.Order, error) {
	o, err := queryOne[domain.Order](ctx, db(ctx, r.pool), `SELECT `+orderCols+` FROM orders WHERE id = $1`, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	if err != nil {
		return domain.Order{}, err
	}
	orders := []domain.Order{o}
	return orders[0], r.loadItems(ctx, orders)
}

// List: đơn của 1 khách, mới nhất trước, phân trang keyset theo id.
func (r *OrderRepo) List(ctx context.Context, userID, cursor int64, limit int) (domain.OrderPage, error) {
	orders, err := queryAll[domain.Order](ctx, db(ctx, r.pool), `SELECT `+orderCols+` FROM orders
		WHERE user_id = $1 AND ($2::bigint = 0 OR id < $2) ORDER BY id DESC LIMIT $3`, userID, cursor, limit+1)
	if err != nil {
		return domain.OrderPage{}, err
	}
	page := domain.OrderPage{Items: orders}
	if len(orders) > limit {
		page.Items = orders[:limit]
		page.NextCursor = page.Items[limit-1].ID
	}
	return page, r.loadItems(ctx, page.Items)
}

type orderItemRow struct {
	OrderID   int64
	VariantID *int64
	ProductID *int64
	Name      string
	Qty       int64
	UnitPrice int64
}

func (r *OrderRepo) loadItems(ctx context.Context, orders []domain.Order) error {
	if len(orders) == 0 {
		return nil
	}
	ids := make([]int64, len(orders))
	for i, o := range orders {
		ids[i] = o.ID
	}
	rows, err := queryAll[orderItemRow](ctx, db(ctx, r.pool), `SELECT order_id, variant_id, product_id, name, qty, unit_price
		FROM order_items WHERE order_id = ANY($1) ORDER BY order_id, id`, ids)
	if err != nil {
		return err
	}
	byOrder := make(map[int64][]domain.OrderItem, len(orders))
	for _, it := range rows {
		byOrder[it.OrderID] = append(byOrder[it.OrderID], domain.OrderItem{VariantID: it.VariantID, ProductID: it.ProductID,
			Name: it.Name, Qty: it.Qty, UnitPrice: it.UnitPrice})
	}
	for i := range orders {
		orders[i].Items = byOrder[orders[i].ID]
	}
	return nil
}

// SetStatus chuyển trạng thái nếu trạng thái hiện tại nằm trong from (kiểm tra ngay trong UPDATE → không có
// khe hở giữa đọc và ghi). ownerID > 0: chỉ đơn của khách đó. Huỷ đơn thì trả kho + trừ lại số đã bán trong
// CÙNG transaction. Trả thêm id các sản phẩm bị ảnh hưởng (khi huỷ) để xoá cache.
func (r *OrderRepo) SetStatus(ctx context.Context, id, ownerID int64, to domain.OrderStatus, from []domain.OrderStatus) (domain.Order, []int64, error) {
	fromStr := make([]string, len(from))
	for i, s := range from {
		fromStr[i] = string(s)
	}
	var products []int64
	err := inTx(ctx, r.pool, func(q querier) error {
		tag, err := q.Exec(ctx, `UPDATE orders SET status = $2, version = version + 1, updated_at = now()
			WHERE id = $1 AND status = ANY($3) AND ($4::bigint = 0 OR user_id = $4)`, id, string(to), fromStr, ownerID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1 AND ($2::bigint = 0 OR user_id = $2))`,
				id, ownerID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return domain.ErrOrderNotFound
			}
			return domain.ErrInvalidTransition
		}
		if to != domain.OrderCancelled {
			return nil
		}
		items, err := queryAll[orderItemRow](ctx, q, `SELECT order_id, variant_id, product_id, name, qty, unit_price
			FROM order_items WHERE order_id = $1 AND variant_id IS NOT NULL ORDER BY product_id, variant_id`, id)
		if err != nil {
			return err
		}
		vids := make([]int64, len(items))
		for i, it := range items {
			vids[i] = *it.VariantID
		}
		if err := lockVariantIDs(ctx, q, vids); err != nil { // cùng thứ tự khoá với Place
			return err
		}
		for _, it := range items {
			if _, err := q.Exec(ctx, `UPDATE product_variants SET stock = stock + $2, version = version + 1, updated_at = now()
				WHERE id = $1`, *it.VariantID, it.Qty); err != nil {
				return err
			}
			if it.ProductID != nil {
				if _, err := q.Exec(ctx, `UPDATE products SET sold_count = GREATEST(sold_count - $2, 0) WHERE id = $1`,
					*it.ProductID, it.Qty); err != nil {
					return err
				}
				products = append(products, *it.ProductID)
			}
		}
		return nil
	})
	if err != nil {
		return domain.Order{}, nil, err
	}
	o, err := r.Get(ctx, id)
	return o, products, err
}

// lockVariantIDs khoá các biến thể theo id tăng dần (bước 1 của thứ tự khoá, xem Place).
func lockVariantIDs(ctx context.Context, q querier, ids []int64) error {
	_, err := q.Exec(ctx, `SELECT 1 FROM product_variants WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	return err
}
