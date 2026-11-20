package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

type Product struct {
	ID          int64
	SKU         string
	Name        string
	Description string
	// Price/Stock: giá thấp nhất và tổng tồn kho của các biến thể (DB tự tổng hợp). Khi tạo sản phẩm,
	// đây là giá/kho của biến thể mặc định.
	Price       int64 // VND, số nguyên để không sai số như float
	Stock       int64
	Version     int64 // optimistic locking
	CategoryID  *int64
	BrandID     *int64
	RatingSum   int64
	RatingCount int64
	SoldCount   int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Images      []Image // theo position tăng dần

	// Chỉ có khi xem chi tiết (Get), không có trong danh sách.
	Brand      *Brand
	Categories []Category // từ gốc xuống danh mục của sản phẩm
	Variants   []Variant
	Attributes []AttributeValue
	Tags       []string
}

// Rating trung bình (0 khi chưa có đánh giá), làm tròn 1 chữ số.
func (p Product) Rating() float64 {
	if p.RatingCount == 0 {
		return 0
	}
	return float64(p.RatingSum*10/p.RatingCount) / 10
}

const MaxProductImages = 10

func (p Product) Validate() error {
	var v Validator
	v.Check(p.SKU != "" && utf8.RuneCountInString(p.SKU) <= 64, "sku", "required, max 64 characters")
	v.Check(strings.TrimSpace(p.Name) != "" && utf8.RuneCountInString(p.Name) <= 200, "name", "required, max 200 characters")
	v.Check(utf8.RuneCountInString(p.Description) <= 5000, "description", "max 5000 characters")
	v.Check(p.Price >= 0, "price", "must be >= 0")
	v.Check(p.Stock >= 0, "stock", "must be >= 0")
	v.Check(p.CategoryID == nil || *p.CategoryID > 0, "category_id", "must be a positive integer")
	v.Check(p.BrandID == nil || *p.BrandID > 0, "brand_id", "must be a positive integer")
	return v.Err()
}

// Variant: 1 phiên bản cụ thể để mua (màu + size), có SKU, giá, tồn kho riêng.
type Variant struct {
	ID        int64
	ProductID int64
	SKU       string
	Color     string
	Size      string
	Price     int64
	Stock     int64
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (v Variant) Validate() error {
	var c Validator
	c.Check(v.SKU != "" && utf8.RuneCountInString(v.SKU) <= 64, "sku", "required, max 64 characters")
	c.Check(utf8.RuneCountInString(v.Color) <= 40, "color", "max 40 characters")
	c.Check(utf8.RuneCountInString(v.Size) <= 20, "size", "max 20 characters")
	c.Check(v.Price >= 0, "price", "must be >= 0")
	c.Check(v.Stock >= 0, "stock", "must be >= 0")
	return c.Err()
}

// AttributeValue: 1 cặp thuộc tính của sản phẩm, vd {Chất liệu, Cotton}.
type AttributeValue struct {
	Name  string
	Value string
}

const (
	MaxProductTags       = 20
	MaxProductAttributes = 30
)

func ValidateTags(tags []string) error {
	var v Validator
	v.Check(len(tags) <= MaxProductTags, "tags", "at most 20 tags")
	for _, t := range tags {
		n := utf8.RuneCountInString(strings.TrimSpace(t))
		v.Check(n >= 1 && n <= 50, "tags", "each tag must be 1-50 characters")
	}
	return v.Err()
}

func ValidateAttributes(attrs []AttributeValue) error {
	var v Validator
	v.Check(len(attrs) <= MaxProductAttributes, "attributes", "at most 30 attributes")
	for _, a := range attrs {
		n, m := utf8.RuneCountInString(strings.TrimSpace(a.Name)), utf8.RuneCountInString(strings.TrimSpace(a.Value))
		v.Check(n >= 1 && n <= 60 && m >= 1 && m <= 100, "attributes", "name 1-60 and value 1-100 characters")
	}
	return v.Err()
}

var (
	ErrVariantNotFound    = &Error{Kind: KindNotFound, Code: "VARIANT_NOT_FOUND", Message: "variant not found"}
	ErrVariantConflict    = &Error{Kind: KindConflict, Code: "VERSION_CONFLICT", Message: "variant was changed by someone else, reload and retry"}
	ErrLastVariant        = &Error{Kind: KindConflict, Code: "LAST_VARIANT", Message: "a product must keep at least one variant"}
	ErrProductHasVariants = Invalid("PRODUCT_HAS_VARIANTS", "product has several variants: order a specific variant via POST /api/v1/orders")
)
