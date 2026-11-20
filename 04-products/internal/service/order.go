package service

import (
	"context"
	"log/slog"

	"golearn/04-products/internal/domain"
)

// VariantService: admin thêm/sửa/xoá biến thể (màu, size, giá, kho riêng).
type VariantService struct {
	repo  VariantRepository
	cache ProductCache
	log   *slog.Logger
}

func NewVariantService(repo VariantRepository, cache ProductCache, log *slog.Logger) *VariantService {
	return &VariantService{repo: repo, cache: cache, log: log}
}

func (s *VariantService) Create(ctx context.Context, v domain.Variant) (domain.Variant, error) {
	if err := v.Validate(); err != nil {
		return domain.Variant{}, err
	}
	out, err := s.repo.Create(ctx, v)
	if err == nil {
		invalidateProducts(ctx, s.cache, s.log, v.ProductID)
	}
	return out, err
}

func (s *VariantService) Update(ctx context.Context, v domain.Variant) (domain.Variant, error) {
	var c domain.Validator
	c.Check(v.Version >= 1, "version", "required: the version you last read")
	if err := c.Err(); err != nil {
		return domain.Variant{}, err
	}
	if err := v.Validate(); err != nil {
		return domain.Variant{}, err
	}
	out, err := s.repo.Update(ctx, v)
	if err == nil {
		invalidateProducts(ctx, s.cache, s.log, v.ProductID)
	}
	return out, err
}

func (s *VariantService) Delete(ctx context.Context, productID, variantID int64) error {
	err := s.repo.Delete(ctx, productID, variantID)
	if err == nil {
		invalidateProducts(ctx, s.cache, s.log, productID)
	}
	return err
}

// OrderService: đặt hàng nhiều món, xem đơn, huỷ đơn (trả kho), admin đổi trạng thái.
// Toàn bộ đảm bảo kho/trạng thái nằm trong transaction của OrderRepository, service chỉ kiểm tra quyền + đầu vào.
type OrderService struct {
	repo  OrderRepository
	cache ProductCache
	log   *slog.Logger
}

func NewOrderService(repo OrderRepository, cache ProductCache, log *slog.Logger) *OrderService {
	return &OrderService{repo: repo, cache: cache, log: log}
}

func (s *OrderService) Place(ctx context.Context, actor domain.Principal, lines []domain.OrderLine) (domain.Order, error) {
	if err := domain.ValidateOrderLines(lines); err != nil {
		return domain.Order{}, err
	}
	o, products, err := s.repo.Place(ctx, actor.UserID, lines)
	if err == nil {
		invalidateProducts(ctx, s.cache, s.log, products...)
	}
	return o, err
}

// Get: khách chỉ xem được đơn của mình (đơn người khác trả 404 như không tồn tại, không lộ là có đơn đó); admin xem mọi đơn.
func (s *OrderService) Get(ctx context.Context, actor domain.Principal, id int64) (domain.Order, error) {
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.Order{}, err
	}
	if actor.Role != domain.RoleAdmin && o.UserID != actor.UserID {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return o, nil
}

func (s *OrderService) List(ctx context.Context, actor domain.Principal, cursor int64, limit int) (domain.OrderPage, error) {
	if limit == 0 {
		limit = 20
	}
	var v domain.Validator
	v.Check(limit >= 1 && limit <= 100, "limit", "must be between 1 and 100")
	v.Check(cursor >= 0, "cursor", "must be >= 0")
	if err := v.Err(); err != nil {
		return domain.OrderPage{}, err
	}
	return s.repo.List(ctx, actor.UserID, cursor, limit)
}

// Cancel: khách huỷ đơn của mình khi chưa giao (pending/paid). Kho được trả lại trong cùng transaction.
func (s *OrderService) Cancel(ctx context.Context, actor domain.Principal, id int64) (domain.Order, error) {
	owner := actor.UserID
	if actor.Role == domain.RoleAdmin {
		owner = 0
	}
	return s.setStatus(ctx, id, owner, domain.OrderCancelled)
}

// SetStatus: admin đẩy đơn qua các bước paid → shipped → delivered (hoặc huỷ).
func (s *OrderService) SetStatus(ctx context.Context, id int64, to domain.OrderStatus) (domain.Order, error) {
	return s.setStatus(ctx, id, 0, to)
}

func (s *OrderService) setStatus(ctx context.Context, id, owner int64, to domain.OrderStatus) (domain.Order, error) {
	from := domain.AllowedFrom(to)
	if from == nil {
		return domain.Order{}, domain.ErrInvalidStatus
	}
	o, products, err := s.repo.SetStatus(ctx, id, owner, to, from)
	if err == nil {
		invalidateProducts(ctx, s.cache, s.log, products...)
	}
	return o, err
}

// ReviewService: đánh giá sản phẩm (chỉ người đã nhận hàng), xem đánh giá.
type ReviewService struct {
	repo  ReviewRepository
	cache ProductCache
	log   *slog.Logger
}

func NewReviewService(repo ReviewRepository, cache ProductCache, log *slog.Logger) *ReviewService {
	return &ReviewService{repo: repo, cache: cache, log: log}
}

func (s *ReviewService) Create(ctx context.Context, actor domain.Principal, r domain.Review) (domain.Review, error) {
	r.UserID = actor.UserID
	if err := r.Validate(); err != nil {
		return domain.Review{}, err
	}
	out, err := s.repo.Create(ctx, r)
	if err == nil {
		invalidateProducts(ctx, s.cache, s.log, r.ProductID) // điểm trung bình đổi
	}
	return out, err
}

func (s *ReviewService) List(ctx context.Context, productID, cursor int64, limit int) (domain.ReviewPage, error) {
	if limit == 0 {
		limit = 20
	}
	var v domain.Validator
	v.Check(limit >= 1 && limit <= 100, "limit", "must be between 1 and 100")
	v.Check(cursor >= 0, "cursor", "must be >= 0")
	if err := v.Err(); err != nil {
		return domain.ReviewPage{}, err
	}
	return s.repo.List(ctx, productID, cursor, limit)
}
