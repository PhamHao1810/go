package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"golearn/04-products/internal/domain"
)

// breakerOpenFor: Meilisearch không kết nối được thì bỏ qua nó ngần này, đi thẳng Postgres. Không có cái này, khi
// máy search chết MỌI request search phải thử kết nối lại rồi mới fallback.
const breakerOpenFor = 5 * time.Second

// SearchService: tìm kiếm cho khách. Ưu tiên Meilisearch (sai chính tả, không dấu, facet).
//   - Meilisearch CHẾT (không kết nối được, lỗi 5xx) → Postgres full-text (kém thông minh, không facet).
//   - Meilisearch QUÁ TẢI (trả lời chậm hơn timeout) → 503 SEARCH_BUSY ngay, KHÔNG fallback. Đo thật trên cụm:
//     fallback lúc quá tải dồn full-text search lên Postgres standby (cùng máy với Meilisearch) → máy đó càng ngợp,
//     kéo chậm cả duyệt danh mục đang đọc standby. Thà báo bận vài request search còn hơn làm chậm mọi thứ.
type SearchService struct {
	engine   SearchEngine // nil = chưa cấu hình Meilisearch, luôn dùng Postgres
	fallback ProductRepository
	cache    SearchCache
	timeout  time.Duration
	log      *slog.Logger
	sf       singleflight.Group
	openTill atomic.Int64 // unix nano: trước thời điểm này không gọi engine
}

func NewSearchService(engine SearchEngine, fallback ProductRepository, cache SearchCache, timeout time.Duration, log *slog.Logger) *SearchService {
	return &SearchService{engine: engine, fallback: fallback, cache: cache, timeout: timeout, log: log}
}

func (s *SearchService) Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, error) {
	if err := q.Normalize(); err != nil {
		return domain.SearchResult{}, err
	}
	b, _ := json.Marshal(q)
	sum := sha256.Sum256(b)
	key := hex.EncodeToString(sum[:16])
	if r, ok, err := s.cache.GetSearch(ctx, key); err != nil {
		s.log.WarnContext(ctx, "search cache get failed", "err", err)
	} else if ok {
		return r, nil
	}
	v, err, _ := s.sf.Do(key, func() (any, error) {
		// Khách đầu tiên ngắt kết nối thì các request chờ chung không lỗi theo; vẫn giữ hạn chót để fallback Postgres không treo.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		r, err := s.search(ctx, q)
		if err != nil {
			return nil, err
		}
		if err := s.cache.SetSearch(ctx, key, r); err != nil {
			s.log.WarnContext(ctx, "search cache set failed", "err", err)
		}
		return r, nil
	})
	if err != nil {
		return domain.SearchResult{}, err
	}
	return v.(domain.SearchResult), nil
}

func (s *SearchService) search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, error) {
	if s.engine != nil && time.Now().UnixNano() >= s.openTill.Load() {
		ectx, cancel := context.WithTimeout(ctx, s.timeout)
		r, err := s.engine.Search(ectx, q)
		cancel()
		if err == nil {
			return r, nil
		}
		if timedOut(err) && !unreachable(err) {
			return domain.SearchResult{}, domain.ErrSearchBusy
		}
		s.openTill.Store(time.Now().Add(breakerOpenFor).UnixNano())
		s.log.WarnContext(ctx, "search engine failed, using postgres for a while", "err", err, "for", breakerOpenFor.String())
	}
	return s.postgres(ctx, q)
}

// unreachable: không mở nổi kết nối tới engine (máy search chết/mất mạng — timeout lúc dial, khác "engine bận trả lời
// chậm"). Phải mở breaker sang Postgres, không thì máy search chết = mọi lượt search 503 tới khi deploy -Skip.
// Client Meilisearch đặt dial timeout ngắn hơn SearchTimeout để lỗi này hiện ra trước khi ctx hết hạn.
func unreachable(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

func timedOut(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

// postgres: tìm bằng full-text của Postgres (tsvector, có sẵn từ trước). Chỉ trang 1, không facet, bỏ qua
// lọc hãng/màu/size/điểm — đủ để khách vẫn tìm và mua được trong lúc Meilisearch có sự cố.
func (s *SearchService) postgres(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, error) {
	if q.Page > 1 {
		return domain.SearchResult{}, domain.ErrSearchUnavailable
	}
	f := domain.ListFilter{Query: q.Q, CategoryID: q.CategoryID, MinPrice: q.MinPrice, MaxPrice: q.MaxPrice,
		InStock: q.InStock, Limit: q.Limit}
	switch q.Sort {
	case domain.SearchPriceAsc:
		f.Sort = domain.SortPriceAsc
	case domain.SearchPriceDesc:
		f.Sort = domain.SortPriceDesc
	case domain.SearchNewest:
		f.Sort = domain.SortNewest
	}
	if err := f.Normalize(); err != nil {
		return domain.SearchResult{}, err
	}
	start := time.Now()
	page, err := s.fallback.List(ctx, f)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return domain.SearchResult{}, domain.ErrSearchUnavailable
		}
		return domain.SearchResult{}, err
	}
	out := domain.SearchResult{Hits: make([]domain.SearchHit, len(page.Items)), Total: len(page.Items),
		Facets: map[string]map[string]int{}, Engine: "postgres", TookMs: int(time.Since(start).Milliseconds())}
	for i, p := range page.Items {
		h := domain.SearchHit{ID: p.ID, Name: p.Name, SKU: p.SKU, Price: p.Price, InStock: p.Stock > 0, Rating: p.Rating(),
			RatingCount: p.RatingCount, SoldCount: p.SoldCount, CreatedAt: p.CreatedAt}
		if len(p.Images) > 0 {
			h.ImageKey = p.Images[0].Key
		}
		out.Hits[i] = h
	}
	return out, nil
}
