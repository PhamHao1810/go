package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"golearn/04-products/internal/domain"
)

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

// JOIN sẵn key của avatar để tầng HTTP ghép URL, không cần query thêm.
const userSelect = `SELECT u.id, u.email, u.password_hash, u.name, u.role, u.avatar_asset_id,
	COALESCE(a.key, '') AS avatar_key, u.created_at, u.updated_at
	FROM users u LEFT JOIN assets a ON a.id = u.avatar_asset_id`

func (r *UserRepo) Create(ctx context.Context, u domain.User) (domain.User, error) {
	out, err := queryOne[domain.User](ctx, db(ctx, r.pool), `INSERT INTO users (email, password_hash, name, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, email, password_hash, name, role, avatar_asset_id, created_at, updated_at`,
		u.Email, u.PasswordHash, u.Name, u.Role)
	if pgCode(err) == uniqueViolation {
		return domain.User{}, domain.ErrEmailTaken
	}
	return out, err
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (domain.User, error) {
	return r.one(ctx, userSelect+` WHERE u.email = $1`, email)
}

func (r *UserRepo) GetByID(ctx context.Context, id int64) (domain.User, error) {
	return r.one(ctx, userSelect+` WHERE u.id = $1`, id)
}

// SetAvatar: avatar cũ (nếu có) không còn ai dùng → job dọn rác xoá file.
func (r *UserRepo) SetAvatar(ctx context.Context, userID, assetID int64) error {
	tag, err := db(ctx, r.pool).Exec(ctx,
		`UPDATE users SET avatar_asset_id = $2, updated_at = now() WHERE id = $1`, userID, assetID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return err
}

func (r *UserRepo) one(ctx context.Context, sql string, args ...any) (domain.User, error) {
	u, err := queryOne[domain.User](ctx, db(ctx, r.pool), sql, args...)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrUserNotFound
	}
	return u, err
}

// ----- Refresh token -----

type RefreshTokenRepo struct {
	pool *pgxpool.Pool
}

func NewRefreshTokenRepo(pool *pgxpool.Pool) *RefreshTokenRepo { return &RefreshTokenRepo{pool: pool} }

func (r *RefreshTokenRepo) Insert(ctx context.Context, userID int64, hash []byte, expiresAt time.Time) error {
	_, err := db(ctx, r.pool).Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`, userID, hash, expiresAt)
	return err
}

// Consume đánh dấu token đã dùng trong 1 câu UPDATE nguyên tử: 2 request refresh cùng lúc bằng 1 token
// thì đúng 1 request thành công.
func (r *RefreshTokenRepo) Consume(ctx context.Context, hash []byte) (int64, error) {
	var userID int64
	err := db(ctx, r.pool).QueryRow(ctx, `UPDATE refresh_tokens SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now() RETURNING user_id`, hash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrInvalidRefreshToken
	}
	return userID, err
}

// RevokedOwner: token này có tồn tại nhưng ĐÃ bị dùng/thu hồi không? (dấu hiệu token bị đánh cắp)
func (r *RefreshTokenRepo) RevokedOwner(ctx context.Context, hash []byte) (int64, bool, error) {
	var userID int64
	err := db(ctx, r.pool).QueryRow(ctx,
		`SELECT user_id FROM refresh_tokens WHERE token_hash = $1 AND revoked_at IS NOT NULL`, hash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return userID, err == nil, err
}

func (r *RefreshTokenRepo) Revoke(ctx context.Context, hash []byte) error {
	_, err := db(ctx, r.pool).Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, hash)
	return err
}

func (r *RefreshTokenRepo) RevokeAll(ctx context.Context, userID int64) error {
	_, err := db(ctx, r.pool).Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

func (r *RefreshTokenRepo) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	tag, err := db(ctx, r.pool).Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at < $1`, before)
	return tag.RowsAffected(), err
}
