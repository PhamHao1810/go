package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"golearn/04-products/internal/domain"
)

type AssetRepo struct {
	pool *pgxpool.Pool
}

func NewAssetRepo(pool *pgxpool.Pool) *AssetRepo { return &AssetRepo{pool: pool} }

const assetCols = `id, key, content_type, size, status, uploaded_by, created_at, updated_at`

func (r *AssetRepo) Create(ctx context.Context, a domain.Asset) (domain.Asset, error) {
	return queryOne[domain.Asset](ctx, db(ctx, r.pool), `INSERT INTO assets (key, content_type, size, uploaded_by)
		VALUES ($1, $2, $3, $4) RETURNING `+assetCols, a.Key, a.ContentType, a.Size, a.UploadedBy)
}

func (r *AssetRepo) Get(ctx context.Context, id int64) (domain.Asset, error) {
	a, err := queryOne[domain.Asset](ctx, db(ctx, r.pool), `SELECT `+assetCols+` FROM assets WHERE id = $1`, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Asset{}, domain.ErrAssetNotFound
	}
	return a, err
}

func (r *AssetRepo) MarkReady(ctx context.Context, id int64) error {
	_, err := db(ctx, r.pool).Exec(ctx,
		`UPDATE assets SET status = 'ready', updated_at = now() WHERE id = $1 AND status = 'pending'`, id)
	return err
}

// garbage: file rác =
//   - pending quá 24h (xin upload_url rồi bỏ đó), hoặc
//   - ready nhưng hơn 1h không ai dùng (ảnh bị gỡ khỏi sản phẩm, avatar cũ, sản phẩm đã xoá...).
//
// THÊM BẢNG MỚI CÓ CỘT TRỎ TỚI assets → thêm 1 dòng NOT EXISTS ở đây. Nếu quên, foreign key (không ON DELETE)
// vẫn chặn việc xoá, nên tệ nhất là không dọn được rác chứ không bao giờ xoá nhầm ảnh đang dùng.
const garbage = `(
	(status = 'pending' AND created_at < now() - interval '24 hours')
	OR (status = 'ready' AND updated_at < now() - interval '1 hour'
		AND NOT EXISTS (SELECT 1 FROM product_images pi WHERE pi.asset_id = assets.id)
		AND NOT EXISTS (SELECT 1 FROM users u WHERE u.avatar_asset_id = assets.id))
)`

func (r *AssetRepo) GarbageIDs(ctx context.Context, limit int) ([]int64, error) {
	rows, _ := db(ctx, r.pool).Query(ctx, `SELECT id FROM assets WHERE `+garbage+` ORDER BY id LIMIT $1`, limit)
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// DeleteGarbage xoá dòng nếu nó VẪN là rác ngay lúc xoá (kiểm tra lại trong cùng câu lệnh) và trả về key để xoá file.
// ok=false: không còn là rác / đã bị instance khác xoá.
func (r *AssetRepo) DeleteGarbage(ctx context.Context, id int64) (key string, ok bool, err error) {
	err = db(ctx, r.pool).QueryRow(ctx, `DELETE FROM assets WHERE id = $1 AND `+garbage+` RETURNING key`, id).Scan(&key)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	case pgCode(err) == foreignKeyViolation: // vừa có người gắn ảnh này vào đâu đó
		return "", false, nil
	}
	return key, err == nil, err
}
