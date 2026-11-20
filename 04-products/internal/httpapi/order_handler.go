package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"golearn/04-products/internal/domain"
	"golearn/04-products/internal/httpapi/response"
)

// ----- Đơn hàng -----

type OrderService interface {
	Place(ctx context.Context, actor domain.Principal, lines []domain.OrderLine) (domain.Order, error)
	Get(ctx context.Context, actor domain.Principal, id int64) (domain.Order, error)
	List(ctx context.Context, actor domain.Principal, cursor int64, limit int) (domain.OrderPage, error)
	Cancel(ctx context.Context, actor domain.Principal, id int64) (domain.Order, error)
	SetStatus(ctx context.Context, id int64, to domain.OrderStatus) (domain.Order, error)
}

type OrderHandler struct{ svc OrderService }

func NewOrderHandler(svc OrderService) *OrderHandler { return &OrderHandler{svc: svc} }

func (h *OrderHandler) Register(mux *http.ServeMux, g Guards) {
	handle(mux, "POST /api/v1/orders", h.place, g.User)
	handle(mux, "GET /api/v1/orders", h.list, g.User)
	handle(mux, "GET /api/v1/orders/{id}", h.get, g.User)
	handle(mux, "POST /api/v1/orders/{id}/cancel", h.cancel, g.User)
	handle(mux, "PUT /api/v1/orders/{id}/status", h.setStatus, g.Admin)
}

func (h *OrderHandler) place(w http.ResponseWriter, r *http.Request) {
	var req placeOrderRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	lines := make([]domain.OrderLine, len(req.Items))
	for i, it := range req.Items {
		lines[i] = domain.OrderLine{VariantID: it.VariantID, Qty: it.Quantity}
	}
	o, err := h.svc.Place(r.Context(), actor(r), lines)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/orders/"+strconv.FormatInt(o.ID, 10))
	response.OK(w, http.StatusCreated, toOrderResponse(o))
}

func (h *OrderHandler) list(w http.ResponseWriter, r *http.Request) {
	cursor, limit, err := parseCursor(r.URL.Query())
	if err != nil {
		response.Error(w, r, err)
		return
	}
	page, err := h.svc.List(r.Context(), actor(r), cursor, limit)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	items := make([]orderResponse, len(page.Items))
	for i, o := range page.Items {
		items[i] = toOrderResponse(o)
	}
	response.List(w, items, newCursorMeta(orDefault(limit), page.NextCursor))
}

func (h *OrderHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	o, err := h.svc.Get(r.Context(), actor(r), id)
	h.order(w, r, o, err)
}

func (h *OrderHandler) cancel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	o, err := h.svc.Cancel(r.Context(), actor(r), id)
	h.order(w, r, o, err)
}

func (h *OrderHandler) setStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var req orderStatusRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	o, err := h.svc.SetStatus(r.Context(), id, domain.OrderStatus(req.Status))
	h.order(w, r, o, err)
}

func (h *OrderHandler) order(w http.ResponseWriter, r *http.Request, o domain.Order, err error) {
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusOK, toOrderResponse(o))
}

// ----- Đánh giá -----

type ReviewService interface {
	Create(ctx context.Context, actor domain.Principal, r domain.Review) (domain.Review, error)
	List(ctx context.Context, productID, cursor int64, limit int) (domain.ReviewPage, error)
}

type ReviewHandler struct{ svc ReviewService }

func NewReviewHandler(svc ReviewService) *ReviewHandler { return &ReviewHandler{svc: svc} }

func (h *ReviewHandler) Register(mux *http.ServeMux, g Guards) {
	handle(mux, "GET /api/v1/products/{id}/reviews", h.list)
	handle(mux, "POST /api/v1/products/{id}/reviews", h.create, g.User)
}

func (h *ReviewHandler) create(w http.ResponseWriter, r *http.Request) {
	pid, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var req reviewRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	rv, err := h.svc.Create(r.Context(), actor(r), domain.Review{ProductID: pid, Rating: req.Rating, Content: req.Content})
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusCreated, toReviewResponse(rv))
}

func (h *ReviewHandler) list(w http.ResponseWriter, r *http.Request) {
	pid, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	cursor, limit, err := parseCursor(r.URL.Query())
	if err != nil {
		response.Error(w, r, err)
		return
	}
	page, err := h.svc.List(r.Context(), pid, cursor, limit)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	items := make([]reviewResponse, len(page.Items))
	for i, rv := range page.Items {
		items[i] = toReviewResponse(rv)
	}
	response.List(w, items, newCursorMeta(orDefault(limit), page.NextCursor))
}

// parseCursor: ?cursor=<id>&limit=<n> cho các danh sách phân trang theo id. limit=0 nghĩa là dùng mặc định.
func parseCursor(q url.Values) (int64, int, error) {
	var v domain.Validator
	var cursor int64
	limit := 0
	if n := optionalInt(q, "cursor", &v); n != nil {
		cursor = *n
	}
	if n := optionalInt(q, "limit", &v); n != nil {
		limit = int(*n)
		v.Check(*n >= 1, "limit", "must be between 1 and 100")
	}
	return cursor, limit, v.Err()
}

// orDefault: limit client không gửi (0) thì service dùng 20; meta báo đúng con số đó.
func orDefault(limit int) int {
	if limit == 0 {
		return 20
	}
	return limit
}
