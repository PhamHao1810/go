package domain

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

type SearchSort string

const (
	SearchRelevance SearchSort = ""         // mặc định: độ khớp, rồi bán chạy
	SearchPriceAsc  SearchSort = "price"    // giá tăng
	SearchPriceDesc SearchSort = "-price"   // giá giảm
	SearchRating    SearchSort = "-rating"  // đánh giá cao trước
	SearchBestSell  SearchSort = "-sold"    // bán chạy trước
	SearchNewest    SearchSort = "-created" // mới nhất
)

// SearchQuery: tìm kiếm + lọc + facet cho trang tìm kiếm của khách.
type SearchQuery struct {
	Q          string
	CategoryID *int64 // gồm cả danh mục con
	Brands     []string
	Colors     []string
	Sizes      []string
	MinPrice   *int64
	MaxPrice   *int64
	InStock    bool
	MinRating  *float64
	Sort       SearchSort
	Facets     bool // tính số lượng theo hãng/danh mục/màu/size: tốn gấp 2-5 lần, chỉ bật khi giao diện cần (trang đầu)
	Page       int  // từ 1
	Limit      int
}

const (
	SearchMaxLimit = 50
	SearchMaxHits  = 1000 // Meilisearch maxTotalHits: không cho lật quá trang 1000/limit
)

func (q *SearchQuery) Normalize() error {
	q.Q = strings.TrimSpace(q.Q)
	if q.Page == 0 {
		q.Page = 1
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	var v Validator
	v.Check(utf8.RuneCountInString(q.Q) <= 100, "q", "max 100 characters")
	v.Check(q.Limit >= 1 && q.Limit <= SearchMaxLimit, "limit", "must be between 1 and 50")
	v.Check(q.Page >= 1 && q.Limit >= 1 && q.Page <= SearchMaxHits/q.Limit, "page", "page * limit must be <= 1000")
	v.Check(q.MinPrice == nil || *q.MinPrice >= 0, "min_price", "must be >= 0")
	v.Check(q.MaxPrice == nil || *q.MaxPrice >= 0, "max_price", "must be >= 0")
	v.Check(q.MinPrice == nil || q.MaxPrice == nil || *q.MinPrice <= *q.MaxPrice, "min_price", "must be <= max_price")
	v.Check(q.MinRating == nil || (*q.MinRating >= 0 && *q.MinRating <= 5), "min_rating", "must be between 0 and 5")
	v.Check(q.CategoryID == nil || *q.CategoryID > 0, "category_id", "must be a positive integer")
	v.Check(len(q.Brands) <= 20 && len(q.Colors) <= 20 && len(q.Sizes) <= 20, "brand", "at most 20 values per filter")
	switch q.Sort {
	case SearchRelevance, SearchPriceAsc, SearchPriceDesc, SearchRating, SearchBestSell, SearchNewest:
	default:
		v.Check(false, "sort", "must be one of: price, -price, -rating, -sold, -created")
	}
	return v.Err()
}

// SearchHit: 1 sản phẩm trong kết quả tìm kiếm (đủ để vẽ thẻ sản phẩm, không cần gọi thêm API).
type SearchHit struct {
	ID          int64
	Name        string
	SKU         string
	Brand       string
	Category    string
	Price       int64
	InStock     bool
	Rating      float64
	RatingCount int64
	SoldCount   int64
	ImageKey    string
	CreatedAt   time.Time
}

type SearchResult struct {
	Hits   []SearchHit
	Total  int                       // ước lượng (Meilisearch), tối đa SearchMaxHits
	Facets map[string]map[string]int // vd {"brand": {"Samsung": 120}, "color": {...}}
	Engine string                    // "meilisearch" hoặc "postgres" (dự phòng khi Meilisearch lỗi)
	TookMs int
}

// SearchDoc: document đẩy lên search engine, ghép từ products + các bảng liên quan.
type SearchDoc struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	NameASCII   string    `json:"name_ascii"` // bỏ dấu: "dien thoai" vẫn tìm ra "điện thoại"
	SKU         string    `json:"sku"`
	Brand       string    `json:"brand"`
	Category    string    `json:"category"`     // tên danh mục lá
	CategoryIDs []int64   `json:"category_ids"` // id từ gốc xuống, để lọc theo danh mục cha
	Tags        []string  `json:"tags"`
	Attributes  []string  `json:"attributes"` // "Chất liệu: Cotton"
	Colors      []string  `json:"colors"`
	Sizes       []string  `json:"sizes"`
	Price       int64     `json:"price"`
	InStock     bool      `json:"in_stock"`
	Rating      float64   `json:"rating"`
	RatingCount int64     `json:"rating_count"`
	SoldCount   int64     `json:"sold_count"`
	ImageKey    string    `json:"image_key"`
	CreatedAt   int64     `json:"created_at"` // unix giây: Meilisearch chỉ sort được số/chuỗi
	CreatedTime time.Time `json:"-"`
	CategoryID  *int64    `json:"-"`
}

// FoldVietnamese bỏ dấu tiếng Việt và đổi đ→d: "Điện thoại" → "Dien thoai".
func FoldVietnamese(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r): // dấu thanh, dấu mũ... đã tách ra sau NFD
		case r == 'đ':
			b.WriteRune('d')
		case r == 'Đ':
			b.WriteRune('D')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

var (
	ErrSearchUnavailable = &Error{Kind: KindUnavailable, Code: "SEARCH_UNAVAILABLE", Message: "search temporarily unavailable"}
	ErrSearchBusy        = &Error{Kind: KindUnavailable, Code: "SEARCH_BUSY", Message: "search is overloaded, retry in a moment"}
)
