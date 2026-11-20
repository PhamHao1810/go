package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"golearn/04-products/internal/domain"
	"golearn/04-products/internal/httpapi/response"
)

// ----- Danh mục + hãng -----

type CatalogService interface {
	CategoryTree(ctx context.Context) ([]*domain.CategoryNode, error)
	CreateCategory(ctx context.Context, c domain.Category) (domain.Category, error)
	UpdateCategory(ctx context.Context, c domain.Category) (domain.Category, error)
	DeleteCategory(ctx context.Context, id int64) error
	Brands(ctx context.Context) ([]domain.Brand, error)
	CreateBrand(ctx context.Context, b domain.Brand) (domain.Brand, error)
	UpdateBrand(ctx context.Context, b domain.Brand) (domain.Brand, error)
	DeleteBrand(ctx context.Context, id int64) error
}

type CatalogHandler struct{ svc CatalogService }

func NewCatalogHandler(svc CatalogService) *CatalogHandler { return &CatalogHandler{svc: svc} }

func (h *CatalogHandler) Register(mux *http.ServeMux, g Guards) {
	handle(mux, "GET /api/v1/categories", h.categories)
	handle(mux, "POST /api/v1/categories", h.createCategory, g.Admin)
	handle(mux, "PUT /api/v1/categories/{id}", h.updateCategory, g.Admin)
	handle(mux, "DELETE /api/v1/categories/{id}", h.deleteCategory, g.Admin)
	handle(mux, "GET /api/v1/brands", h.brands)
	handle(mux, "POST /api/v1/brands", h.createBrand, g.Admin)
	handle(mux, "PUT /api/v1/brands/{id}", h.updateBrand, g.Admin)
	handle(mux, "DELETE /api/v1/brands/{id}", h.deleteBrand, g.Admin)
}

func (h *CatalogHandler) categories(w http.ResponseWriter, r *http.Request) {
	tree, err := h.svc.CategoryTree(r.Context())
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusOK, toCategoryTree(tree))
}

func (h *CatalogHandler) createCategory(w http.ResponseWriter, r *http.Request) {
	var req categoryRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	c, err := h.svc.CreateCategory(r.Context(), domain.Category{ParentID: req.ParentID, Name: req.Name, Slug: req.Slug})
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusCreated, toCategoryResponse(c))
}

func (h *CatalogHandler) updateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var req categoryRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	c, err := h.svc.UpdateCategory(r.Context(), domain.Category{ID: id, Name: req.Name, Slug: req.Slug})
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusOK, toCategoryResponse(c))
}

func (h *CatalogHandler) deleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err == nil {
		err = h.svc.DeleteCategory(r.Context(), id)
	}
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.NoContent(w)
}

func (h *CatalogHandler) brands(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Brands(r.Context())
	if err != nil {
		response.Error(w, r, err)
		return
	}
	out := make([]brandResponse, len(list))
	for i, b := range list {
		out[i] = toBrandResponse(b)
	}
	response.OK(w, http.StatusOK, out)
}

func (h *CatalogHandler) createBrand(w http.ResponseWriter, r *http.Request) {
	var req brandRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	b, err := h.svc.CreateBrand(r.Context(), domain.Brand{Name: req.Name, Slug: req.Slug})
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusCreated, toBrandResponse(b))
}

func (h *CatalogHandler) updateBrand(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var req brandRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	b, err := h.svc.UpdateBrand(r.Context(), domain.Brand{ID: id, Name: req.Name, Slug: req.Slug})
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusOK, toBrandResponse(b))
}

func (h *CatalogHandler) deleteBrand(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err == nil {
		err = h.svc.DeleteBrand(r.Context(), id)
	}
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.NoContent(w)
}

// ----- Biến thể -----

type VariantService interface {
	Create(ctx context.Context, v domain.Variant) (domain.Variant, error)
	Update(ctx context.Context, v domain.Variant) (domain.Variant, error)
	Delete(ctx context.Context, productID, variantID int64) error
}

type VariantHandler struct{ svc VariantService }

func NewVariantHandler(svc VariantService) *VariantHandler { return &VariantHandler{svc: svc} }

