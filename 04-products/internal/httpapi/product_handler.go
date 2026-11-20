package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"golearn/04-products/internal/domain"
	"golearn/04-products/internal/httpapi/response"
)

// ProductService là những gì handler cần; định nghĩa ở phía dùng để test handler bằng bản giả.
type ProductService interface {
	Create(ctx context.Context, p domain.Product) (domain.Product, error)
	Get(ctx context.Context, id int64) (domain.Product, error)
	List(ctx context.Context, f domain.ListFilter) (domain.Page, error)
	Update(ctx context.Context, p domain.Product) (domain.Product, error)
	Delete(ctx context.Context, id int64) error
	Purchase(ctx context.Context, actor domain.Principal, id, qty int64) (domain.Product, error)
	SetTags(ctx context.Context, id int64, tags []string) (domain.Product, error)
	SetAttributes(ctx context.Context, id int64, attrs []domain.AttributeValue) (domain.Product, error)
	AddImage(ctx context.Context, actor domain.Principal, productID, assetID int64) (domain.Product, error)
	RemoveImage(ctx context.Context, productID, assetID int64) (domain.Product, error)
}

// ProductHandler chỉ làm việc HTTP: đọc request → gọi service → ghi response. Không chứa logic nghiệp vụ.
type ProductHandler struct {
	svc ProductService
	url URLFunc
}

func NewProductHandler(svc ProductService, url URLFunc) *ProductHandler {
	return &ProductHandler{svc: svc, url: url}
}

// Register: quyền truy cập đọc được ngay tại đây — xem/tìm công khai, mua cần đăng nhập, sửa cần admin.
func (h *ProductHandler) Register(mux *http.ServeMux, g Guards) {
	handle(mux, "GET /api/v1/products", h.list)
	handle(mux, "GET /api/v1/products/{id}", h.get)
	handle(mux, "POST /api/v1/products/{id}/purchase", h.purchase, g.User)
	handle(mux, "POST /api/v1/products", h.create, g.Admin)
	handle(mux, "PUT /api/v1/products/{id}", h.update, g.Admin)
	handle(mux, "DELETE /api/v1/products/{id}", h.delete, g.Admin)
	handle(mux, "POST /api/v1/products/{id}/images", h.addImage, g.Admin)
	handle(mux, "DELETE /api/v1/products/{id}/images/{asset_id}", h.removeImage, g.Admin)
	handle(mux, "PUT /api/v1/products/{id}/tags", h.setTags, g.Admin)
	handle(mux, "PUT /api/v1/products/{id}/attributes", h.setAttributes, g.Admin)
}

func (h *ProductHandler) setTags(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var req tagsRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	p, err := h.svc.SetTags(r.Context(), id, req.Tags)
	h.product(w, r, p, err)
}

func (h *ProductHandler) setAttributes(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var req attributesRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	attrs := make([]domain.AttributeValue, len(req.Attributes))
	for i, a := range req.Attributes {
		attrs[i] = domain.AttributeValue{Name: a.Name, Value: a.Value}
	}
	p, err := h.svc.SetAttributes(r.Context(), id, attrs)
	h.product(w, r, p, err)
}

func (h *ProductHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createProductRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	p, err := h.svc.Create(r.Context(), req.toDomain())
	if err != nil {
		response.Error(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/products/"+strconv.FormatInt(p.ID, 10))
	response.OK(w, http.StatusCreated, toProductResponse(p, h.url))
}

func (h *ProductHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	p, err := h.svc.Get(r.Context(), id)
	h.product(w, r, p, err)
}

// GET /api/v1/products?q=&category_id=&brand_id=&min_price=&max_price=&in_stock=&sort=&limit=&cursor=
func (h *ProductHandler) list(w http.ResponseWriter, r *http.Request) {
	f, err := parseListFilter(r.URL.Query())
	if err != nil {
		response.Error(w, r, err)
		return
	}
	page, err := h.svc.List(r.Context(), f)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	items := make([]productResponse, len(page.Items))
	for i, p := range page.Items {
		items[i] = toProductResponse(p, h.url)
	}
	meta := listMeta{Limit: f.Limit, HasMore: page.NextCursor != ""}
	if meta.Limit == 0 {
		meta.Limit = domain.DefaultLimit
	}
	if meta.HasMore {
		meta.NextCursor = &page.NextCursor
	}
	response.List(w, items, meta)
}

func (h *ProductHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var req updateProductRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	p := req.toDomain()
	p.ID, p.Version = id, req.Version
	p, err = h.svc.Update(r.Context(), p)
	h.product(w, r, p, err)
}

func (h *ProductHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		response.Error(w, r, err)
		return
	}
	response.NoContent(w)
}

func (h *ProductHandler) purchase(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var req purchaseRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	p, err := h.svc.Purchase(r.Context(), actor(r), id, req.Quantity)
	h.product(w, r, p, err)
}

func (h *ProductHandler) addImage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var req assetRefRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	p, err := h.svc.AddImage(r.Context(), actor(r), id, req.AssetID)
	h.product(w, r, p, err)
}

func (h *ProductHandler) removeImage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	assetID, err := pathID(r, "asset_id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	p, err := h.svc.RemoveImage(r.Context(), id, assetID)
	h.product(w, r, p, err)
}

func (h *ProductHandler) product(w http.ResponseWriter, r *http.Request, p domain.Product, err error) {
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusOK, toProductResponse(p, h.url))
}

// parseListFilter chỉ chuyển kiểu query string; quy tắc (khoảng giá, limit, sort hợp lệ) nằm ở domain.
func parseListFilter(q url.Values) (domain.ListFilter, error) {
	var v domain.Validator
	f := domain.ListFilter{
		Query:      q.Get("q"),
		Sort:       domain.Sort(q.Get("sort")),
		Cursor:     q.Get("cursor"),
		MinPrice:   optionalInt(q, "min_price", &v),
		MaxPrice:   optionalInt(q, "max_price", &v),
		CategoryID: optionalInt(q, "category_id", &v),
		BrandID:    optionalInt(q, "brand_id", &v),
	}
	if n := optionalInt(q, "limit", &v); n != nil {
		f.Limit = int(*n)
		v.Check(*n >= 1, "limit", "must be between 1 and 100") // chặn limit=0 bị hiểu thành mặc định
	}
	if s := q.Get("in_stock"); s != "" {
		b, err := strconv.ParseBool(s)
		v.Check(err == nil, "in_stock", "must be true or false")
		f.InStock = b
	}
	return f, v.Err()
}

func optionalInt(q url.Values, key string, v *domain.Validator) *int64 {
	s := q.Get(key)
	if s == "" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	v.Check(err == nil, key, "must be an integer")
	return &n
}
