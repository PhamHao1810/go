package service

import (
	"context"
	"time"

	"golearn/04-products/internal/domain"
)

// Các interface (port) mà service cần từ bên ngoài. Định nghĩa ở phía DÙNG, cài đặt ở repository/.
// Service chỉ biết những hàm này → thay Postgres/Redis/S3 bằng bản giả khi test.

// TxManager chạy nhiều thao tác (có thể trên nhiều bảng/repository) trong 1 transaction.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type ProductRepository interface {
	Create(ctx context.Context, p domain.Product) (domain.Product, error)
	Get(ctx context.Context, id int64) (domain.Product, error)
	List(ctx context.Context, f domain.ListFilter) (domain.Page, error)
	Update(ctx context.Context, p domain.Product) (domain.Product, error) // p.Version = version client đang giữ
	Delete(ctx context.Context, id int64) error
	Purchase(ctx context.Context, id, qty int64) (domain.Product, error)
	AddImage(ctx context.Context, productID, assetID int64) error
	RemoveImage(ctx context.Context, productID, assetID int64) error
}

type ProductCache interface {
	Get(ctx context.Context, id int64) (domain.Product, bool, error)
	Set(ctx context.Context, p domain.Product) error
	Delete(ctx context.Context, id int64) error
	// Kết quả search/danh sách theo bộ lọc, giữ rất ngắn (vài giây): không xoá khi sửa, tự hết hạn.
	GetList(ctx context.Context, key string) (domain.Page, bool, error)
	SetList(ctx context.Context, key string, p domain.Page) error
}

type UserRepository interface {
	Create(ctx context.Context, u domain.User) (domain.User, error)
	GetByEmail(ctx context.Context, email string) (domain.User, error)
	GetByID(ctx context.Context, id int64) (domain.User, error)
	SetAvatar(ctx context.Context, userID, assetID int64) error
}

type RefreshTokenRepository interface {
	Insert(ctx context.Context, userID int64, hash []byte, expiresAt time.Time) error
	Consume(ctx context.Context, hash []byte) (userID int64, err error)
	RevokedOwner(ctx context.Context, hash []byte) (userID int64, found bool, err error)
	Revoke(ctx context.Context, hash []byte) error
	RevokeAll(ctx context.Context, userID int64) error
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

type LoginLimiter interface {
	Blocked(ctx context.Context, key string) (bool, error)
	Fail(ctx context.Context, key string) error
	Reset(ctx context.Context, key string) error
}

type AssetRepository interface {
	Create(ctx context.Context, a domain.Asset) (domain.Asset, error)
	Get(ctx context.Context, id int64) (domain.Asset, error)
	MarkReady(ctx context.Context, id int64) error
	GarbageIDs(ctx context.Context, limit int) ([]int64, error)
	DeleteGarbage(ctx context.Context, id int64) (key string, ok bool, err error)
}

type ObjectStore interface {
	// PresignUpload trả Method/URL/Headers để client tự upload; service điền Asset, ExpiresAt.
	PresignUpload(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (domain.Upload, error)
	Stat(ctx context.Context, key string) (size int64, contentType string, found bool, err error)
	ReadHead(ctx context.Context, key string, n int64) ([]byte, error)
	Remove(ctx context.Context, key string) error
}

// AssetClaimer: service khác (product, user) dùng để "nhận" 1 file đã upload trước khi gắn vào bản ghi của mình.
type AssetClaimer interface {
	Claim(ctx context.Context, actor domain.Principal, assetID int64) (domain.Asset, error)
}
