package httpapi

import (
	"context"
	"net/http"

	"golearn/04-products/internal/domain"
	"golearn/04-products/internal/httpapi/response"
)

type AssetService interface {
	CreateUpload(ctx context.Context, actor domain.Principal, req domain.UploadRequest) (domain.Upload, error)
}

type AssetHandler struct {
	svc AssetService
}

func NewAssetHandler(svc AssetService) *AssetHandler { return &AssetHandler{svc: svc} }

func (h *AssetHandler) Register(mux *http.ServeMux, g Guards) {
	handle(mux, "POST /api/v1/assets", h.createUpload, g.User)
}

// POST /api/v1/assets {"content_type":"image/jpeg","size":12345}
// → client PUT file lên upload_url (kèm headers) → gửi asset_id vào API gắn ảnh/avatar.
func (h *AssetHandler) createUpload(w http.ResponseWriter, r *http.Request) {
	var req uploadRequest
	if err := decode(w, r, &req); err != nil {
		response.Error(w, r, err)
		return
	}
	u, err := h.svc.CreateUpload(r.Context(), actor(r), domain.UploadRequest{ContentType: req.ContentType, Size: req.Size})
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.OK(w, http.StatusCreated, uploadResponse{AssetID: u.Asset.ID, UploadURL: u.URL, Method: u.Method,
		Headers: u.Headers, ExpiresAt: u.ExpiresAt})
}
