package domain

import "time"

type AssetStatus string

const (
	AssetPending AssetStatus = "pending" // đã cấp upload_url, chưa xác nhận file
	AssetReady   AssetStatus = "ready"   // file đã kiểm tra đúng loại/kích thước, được phép gắn vào sản phẩm/avatar
)

const MaxAssetSize = 5 << 20 // 5MB

// AllowedImageTypes → đuôi file. Không cho SVG: SVG chứa được JavaScript (XSS).
var AllowedImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

type Asset struct {
	ID          int64
	Key         string
	ContentType string
	Size        int64
	Status      AssetStatus
	UploadedBy  int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Image: 1 ảnh đã gắn vào sản phẩm. URL do tầng HTTP ghép từ Key (đổi CDN không phải sửa DB).
type Image struct {
	AssetID  int64
	Key      string
	Position int
}

type UploadRequest struct {
	ContentType string
	Size        int64
}

func (r UploadRequest) Validate() error {
	var v Validator
	_, ok := AllowedImageTypes[r.ContentType]
	v.Check(ok, "content_type", "must be one of: image/jpeg, image/png, image/webp")
	v.Check(r.Size >= 1 && r.Size <= MaxAssetSize, "size", "must be between 1 byte and 5MB")
	return v.Err()
}

// Upload là thông tin client cần để tự đưa file thẳng lên storage (không đi qua API).
type Upload struct {
	Asset     Asset
	Method    string
	URL       string
	Headers   map[string]string // client PHẢI gửi đúng các header này
	ExpiresAt time.Time
}
