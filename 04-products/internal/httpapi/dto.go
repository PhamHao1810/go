package httpapi

import (
	"time"

	"golearn/04-products/internal/auth"
	"golearn/04-products/internal/domain"
)

// DTO: hình dạng JSON của API, tách khỏi domain để đổi DB/domain không làm vỡ hợp đồng với client.
// Không bao giờ trả domain.User trực tiếp (có PasswordHash).

// ----- Product -----

type createProductRequest struct {
	SKU         string `json:"sku"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       int64  `json:"price"`
	Stock       int64  `json:"stock"`
}

func (r createProductRequest) toDomain() domain.Product {
	return domain.Product{SKU: r.SKU, Name: r.Name, Description: r.Description, Price: r.Price, Stock: r.Stock}
}

type updateProductRequest struct {
	createProductRequest
	Version int64 `json:"version"` // version client đọc được lần cuối (optimistic locking)
}

type purchaseRequest struct {
	Quantity int64 `json:"quantity"`
}

type assetRefRequest struct {
	AssetID int64 `json:"asset_id"`
}

type imageResponse struct {
	AssetID  int64  `json:"asset_id"`
	URL      string `json:"url"`
	Position int    `json:"position"`
}

type productResponse struct {
	ID          int64           `json:"id"`
	SKU         string          `json:"sku"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Price       int64           `json:"price"`
	Stock       int64           `json:"stock"`
	Version     int64           `json:"version"`
	Images      []imageResponse `json:"images"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// URLFunc biến key trong storage thành URL công khai (CDN/bucket).
type URLFunc func(key string) string

func toProductResponse(p domain.Product, url URLFunc) productResponse {
	images := make([]imageResponse, len(p.Images))
	for i, img := range p.Images {
		images[i] = imageResponse{AssetID: img.AssetID, URL: url(img.Key), Position: img.Position}
	}
	return productResponse{ID: p.ID, SKU: p.SKU, Name: p.Name, Description: p.Description, Price: p.Price,
		Stock: p.Stock, Version: p.Version, Images: images, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

type listMeta struct {
	Limit      int     `json:"limit"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"` // null khi hết dữ liệu
}

// ----- Auth / user -----

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"` // giây
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
}

func toTokenResponse(t auth.Tokens, now time.Time) tokenResponse {
	return tokenResponse{AccessToken: t.Access, TokenType: "Bearer", ExpiresIn: int64(t.AccessExp.Sub(now).Seconds()),
		RefreshToken: t.Refresh, RefreshExpiresIn: int64(t.RefreshExp.Sub(now).Seconds())}
}

type userResponse struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	AvatarURL *string   `json:"avatar_url"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserResponse(u domain.User, url URLFunc) userResponse {
	res := userResponse{ID: u.ID, Email: u.Email, Name: u.Name, Role: string(u.Role), CreatedAt: u.CreatedAt}
	if u.AvatarKey != "" {
		s := url(u.AvatarKey)
		res.AvatarURL = &s
	}
	return res
}

// ----- Asset -----

type uploadRequest struct {
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

type uploadResponse struct {
	AssetID   int64             `json:"asset_id"`
	UploadURL string            `json:"upload_url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}
