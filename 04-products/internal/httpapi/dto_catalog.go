package httpapi

import (
	"time"

	"golearn/04-products/internal/domain"
)

// ----- Danh mục / hãng -----

type categoryRequest struct {
	ParentID *int64 `json:"parent_id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
}

type categoryResponse struct {
	ID       int64               `json:"id"`
	ParentID *int64              `json:"parent_id"`
	Name     string              `json:"name"`
	Slug     string              `json:"slug"`
	Depth    int                 `json:"depth"`
	Children []*categoryResponse `json:"children,omitempty"`
}

func toCategoryResponse(c domain.Category) *categoryResponse {
	return &categoryResponse{ID: c.ID, ParentID: c.ParentID, Name: c.Name, Slug: c.Slug, Depth: c.Depth}
}

func toCategoryTree(nodes []*domain.CategoryNode) []*categoryResponse {
	out := make([]*categoryResponse, len(nodes))
	for i, n := range nodes {
		out[i] = toCategoryResponse(n.Category)
		out[i].Children = toCategoryTree(n.Children)
	}
	return out
}

type brandRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type brandResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func toBrandResponse(b domain.Brand) brandResponse {
	return brandResponse{ID: b.ID, Name: b.Name, Slug: b.Slug}
}

// ----- Biến thể / tag / thuộc tính -----

type variantRequest struct {
	SKU     string `json:"sku"`
	Color   string `json:"color"`
	Size    string `json:"size"`
	Price   int64  `json:"price"`
	Stock   int64  `json:"stock"`
	Version int64  `json:"version"` // chỉ cần khi sửa
}

type variantResponse struct {
	ID      int64  `json:"id"`
	SKU     string `json:"sku"`
	Color   string `json:"color"`
	Size    string `json:"size"`
	Price   int64  `json:"price"`
	Stock   int64  `json:"stock"`
	Version int64  `json:"version"`
}

func toVariantResponse(v domain.Variant) variantResponse {
	return variantResponse{ID: v.ID, SKU: v.SKU, Color: v.Color, Size: v.Size, Price: v.Price, Stock: v.Stock, Version: v.Version}
}

type tagsRequest struct {
	Tags []string `json:"tags"`
}

type attribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type attributesRequest struct {
	Attributes []attribute `json:"attributes"`
}

type ratingResponse struct {
	Average float64 `json:"average"`
	Count   int64   `json:"count"`
}

type categoryRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// ----- Đơn hàng -----

type orderLineRequest struct {
	VariantID int64 `json:"variant_id"`
	Quantity  int64 `json:"quantity"`
}

type placeOrderRequest struct {
	Items []orderLineRequest `json:"items"`
}

type orderStatusRequest struct {
	Status string `json:"status"`
}

type orderItemResponse struct {
	VariantID *int64 `json:"variant_id"`
	ProductID *int64 `json:"product_id"`
	Name      string `json:"name"`
	Quantity  int64  `json:"quantity"`
	UnitPrice int64  `json:"unit_price"`
}

type orderResponse struct {
	ID        int64               `json:"id"`
	Status    string              `json:"status"`
	Total     int64               `json:"total"`
	Items     []orderItemResponse `json:"items"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

func toOrderResponse(o domain.Order) orderResponse {
	items := make([]orderItemResponse, len(o.Items))
	for i, it := range o.Items {
		items[i] = orderItemResponse{VariantID: it.VariantID, ProductID: it.ProductID, Name: it.Name, Quantity: it.Qty, UnitPrice: it.UnitPrice}
	}
	return orderResponse{ID: o.ID, Status: string(o.Status), Total: o.Total, Items: items, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt}
}

// cursorMeta: phân trang keyset theo id (đơn hàng, đánh giá).
type cursorMeta struct {
	Limit      int    `json:"limit"`
	HasMore    bool   `json:"has_more"`
	NextCursor *int64 `json:"next_cursor"`
}

func newCursorMeta(limit int, next int64) cursorMeta {
	m := cursorMeta{Limit: limit, HasMore: next != 0}
	if next != 0 {
		m.NextCursor = &next
	}
	return m
}

// ----- Đánh giá -----

type reviewRequest struct {
	Rating  int    `json:"rating"`
	Content string `json:"content"`
}

type reviewResponse struct {
	ID        int64     `json:"id"`
	UserName  string    `json:"user_name"`
	Rating    int       `json:"rating"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func toReviewResponse(r domain.Review) reviewResponse {
	return reviewResponse{ID: r.ID, UserName: r.UserName, Rating: r.Rating, Content: r.Content, CreatedAt: r.CreatedAt}
}

// ----- Tìm kiếm -----

type searchHitResponse struct {
	ID       int64          `json:"id"`
	Name     string         `json:"name"`
	SKU      string         `json:"sku"`
	Brand    string         `json:"brand"`
	Category string         `json:"category"`
	Price    int64          `json:"price"`
	InStock  bool           `json:"in_stock"`
	Rating   ratingResponse `json:"rating"`
	Sold     int64          `json:"sold_count"`
	ImageURL *string        `json:"image_url"`
}

type searchMeta struct {
	Total  int                       `json:"total"` // ước lượng, tối đa 1000
	Page   int                       `json:"page"`
	Limit  int                       `json:"limit"`
	Facets map[string]map[string]int `json:"facets"`
	Engine string                    `json:"engine"`
	TookMs int                       `json:"took_ms"`
}

func toSearchHit(h domain.SearchHit, url URLFunc) searchHitResponse {
	out := searchHitResponse{ID: h.ID, Name: h.Name, SKU: h.SKU, Brand: h.Brand, Category: h.Category, Price: h.Price,
		InStock: h.InStock, Rating: ratingResponse{Average: h.Rating, Count: h.RatingCount}, Sold: h.SoldCount}
	if h.ImageKey != "" {
		u := url(h.ImageKey)
		out.ImageURL = &u
	}
	return out
}
