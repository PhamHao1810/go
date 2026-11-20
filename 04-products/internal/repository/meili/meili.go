// Package meili cài đặt search engine bằng Meilisearch qua HTTP API (không cần thư viện ngoài).
package meili

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golearn/04-products/internal/domain"
)

const (
	Index       = "products"
	reindexUID  = "products_new" // index dựng lại song song, xong thì swap với Index
	mirrorCheck = 10 * time.Second
)

// settings: field nào tìm được, lọc được, sắp được. Áp lại mỗi lần khởi động (Meilisearch bỏ qua nếu không đổi).
var settings = map[string]any{
	// Thứ tự = độ ưu tiên khi chấm điểm: khớp tên quan trọng hơn khớp tag.
	// Chỉ tên danh mục LÁ: tìm theo cả đường dẫn ("thời trang") khớp hàng trăm nghìn sản phẩm → tốn CPU, kết quả loãng;
	// muốn cả danh mục cha thì lọc category_ids.
	"searchableAttributes": []string{"name", "name_ascii", "brand", "category", "tags", "attributes", "sku"},
	"filterableAttributes": []string{"category_ids", "category", "brand", "colors", "sizes", "price", "in_stock", "rating"},
	"sortableAttributes":   []string{"price", "rating", "sold_count", "created_at"},
	// Mặc định của Meilisearch + cuối cùng: cùng độ khớp thì hàng bán chạy lên trước.
	"rankingRules": []string{"words", "typo", "proximity", "attribute", "sort", "exactness", "sold_count:desc"},
	"displayedAttributes": []string{"id", "name", "sku", "brand", "category", "price", "in_stock", "rating", "rating_count",
		"sold_count", "image_key", "created_at"},
	"pagination":    map[string]any{"maxTotalHits": domain.SearchMaxHits},
	"faceting":      map[string]any{"maxValuesPerFacet": 30},
	"typoTolerance": map[string]any{"disableOnAttributes": []string{"sku"}}, // mã SKU phải gõ đúng
}

var facets = []string{"brand", "category", "colors", "sizes"}

type Client struct {
	base string
	key  string
	http *http.Client

	mu        sync.Mutex
	mirror    bool // đang có index dựng lại → ghi vào cả 2 để không mất thay đổi trong lúc reindex
	mirrorAge time.Time
}

func New(url, key string) *Client {
	return &Client{base: strings.TrimRight(url, "/"), key: key, http: &http.Client{
		Timeout: 60 * time.Second, // trần cho lệnh lớn (đẩy lô document); search tự đặt timeout ngắn qua ctx
		Transport: &http.Transport{MaxIdleConnsPerHost: 64, IdleConnTimeout: 90 * time.Second,
			// Mạng nội bộ (WireGuard) mở kết nối mất vài ms; quá 300ms = máy search chết → lỗi "dial" (search chuyển
			// sang Postgres) thay vì chờ tới SEARCH_TIMEOUT rồi báo bận.
			DialContext: (&net.Dialer{Timeout: 300 * time.Millisecond, KeepAlive: 30 * time.Second}).DialContext},
	}}
}

