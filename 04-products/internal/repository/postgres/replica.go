package postgres

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
)

// maxReplicaLag: standby chậm hơn primary quá ngần này (byte WAL, ~vài giây ghi nặng) thì thôi đọc từ standby.
const maxReplicaLag = 16 << 20

// ReadRouter chọn nơi đọc cho truy vấn chịu được dữ liệu trễ vài giây (search, danh sách sản phẩm):
// standby nếu nó đang theo kịp primary, ngược lại primary. Mặc định đọc primary cho tới lần kiểm tra đầu tiên đạt,
// nên standby chết, tụt lại hay replication bị đứt đều không làm khách thấy dữ liệu cũ.
type ReadRouter struct {
	primary, replica *pgxpool.Pool
	log              *slog.Logger
	ok               atomic.Bool
}

func NewReadRouter(primary, replica *pgxpool.Pool, log *slog.Logger) *ReadRouter {
	return &ReadRouter{primary: primary, replica: replica, log: log}
}

func (rr *ReadRouter) Pool() *pgxpool.Pool {
	if rr.ok.Load() {
		return rr.replica
	}
	return rr.primary
}

// MarkBehind: vừa đọc standby lỗi → chuyển sang primary ngay, lần Check sau tự bật lại nếu standby ổn.
func (rr *ReadRouter) MarkBehind() { rr.set(false, "standby read failed") }

// Check chạy định kỳ (job 5s/lần ở mọi instance): so vị trí WAL của primary với vị trí standby đã áp dụng.
// Phải hỏi pg_is_in_recovery(): primary từng crash-recovery cũng có pg_last_wal_replay_lsn() khác NULL.
func (rr *ReadRouter) Check(ctx context.Context) error {
	var lsn string
	err := rr.primary.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&lsn)
	var lag *int64
	if err == nil {
		err = rr.replica.QueryRow(ctx, `SELECT CASE WHEN pg_is_in_recovery()
			THEN pg_wal_lsn_diff($1::pg_lsn, pg_last_wal_replay_lsn())::bigint END`, lsn).Scan(&lag)
	}
	ok := err == nil && lag != nil && *lag < maxReplicaLag
	reason := "standby caught up"
	switch {
	case err != nil:
		reason = err.Error()
	case lag == nil:
		reason = "read database is not a standby"
	case !ok:
		reason = "standby lagging"
	}
	rr.set(ok, reason)
	return nil // lỗi đã ghi log khi đổi trạng thái; không trả lỗi để khỏi log mỗi 5 giây
}

func (rr *ReadRouter) set(ok bool, reason string) {
	if rr.ok.Swap(ok) != ok {
		if ok {
			rr.log.Info("reads go to standby", "reason", reason)
		} else {
			rr.log.Warn("reads go to primary", "reason", reason)
		}
	}
}
