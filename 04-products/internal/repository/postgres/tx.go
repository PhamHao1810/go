package postgres

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier: thứ mà cả *pgxpool.Pool lẫn pgx.Tx đều có → repository viết 1 lần, chạy được cả trong lẫn ngoài transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type txKey struct{}

// TxManager: Unit of Work cho nhiều bảng/nhiều repository. Service gọi
//
//	tx.WithinTx(ctx, func(ctx context.Context) error { repoA.X(ctx); repoB.Y(ctx) })
//
// và mọi repository nhận ctx đó tự chạy trong CÙNG 1 transaction (lỗi ở đâu cũng rollback hết),
// mà service không cần biết pgx.Tx là gì.
type TxManager struct {
	pool *pgxpool.Pool
}

func NewTxManager(pool *pgxpool.Pool) *TxManager { return &TxManager{pool: pool} }

func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx) // đã ở trong transaction: dùng luôn transaction ngoài
	}
	return pgx.BeginFunc(ctx, m.pool, func(tx pgx.Tx) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// db trả transaction đang mở trong ctx (nếu có), không thì pool.
func db(ctx context.Context, pool *pgxpool.Pool) querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}

// inTx chạy fn trong transaction đang mở (nếu service đã mở bằng TxManager) hoặc mở transaction mới.
// Dùng cho repository cần nhiều câu lệnh phải cùng thành công/thất bại.
func inTx(ctx context.Context, pool *pgxpool.Pool, fn func(q querier) error) error {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(tx)
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return fn(tx) })
}

// queryOne/queryAll map cột → field theo TÊN (created_at → CreatedAt), field thừa (vd Images) được bỏ qua.
func queryOne[T any](ctx context.Context, q querier, sql string, args ...any) (T, error) {
	rows, _ := q.Query(ctx, sql, args...) // lỗi của Query được trả lại qua CollectExactlyOneRow
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByNameLax[T])
}

func queryAll[T any](ctx context.Context, q querier, sql string, args ...any) ([]T, error) {
	rows, _ := q.Query(ctx, sql, args...)
	return pgx.CollectRows(rows, pgx.RowToStructByNameLax[T])
}

// unavailable: không tới được DB (lỗi mạng), DB đang tắt/khởi động (57P..) hoặc lỗi kết nối (08...).
func unavailable(err error) bool {
	code := pgCode(err)
	return errors.As(err, new(net.Error)) || strings.HasPrefix(code, "57P") || strings.HasPrefix(code, "08")
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// pgConstraint: tên ràng buộc bị vi phạm (vd "products_category_id_fkey") để biết lỗi FK thuộc cột nào.
func pgConstraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

const (
	checkViolation      = "23514"
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)
