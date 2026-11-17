package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"golearn/04-products/internal/domain"
)

const uploadURLTTL = 10 * time.Minute

// AssetService quản lý vòng đời file: cấp upload_url → client tự upload → Claim kiểm tra file → gắn vào bản ghi
// → hết ai dùng thì job dọn rác xoá. File KHÔNG đi qua API: không tốn băng thông/CPU của API khi upload.
type AssetService struct {
	repo  AssetRepository
	store ObjectStore
	tx    TxManager
	log   *slog.Logger
	now   func() time.Time
}

func NewAssetService(repo AssetRepository, store ObjectStore, tx TxManager, log *slog.Logger) *AssetService {
	return &AssetService{repo: repo, store: store, tx: tx, log: log, now: time.Now}
}

func (s *AssetService) CreateUpload(ctx context.Context, actor domain.Principal, req domain.UploadRequest) (domain.Upload, error) {
	if err := req.Validate(); err != nil {
		return domain.Upload{}, err
	}
	// Tên file ngẫu nhiên: không đoán được, không trùng, không dính tên file người dùng đặt.
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	key := "images/" + s.now().UTC().Format("2006/01") + "/" + hex.EncodeToString(b) + domain.AllowedImageTypes[req.ContentType]

	a, err := s.repo.Create(ctx, domain.Asset{Key: key, ContentType: req.ContentType, Size: req.Size, UploadedBy: actor.UserID})
	if err != nil {
		return domain.Upload{}, err
	}
	up, err := s.store.PresignUpload(ctx, key, req.ContentType, req.Size, uploadURLTTL)
	if err != nil {
		return domain.Upload{}, err
	}
	up.Asset, up.ExpiresAt = a, s.now().Add(uploadURLTTL)
	return up, nil
}

// Claim xác nhận file đã lên storage đúng như đã khai và thuộc về actor. Gọi nhiều lần không sao.
func (s *AssetService) Claim(ctx context.Context, actor domain.Principal, id int64) (domain.Asset, error) {
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.Asset{}, err
	}
	if a.UploadedBy != actor.UserID {
		return domain.Asset{}, domain.ErrAssetNotFound // không tiết lộ file của người khác có tồn tại
	}
	if a.Status == domain.AssetReady {
		return a, nil
	}

	size, contentType, found, err := s.store.Stat(ctx, a.Key)
	if err != nil {
		return domain.Asset{}, err
	}
	if !found {
		return domain.Asset{}, domain.ErrAssetNotUploaded
	}
	if size != a.Size || contentType != a.ContentType {
		return domain.Asset{}, domain.ErrAssetMismatch
	}
	// Không tin Content-Type client khai: đọc 512 byte đầu, nhận dạng định dạng thật (chặn HTML/script giả làm ảnh).
	head, err := s.store.ReadHead(ctx, a.Key, 512)
	if err != nil {
		return domain.Asset{}, err
	}
	if http.DetectContentType(head) != a.ContentType {
		return domain.Asset{}, domain.ErrAssetMismatch
	}

	if err := s.repo.MarkReady(ctx, a.ID); err != nil {
		return domain.Asset{}, err
	}
	a.Status = domain.AssetReady
	return a, nil
}

var errSkip = errors.New("skip")

// CollectGarbage xoá file rác (xem điều kiện trong repository). Chạy định kỳ ở MỌI instance vẫn an toàn:
// mỗi file được xoá trong 1 transaction riêng, xoá dòng DB trước, xoá file sau; xoá file lỗi → rollback, lần sau thử lại.
func (s *AssetService) CollectGarbage(ctx context.Context) error {
	ids, err := s.repo.GarbageIDs(ctx, 100)
	if err != nil {
		return err
	}
	removed := 0
	for _, id := range ids {
		err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
			key, ok, err := s.repo.DeleteGarbage(ctx, id)
			if err != nil {
				return err
			}
			if !ok {
				return errSkip
			}
			return s.store.Remove(ctx, key)
		})
		switch {
		case err == nil:
			removed++
		case !errors.Is(err, errSkip):
			s.log.WarnContext(ctx, "asset gc failed", "asset_id", id, "err", err)
		}
	}
	if removed > 0 {
		s.log.InfoContext(ctx, "asset gc", "removed", removed)
	}
	return nil
}
