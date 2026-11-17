package link

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRepo viết SQL tay để bạn học SQL thật.
// Bài tập giai đoạn 3: chuyển sang sqlc (sinh code type-safe từ file .sql).
type PostgresRepo struct {
	pool *pgxpool.Pool
}

func NewPostgresRepo(pool *pgxpool.Pool) *PostgresRepo {
	return &PostgresRepo{pool: pool}
}

func (r *PostgresRepo) Create(ctx context.Context, l Link) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO links (code, url, created_at) VALUES ($1, $2, $3)`,
		l.Code, l.URL, l.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return ErrCodeTaken
	}
	return err
}

func (r *PostgresRepo) GetByCode(ctx context.Context, code string) (Link, error) {
	var l Link
	err := r.pool.QueryRow(ctx,
		`SELECT code, url, clicks, created_at FROM links WHERE code = $1`, code).
		Scan(&l.Code, &l.URL, &l.Clicks, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return l, err
}

func (r *PostgresRepo) IncrementClicks(ctx context.Context, code string, n int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE links SET clicks = clicks + $2 WHERE code = $1`, code, n)
	return err
}
