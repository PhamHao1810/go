package httpapi

import (
	"context"
	"net/http"
	"time"

	"golearn/04-products/internal/auth"
	"golearn/04-products/internal/domain"
	"golearn/04-products/internal/httpapi/response"
)

type AuthService interface {
	Register(ctx context.Context, r domain.Registration) (domain.User, error)
	Login(ctx context.Context, email, password string) (auth.Tokens, error)
	Refresh(ctx context.Context, refresh string) (auth.Tokens, error)
	Logout(ctx context.Context, refresh string) error
	Me(ctx context.Context, actor domain.Principal) (domain.User, error)
	SetAvatar(ctx context.Context, actor domain.Principal, assetID int64) (domain.User, error)
	VerifyAccess(token string) (domain.Principal, error)
}

type AuthHandler struct {
	svc AuthService
	url URLFunc
}

func NewAuthHandler(svc AuthService, url URLFunc) *AuthHandler {
	return &AuthHandler{svc: svc, url: url}
}

func (h *AuthHandler) Register(mux *http.ServeMux, g Guards) {
	handle(mux, "POST /api/v1/auth/register", h.register)
	handle(mux, "POST /api/v1/auth/login", h.login)
	handle(mux, "POST /api/v1/auth/refresh", h.refresh)
	handle(mux, "POST /api/v1/auth/logout", h.logout)
	handle(mux, "GET /api/v1/me", h.me, g.User)
	handle(mux, "PUT /api/v1/me/avatar", h.setAvatar, g.User)
}

func (h *AuthHandler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	u, err := h.svc.Register(r.Context(), domain.Registration{Email: req.Email, Password: req.Password, Name: req.Name})
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusCreated, toUserResponse(u, h.url))
}

func (h *AuthHandler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	t, err := h.svc.Login(r.Context(), req.Email, req.Password)
	h.tokens(w, r, t, err)
}

func (h *AuthHandler) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	t, err := h.svc.Refresh(r.Context(), req.RefreshToken)
	h.tokens(w, r, t, err)
}

func (h *AuthHandler) logout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	if err := h.svc.Logout(r.Context(), req.RefreshToken); err != nil {
		response.Error(w, r, err)
		return
	}
	response.NoContent(w)
}

func (h *AuthHandler) me(w http.ResponseWriter, r *http.Request) {
	u, err := h.svc.Me(r.Context(), actor(r))
	h.user(w, r, u, err)
}

func (h *AuthHandler) setAvatar(w http.ResponseWriter, r *http.Request) {
	var req assetRefRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	u, err := h.svc.SetAvatar(r.Context(), actor(r), req.AssetID)
	h.user(w, r, u, err)
}

func (h *AuthHandler) user(w http.ResponseWriter, r *http.Request, u domain.User, err error) {
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusOK, toUserResponse(u, h.url))
}

func (h *AuthHandler) tokens(w http.ResponseWriter, r *http.Request, t auth.Tokens, err error) {
	if err != nil {
		response.Error(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store") // không cho proxy/trình duyệt lưu token
	response.OK(w, http.StatusOK, toTokenResponse(t, time.Now()))
}
