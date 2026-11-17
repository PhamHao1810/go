package link

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"golearn/03-shortener/internal/httpx"
)

// Handler tương đương Controller trong NestJS.
type Handler struct {
	svc     *Service
	baseURL string
	log     *slog.Logger
}

func NewHandler(svc *Service, baseURL string, log *slog.Logger) *Handler {
	return &Handler{svc: svc, baseURL: baseURL, log: log}
}

// Register khai báo route. Router chuẩn của Go (1.22+) hỗ trợ method và {param}.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/links", h.create)
	mux.HandleFunc("GET /api/links/{code}", h.stats)
	mux.HandleFunc("GET /{code}", h.redirect)
}

// DTO: struct tag `json:"..."` thay cho class-transformer.
type createRequest struct {
	URL string `json:"url"`
}

type createResponse struct {
	Code     string `json:"code"`
	ShortURL string `json:"short_url"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // giới hạn body 1MB
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid json body")
		return
	}
	l, err := h.svc.Shorten(r.Context(), req.URL)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, createResponse{Code: l.Code, ShortURL: h.baseURL + "/" + l.Code})
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	l, err := h.svc.Get(r.Context(), r.PathValue("code"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, l)
}

func (h *Handler) redirect(w http.ResponseWriter, r *http.Request) {
	target, err := h.svc.Resolve(r.Context(), r.PathValue("code"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// fail map lỗi nghiệp vụ sang HTTP status, giống Exception Filter của NestJS.
// Lỗi không lường trước thì log chi tiết nhưng chỉ trả "internal error" cho client.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrInvalidURL):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	default:
		h.log.ErrorContext(r.Context(), "request failed", "err", err, "path", r.URL.Path)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