// APIError: lỗi Meilisearch trả về (vd index_not_found).
type APIError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("meilisearch %d %s: %s", e.Status, e.Code, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		apiErr := &APIError{Status: resp.StatusCode}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(apiErr)
		return apiErr
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func isCode(err error, code string) bool {
	var e *APIError
	return errors.As(err, &e) && e.Code == code
}

type task struct {
	TaskUID int64  `json:"taskUid"`
	UID     int64  `json:"uid"`
	Status  string `json:"status"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Setup tạo index (nếu chưa có) và áp settings. Chạy lúc khởi động; lỗi thì chỉ log, search tự dùng Postgres.
func (c *Client) Setup(ctx context.Context) error {
	return c.setupIndex(ctx, Index)
}

func (c *Client) setupIndex(ctx context.Context, uid string) error {
	var t task
	err := c.do(ctx, http.MethodPost, "/indexes", map[string]string{"uid": uid, "primaryKey": "id"}, &t)
	if err != nil && !isCode(err, "index_already_exists") {
		return err
	}
	if err == nil {
		if err := c.wait(ctx, t.TaskUID); err != nil && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	if err := c.do(ctx, http.MethodPatch, "/indexes/"+uid+"/settings", settings, &t); err != nil {
		return err
	}
	return c.wait(ctx, t.TaskUID)
}

// wait chờ 1 task xử lý xong (Meilisearch xử lý ghi bất đồng bộ theo hàng đợi).
func (c *Client) wait(ctx context.Context, uid int64) error {
	for {
		var t task
		if err := c.do(ctx, http.MethodGet, "/tasks/"+strconv.FormatInt(uid, 10), nil, &t); err != nil {
			return err
		}
		switch t.Status {
		case "succeeded":
			return nil
		case "failed", "canceled":
			msg := t.Status
			if t.Error != nil {
				msg = t.Error.Message
			}
			return fmt.Errorf("meilisearch task %d: %s", uid, msg)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Upsert thêm/thay document. Meilisearch nhận vào hàng đợi rồi trả ngay; index cập nhật sau vài trăm ms.
func (c *Client) Upsert(ctx context.Context, docs []domain.SearchDoc) error {
	if len(docs) == 0 {
		return nil
	}
	for _, uid := range c.targets(ctx) {
		if err := c.push(ctx, uid, docs, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	for _, uid := range c.targets(ctx) {
		if err := c.do(ctx, http.MethodPost, "/indexes/"+uid+"/documents/delete-batch", ids, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) push(ctx context.Context, uid string, docs []domain.SearchDoc, t *task) error {
	var out any // giữ nil "thật": interface chứa (*task)(nil) thì out != nil và Decode sẽ lỗi
	if t != nil {
		out = t
	}
	return c.do(ctx, http.MethodPost, "/indexes/"+uid+"/documents?primaryKey=id", docs, out)
}

// targets: index cần ghi. Trong lúc reindex (có products_new) ghi vào cả hai, kiểm tra lại mỗi 10 giây.
func (c *Client) targets(ctx context.Context) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.mirrorAge) > mirrorCheck {
		err := c.do(ctx, http.MethodGet, "/indexes/"+reindexUID, nil, nil)
		c.mirror, c.mirrorAge = err == nil, time.Now()
	}
	if c.mirror {
		return []string{Index, reindexUID}
	}
	return []string{Index}
}

// Reindex dựng lại toàn bộ index mà search không gián đoạn: đổ dữ liệu vào products_new, chờ xử lý xong rồi
// swap với products (đổi tên nguyên tử) và xoá bản cũ. Job đồng bộ ở các instance phát hiện products_new trong
// vòng 10 giây và ghi vào cả hai, nên thay đổi xảy ra trong lúc reindex không bị mất.
func (c *Client) Reindex(ctx context.Context, feed func(push func([]domain.SearchDoc) error) error, log func(string, ...any)) error {
	_ = c.do(ctx, http.MethodDelete, "/indexes/"+reindexUID, nil, nil) // dọn lần chạy dở trước (nếu có)
	if err := c.setupIndex(ctx, reindexUID); err != nil {
		return err
	}
	log("waiting for sync jobs to start mirroring", "seconds", (mirrorCheck + 2*time.Second).Seconds())
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(mirrorCheck + 2*time.Second):
	}
	var last task
	if err := feed(func(docs []domain.SearchDoc) error { return c.push(ctx, reindexUID, docs, &last) }); err != nil {
		return err
	}
	log("all batches enqueued, waiting for indexing", "last_task", last.TaskUID)
	if last.TaskUID > 0 {
		if err := c.wait(ctx, last.TaskUID); err != nil {
			return err
		}
	}
	var t task
	if err := c.do(ctx, http.MethodPost, "/swap-indexes", []map[string][]string{{"indexes": {Index, reindexUID}}}, &t); err != nil {
		return err
	}
	if err := c.wait(ctx, t.TaskUID); err != nil {
		return err
	}
	if err := c.do(ctx, http.MethodDelete, "/indexes/"+reindexUID, nil, &t); err != nil {
		return err
	}
	return c.wait(ctx, t.TaskUID)
}

// Stats: số document trong index (kiểm tra sau reindex).
func (c *Client) Stats(ctx context.Context) (int64, error) {
	var s struct {
		NumberOfDocuments int64 `json:"numberOfDocuments"`
	}
	err := c.do(ctx, http.MethodGet, "/indexes/"+Index+"/stats", nil, &s)
	return s.NumberOfDocuments, err
}

type searchRequest struct {
	Q        string   `json:"q"`
	Filter   []string `json:"filter,omitempty"`
	Facets   []string `json:"facets,omitempty"`
	Sort     []string `json:"sort,omitempty"`
	Offset   int      `json:"offset"`
	Limit    int      `json:"limit"`
	Retrieve []string `json:"attributesToRetrieve"`
}

type hit struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	SKU         string  `json:"sku"`
	Brand       string  `json:"brand"`
	Category    string  `json:"category"`
	Price       int64   `json:"price"`
	InStock     bool    `json:"in_stock"`
	Rating      float64 `json:"rating"`
	RatingCount int64   `json:"rating_count"`
	SoldCount   int64   `json:"sold_count"`
	ImageKey    string  `json:"image_key"`
	CreatedAt   int64   `json:"created_at"`
}

type searchResponse struct {
	Hits               []hit                     `json:"hits"`
	EstimatedTotalHits int                       `json:"estimatedTotalHits"`
	FacetDistribution  map[string]map[string]int `json:"facetDistribution"`
	ProcessingTimeMs   int                       `json:"processingTimeMs"`
}

var sorts = map[domain.SearchSort]string{
	domain.SearchPriceAsc:  "price:asc",
	domain.SearchPriceDesc: "price:desc",
	domain.SearchRating:    "rating:desc",
	domain.SearchBestSell:  "sold_count:desc",
	domain.SearchNewest:    "created_at:desc",
}

func (c *Client) Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, error) {
	req := searchRequest{Q: q.Q, Filter: buildFilter(q), Offset: (q.Page - 1) * q.Limit, Limit: q.Limit,
		Retrieve: []string{"id", "name", "sku", "brand", "category", "price", "in_stock", "rating", "rating_count",
			"sold_count", "image_key", "created_at"}}
	if s, ok := sorts[q.Sort]; ok {
		req.Sort = []string{s}
	}
	if q.Facets {
		req.Facets = facets
	}
	var res searchResponse
	if err := c.do(ctx, http.MethodPost, "/indexes/"+Index+"/search", req, &res); err != nil {
		return domain.SearchResult{}, err
	}
	out := domain.SearchResult{Hits: make([]domain.SearchHit, len(res.Hits)), Total: res.EstimatedTotalHits,
		Facets: res.FacetDistribution, Engine: "meilisearch", TookMs: res.ProcessingTimeMs}
	if out.Facets == nil {
		out.Facets = map[string]map[string]int{}
	}
	for i, h := range res.Hits {
		out.Hits[i] = domain.SearchHit{ID: h.ID, Name: h.Name, SKU: h.SKU, Brand: h.Brand, Category: h.Category, Price: h.Price,
			InStock: h.InStock, Rating: h.Rating, RatingCount: h.RatingCount, SoldCount: h.SoldCount, ImageKey: h.ImageKey,
			CreatedAt: time.Unix(h.CreatedAt, 0).UTC()}
	}
	return out, nil
}

// buildFilter: mỗi phần tử là 1 điều kiện AND; trong 1 phần tử, các giá trị nối OR. Giá trị chữ luôn được
// đặt trong ngoặc kép + escape → khách không chèn được cú pháp filter.
func buildFilter(q domain.SearchQuery) []string {
	var f []string
	if q.CategoryID != nil {
		f = append(f, "category_ids = "+strconv.FormatInt(*q.CategoryID, 10))
	}
	add := func(field string, values []string) {
		if len(values) == 0 {
			return
		}
		parts := make([]string, len(values))
		for i, v := range values {
			parts[i] = field + " = " + quote(v)
		}
		f = append(f, strings.Join(parts, " OR "))
	}
	add("brand", q.Brands)
	add("colors", q.Colors)
	add("sizes", q.Sizes)
	if q.MinPrice != nil {
		f = append(f, "price >= "+strconv.FormatInt(*q.MinPrice, 10))
	}
	if q.MaxPrice != nil {
		f = append(f, "price <= "+strconv.FormatInt(*q.MaxPrice, 10))
	}
	if q.InStock {
		f = append(f, "in_stock = true")
	}
	if q.MinRating != nil {
		f = append(f, "rating >= "+strconv.FormatFloat(*q.MinRating, 'f', 1, 64))
	}
	return f
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Health: Meilisearch còn sống không (dùng cho /readyz phụ và monitor).
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/health", nil, nil)
}
