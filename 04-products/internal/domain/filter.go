package domain

import (
	"strings"
	"unicode/utf8"
)

type Sort string

const (
	SortNewest    Sort = "-created_at"
	SortOldest    Sort = "created_at"
	SortPriceAsc  Sort = "price"
	SortPriceDesc Sort = "-price"
	SortRelevance Sort = "relevance" // chỉ dùng khi có q
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// ListFilter: lọc + sắp xếp + phân trang cho danh sách sản phẩm. Cursor là chuỗi mờ (opaque) do server cấp.
type ListFilter struct {
	Query      string
	CategoryID *int64 // gồm cả danh mục con
	BrandID    *int64
	MinPrice   *int64
	MaxPrice   *int64
	InStock    bool
	Sort       Sort
	Limit      int
	Cursor     string
}

type Page struct {
	Items      []Product
	NextCursor string // rỗng = hết dữ liệu
}

// Normalize điền giá trị mặc định rồi kiểm tra filter.
func (f *ListFilter) Normalize() error {
	f.Query = strings.TrimSpace(f.Query)
	if f.Sort == "" {
		f.Sort = SortNewest
		if f.Query != "" {
			f.Sort = SortRelevance
		}
	}
	if f.Limit == 0 {
		f.Limit = DefaultLimit
	}

	var v Validator
	v.Check(utf8.RuneCountInString(f.Query) <= 100, "q", "max 100 characters")
	v.Check(f.Limit >= 1 && f.Limit <= MaxLimit, "limit", "must be between 1 and 100")
	switch f.Sort {
	case SortNewest, SortOldest, SortPriceAsc, SortPriceDesc:
	case SortRelevance:
		v.Check(f.Query != "", "sort", "relevance requires q")
	default:
		v.Check(false, "sort", "must be one of: -created_at, created_at, price, -price, relevance")
	}
	v.Check(f.MinPrice == nil || *f.MinPrice >= 0, "min_price", "must be >= 0")
	v.Check(f.MinPrice == nil || f.MaxPrice == nil || *f.MinPrice <= *f.MaxPrice, "max_price", "must be >= min_price")
	v.Check(f.CategoryID == nil || *f.CategoryID > 0, "category_id", "must be a positive integer")
	v.Check(f.BrandID == nil || *f.BrandID > 0, "brand_id", "must be a positive integer")
	return v.Err()
}
