package service

import (
	"context"
	"log/slog"

	"golearn/04-products/internal/domain"
)

const (
	syncBatch    = 1000 // dòng outbox mỗi lượt
	syncMaxLoops = 20   // tối đa 20.000 sản phẩm mỗi lần job chạy, phần còn lại để lần sau
	reindexBatch = 5000
)

// Reindexer: engine dựng lại toàn bộ index mà không làm gián đoạn search (Meilisearch: index phụ + swap).
type Reindexer interface {
	Reindex(ctx context.Context, feed func(push func([]domain.SearchDoc) error) error, log func(string, ...any)) error
}

// SearchSync đồng bộ Postgres → search engine theo outbox (xem migrations/004_catalog.sql).
type SearchSync struct {
	source  SearchSource
	engine  SearchEngine
	catalog *CatalogService
	log     *slog.Logger
}

func NewSearchSync(source SearchSource, engine SearchEngine, catalog *CatalogService, log *slog.Logger) *SearchSync {
	return &SearchSync{source: source, engine: engine, catalog: catalog, log: log}
}

// Run: job nền, chạy mỗi giây trên mọi instance; khoá advisory trong DrainOutbox bảo đảm chỉ 1 instance làm.
// Engine lỗi → transaction rollback, outbox giữ nguyên, lần sau thử lại (không mất cập nhật nào).
func (s *SearchSync) Run(ctx context.Context) error {
	for range syncMaxLoops {
		n, err := s.source.DrainOutbox(ctx, syncBatch, s.sync)
		if err != nil || n < syncBatch {
			return err
		}
	}
	return nil
}

func (s *SearchSync) sync(ctx context.Context, ids []int64) error {
	docs, err := s.source.Docs(ctx, ids)
	if err != nil {
		return err
	}
	if err := s.enrich(ctx, docs); err != nil {
		return err
	}
	alive := make(map[int64]bool, len(docs))
	for _, d := range docs {
		alive[d.ID] = true
	}
	var gone []int64
	for _, id := range ids {
		if !alive[id] {
			gone = append(gone, id)
		}
	}
	if err := s.engine.Upsert(ctx, docs); err != nil {
		return err
	}
	return s.engine.Delete(ctx, gone)
}

// enrich điền tên danh mục lá + id các danh mục từ gốc xuống, lấy từ cache danh mục (bảng nhỏ) thay vì JOIN đệ quy.
func (s *SearchSync) enrich(ctx context.Context, docs []domain.SearchDoc) error {
	idx, err := s.catalog.CategoryIndex(ctx)
	if err != nil {
		return err
	}
	for i := range docs {
		d := &docs[i]
		d.CategoryIDs = []int64{}
		if d.CategoryID == nil {
			continue
		}
		for _, c := range domain.CategoryAncestors(idx, *d.CategoryID) {
			d.CategoryIDs = append(d.CategoryIDs, c.ID)
			d.Category = c.Name // phần tử cuối = danh mục lá
		}
	}
	return nil
}

// Reindex dựng lại toàn bộ index từ Postgres (sau khi seed dữ liệu lớn, hoặc khi index hỏng).
func (s *SearchSync) Reindex(ctx context.Context, r Reindexer) error {
	logf := func(msg string, args ...any) { s.log.InfoContext(ctx, msg, args...) }
	return r.Reindex(ctx, func(push func([]domain.SearchDoc) error) error {
		var after, total int64
		for {
			docs, err := s.source.DocsAfter(ctx, after, reindexBatch)
			if err != nil || len(docs) == 0 {
				return err
			}
			if err := s.enrich(ctx, docs); err != nil {
				return err
			}
			if err := push(docs); err != nil {
				return err
			}
			after = docs[len(docs)-1].ID
			total += int64(len(docs))
			if total%100_000 < reindexBatch {
				logf("reindex progress", "documents", total, "last_id", after)
			}
		}
	}, logf)
}
