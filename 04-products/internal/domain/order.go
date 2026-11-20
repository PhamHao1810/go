package domain

import (
	"time"
	"unicode/utf8"
)

type OrderStatus string

const (
	OrderPending   OrderStatus = "pending"
	OrderPaid      OrderStatus = "paid"
	OrderShipped   OrderStatus = "shipped"
	OrderDelivered OrderStatus = "delivered"
	OrderCancelled OrderStatus = "cancelled"
)

// orderTransitions: trạng thái mới → các trạng thái được phép chuyển TỪ đó. Kiểm tra nằm trong câu UPDATE
// (WHERE status = ANY(...)) nên 2 request đổi trạng thái cùng lúc không thể cùng thành công sai thứ tự.
var orderTransitions = map[OrderStatus][]OrderStatus{
	OrderPaid:      {OrderPending},
	OrderShipped:   {OrderPaid},
	OrderDelivered: {OrderShipped},
	OrderCancelled: {OrderPending, OrderPaid},
}

// AllowedFrom: các trạng thái hiện tại được phép chuyển sang to; nil = to không hợp lệ.
func AllowedFrom(to OrderStatus) []OrderStatus { return orderTransitions[to] }

type Order struct {
	ID        int64
	UserID    int64
	Status    OrderStatus
	Total     int64
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
	Items     []OrderItem
}

type OrderItem struct {
	VariantID *int64 // nil nếu biến thể đã bị xoá sau khi mua
	ProductID *int64
	Name      string // tên + màu/size chốt lúc mua
	Qty       int64
	UnitPrice int64
}

// OrderLine: 1 dòng khách gửi lên khi đặt hàng.
type OrderLine struct {
	VariantID int64
	Qty       int64
}

const MaxOrderLines = 50

func ValidateOrderLines(lines []OrderLine) error {
	var v Validator
	v.Check(len(lines) >= 1 && len(lines) <= MaxOrderLines, "items", "must have 1-50 items")
	seen := make(map[int64]bool, len(lines))
	for _, l := range lines {
		v.Check(l.VariantID > 0, "items.variant_id", "must be a positive integer")
		v.Check(l.Qty >= 1 && l.Qty <= 1000, "items.quantity", "must be between 1 and 1000")
		v.Check(!seen[l.VariantID], "items.variant_id", "each variant at most once per order")
		seen[l.VariantID] = true
	}
	return v.Err()
}

// OrderPage: phân trang keyset theo id giảm dần.
type OrderPage struct {
	Items      []Order
	NextCursor int64 // 0 = hết
}

// ----- Đánh giá -----

type Review struct {
	ID        int64
	ProductID int64
	UserID    int64
	UserName  string
	Rating    int
	Content   string
	CreatedAt time.Time
}

func (r Review) Validate() error {
	var v Validator
	v.Check(r.Rating >= 1 && r.Rating <= 5, "rating", "must be between 1 and 5")
	v.Check(utf8.RuneCountInString(r.Content) <= 2000, "content", "max 2000 characters")
	return v.Err()
}

type ReviewPage struct {
	Items      []Review
	NextCursor int64
}

var (
	ErrOrderNotFound     = &Error{Kind: KindNotFound, Code: "ORDER_NOT_FOUND", Message: "order not found"}
	ErrInvalidTransition = &Error{Kind: KindConflict, Code: "INVALID_STATUS_TRANSITION", Message: "order cannot move to this status from its current status"}
	ErrInvalidStatus     = Invalid("INVALID_STATUS", "status must be one of: paid, shipped, delivered, cancelled")
	ErrReviewNotAllowed  = &Error{Kind: KindForbidden, Code: "REVIEW_NOT_ALLOWED", Message: "only customers who received this product can review it"}
	ErrAlreadyReviewed   = &Error{Kind: KindConflict, Code: "ALREADY_REVIEWED", Message: "you already reviewed this product"}
)