func (h *VariantHandler) Register(mux *http.ServeMux, g Guards) {
	handle(mux, "POST /api/v1/products/{id}/variants", h.create, g.Admin)
	handle(mux, "PUT /api/v1/products/{id}/variants/{vid}", h.update, g.Admin)
	handle(mux, "DELETE /api/v1/products/{id}/variants/{vid}", h.delete, g.Admin)
}

func (h *VariantHandler) create(w http.ResponseWriter, r *http.Request) {
	pid, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var req variantRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	v, err := h.svc.Create(r.Context(), domain.Variant{ProductID: pid, SKU: req.SKU, Color: req.Color, Size: req.Size,
		Price: req.Price, Stock: req.Stock})
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusCreated, toVariantResponse(v))
}

func (h *VariantHandler) update(w http.ResponseWriter, r *http.Request) {
	pid, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	vid, err := pathID(r, "vid")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var req variantRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	v, err := h.svc.Update(r.Context(), domain.Variant{ID: vid, ProductID: pid, SKU: req.SKU, Color: req.Color, Size: req.Size,
		Price: req.Price, Stock: req.Stock, Version: req.Version})
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusOK, toVariantResponse(v))
}

func (h *VariantHandler) delete(w http.ResponseWriter, r *http.Request) {
	pid, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	vid, err := pathID(r, "vid")
	if err == nil {
		err = h.svc.Delete(r.Context(), pid, vid)
	}
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.NoContent(w)
}

// ----- Tìm kiếm -----

type SearchService interface {
	Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, error)
}

type SearchHandler struct {
	svc SearchService
	url URLFunc
}

func NewSearchHandler(svc SearchService, url URLFunc) *SearchHandler {
	return &SearchHandler{svc: svc, url: url}
}

func (h *SearchHandler) Register(mux *http.ServeMux, _ Guards) {
	handle(mux, "GET /api/v1/search", h.search)
}

// GET /api/v1/search?q=&category_id=&brand=&brand=&color=&size=&min_price=&max_price=&in_stock=&min_rating=&sort=&page=&limit=&facets=true
func (h *SearchHandler) search(w http.ResponseWriter, r *http.Request) {
	q, err := parseSearchQuery(r.URL.Query())
	if err == nil {
		err = q.Normalize() // điền page/limit mặc định để meta trả đúng con số đang dùng
	}
	if err != nil {
		response.Error(w, r, err)
		return
	}
	res, err := h.svc.Search(r.Context(), q)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	hits := make([]searchHitResponse, len(res.Hits))
	for i, hit := range res.Hits {
		hits[i] = toSearchHit(hit, h.url)
	}
	response.List(w, hits, searchMeta{Total: res.Total, Page: q.Page, Limit: q.Limit, Facets: res.Facets,
		Engine: res.Engine, TookMs: res.TookMs})
}

func parseSearchQuery(v url.Values) (domain.SearchQuery, error) {
	var c domain.Validator
	q := domain.SearchQuery{
		Q: v.Get("q"), Brands: v["brand"], Colors: v["color"], Sizes: v["size"], Sort: domain.SearchSort(v.Get("sort")),
		CategoryID: optionalInt(v, "category_id", &c), MinPrice: optionalInt(v, "min_price", &c),
		MaxPrice: optionalInt(v, "max_price", &c),
	}
	if n := optionalInt(v, "page", &c); n != nil {
		q.Page = int(*n)
		c.Check(*n >= 1, "page", "must be >= 1")
	}
	if n := optionalInt(v, "limit", &c); n != nil {
		q.Limit = int(*n)
		c.Check(*n >= 1, "limit", "must be between 1 and 50")
	}
	if s := v.Get("in_stock"); s != "" {
		b, err := strconv.ParseBool(s)
		c.Check(err == nil, "in_stock", "must be true or false")
		q.InStock = b
	}
	if s := v.Get("facets"); s != "" {
		b, err := strconv.ParseBool(s)
		c.Check(err == nil, "facets", "must be true or false")
		q.Facets = b
	}
	if s := v.Get("min_rating"); s != "" {
		f, err := strconv.ParseFloat(s, 64)
		c.Check(err == nil, "min_rating", "must be a number")
		q.MinRating = &f
	}
	return q, c.Err()
}
