package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"golearn/04-products/internal/domain"
)

// SearchSource: dữ liệu cho search engine — outbox (sản phẩm nào vừa đổi) và document ghép từ nhiều bảng.
type SearchSource struct {
	pool *pgxpool.Pool
}

func NewSearchSource(pool *pgxpool.Pool) *SearchSource { return &SearchSource{pool: pool} }

const outboxLockID = 4_040_002

// DrainOutbox lấy (và xoá) tối đa limit dòng outbox, gọi fn với các product_id (không trùng). fn lỗi → rollback,
// các dòng quay lại outbox để lần sau làm lại. Khoá advisory: cả cụm chỉ 1 instance đồng bộ tại 1 thời điểm,
// nên không có chuyện 2 instance đẩy 2 phiên bản của cùng 1 sản phẩm lên Meilisearch sai thứ tự.
// Trả số product_id đã xử lý; 0 khi outbox rỗng hoặc instance khác đang giữ khoá.
func (s *SearchSource) DrainOutbox(ctx context.Context, limit int, fn func(ctx context.Context, ids []int64) error) (int, error) {
	n := 0
	err := inTx(ctx, s.pool, func(q querier) error {
		var locked bool
		if err := q.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, outboxLockID).Scan(&locked); err != nil || !locked {
			return err
		}
		rows, _ := q.Query(ctx, `DELETE FROM search_outbox WHERE id IN (SELECT id FROM search_outbox ORDER BY id LIMIT $1)
			RETURNING product_id`, limit)
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil || len(ids) == 0 {
			return err
		}
		seen := make(map[int64]bool, len(ids))
		uniq := ids[:0]
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				uniq = append(uniq, id)
			}
		}
		n = len(uniq)
		return fn(ctx, uniq)
	})
	return n, err
}

// FlushSold chuyển các sản phẩm vừa đổi sold_count (search_sold_dirty) vào outbox để job search-sync đẩy đi.
// DELETE ... RETURNING trong 1 câu: nhiều instance chạy cùng lúc thì mỗi dòng chỉ được lấy 1 lần.
func (s *SearchSource) FlushSold(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `WITH d AS (DELETE FROM search_sold_dirty RETURNING product_id)
		INSERT INTO search_outbox (product_id) SELECT product_id FROM d`)
	return tag.RowsAffected(), err
}

// OutboxBacklog: số dòng đang chờ đồng bộ (để theo dõi Meilisearch có bị tụt lại không).
func (s *SearchSource) OutboxBacklog(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM search_outbox`).Scan(&n)
	return n, err
}

// docSelect ghép document từ products + hãng + tag + thuộc tính + màu/size của biến thể + ảnh đầu tiên.
// Mỗi phần là subquery theo product_id (có index) → chi phí tỉ lệ với số sản phẩm cần ghép, không quét cả bảng.
const docSelect = `SELECT p.id, p.name, p.sku, p.price, p.stock > 0 AS in_stock, p.rating_sum, p.rating_count, p.sold_count,
	p.created_at AS created_time, p.category_id, COALESCE(b.name, '') AS brand,
	COALESCE((SELECT array_agg(t.name ORDER BY t.name) FROM product_tags pt JOIN tags t ON t.id = pt.tag_id
		WHERE pt.product_id = p.id), '{}') AS tags,
	COALESCE((SELECT array_agg(a.name || ': ' || av.value ORDER BY a.name, av.value) FROM product_attribute_values pav
		JOIN attribute_values av ON av.id = pav.value_id JOIN attributes a ON a.id = av.attribute_id
		WHERE pav.product_id = p.id), '{}') AS attributes,
	COALESCE((SELECT array_agg(DISTINCT v.color) FROM product_variants v WHERE v.product_id = p.id AND v.color <> ''), '{}') AS colors,
	COALESCE((SELECT array_agg(DISTINCT v.size) FROM product_variants v WHERE v.product_id = p.id AND v.size <> ''), '{}') AS sizes,
	COALESCE((SELECT a.key FROM product_images pi JOIN assets a ON a.id = pi.asset_id
		WHERE pi.product_id = p.id ORDER BY pi.position LIMIT 1), '') AS image_key
	FROM products p LEFT JOIN brands b ON b.id = p.brand_id`

type docScan struct {
	ID          int64
	Name        string
	SKU         string
	Price       int64
	InStock     bool
	RatingSum   int64
	RatingCount int64
	SoldCount   int64
	CreatedTime time.Time
	CategoryID  *int64
	Brand       string
	Tags        []string
	Attributes  []string
	Colors      []string
	Sizes       []string
	ImageKey    string
}

// Docs ghép document cho các sản phẩm còn tồn tại; sản phẩm đã xoá không có trong kết quả.
func (s *SearchSource) Docs(ctx context.Context, ids []int64) ([]domain.SearchDoc, error) {
	return s.docs(ctx, docSelect+` WHERE p.id = ANY($1)`, ids)
}

// DocsAfter: lô tiếp theo theo id tăng dần (dựng lại toàn bộ index).
func (s *SearchSource) DocsAfter(ctx context.Context, afterID int64, limit int) ([]domain.SearchDoc, error) {
	return s.docs(ctx, docSelect+` WHERE p.id > $1 ORDER BY p.id LIMIT $2`, afterID, limit)
}

func (s *SearchSource) docs(ctx context.Context, sql string, args ...any) ([]domain.SearchDoc, error) {
	rows, err := queryAll[docScan](ctx, s.pool, sql, args...)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SearchDoc, len(rows))
	for i, r := range rows {
		p := domain.Product{RatingSum: r.RatingSum, RatingCount: r.RatingCount}
		out[i] = domain.SearchDoc{ID: r.ID, Name: r.Name, NameASCII: domain.FoldVietnamese(r.Name), SKU: r.SKU, Brand: r.Brand,
			Tags: r.Tags, Attributes: r.Attributes, Colors: r.Colors, Sizes: r.Sizes, Price: r.Price, InStock: r.InStock,
			Rating: p.Rating(), RatingCount: r.RatingCount, SoldCount: r.SoldCount, ImageKey: r.ImageKey,
			CreatedAt: r.CreatedTime.Unix(), CreatedTime: r.CreatedTime, CategoryID: r.CategoryID}
	}
	return out, nil
}
