// Package worker chạy job nền định kỳ (dọn file rác, xoá token hết hạn...).
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"
)

type Job struct {
	Name     string
	Interval time.Duration
	Run      func(ctx context.Context) error
}

// RunTimeout: 1 lượt job chạy quá ngần này thì bị huỷ (ctx hết hạn → transaction rollback, lượt sau làm lại).
// Không có nó, lúc quá tải job chờ kết nối DB mãi → vòng lặp job đó trên instance này đứng im vĩnh viễn.
// Phải nhỏ hơn idle_in_transaction_session_timeout của Postgres (migrations/007).
var RunTimeout = time.Minute

// maxBackoff: job lỗi liên tiếp thì giãn dần (gấp đôi) tới ngần này — DB/search đang sập hay quá tải thì
// 4 instance không dội thêm tải + log mỗi giây. Job chu kỳ dài hơn thì giữ nguyên chu kỳ.
const maxBackoff = 30 * time.Second

// Start chạy mỗi job trong 1 goroutine tới khi ctx bị huỷ. Lệch ngẫu nhiên lúc bắt đầu để N instance
// không cùng lúc đập vào DB. Job phải an toàn khi nhiều instance chạy song song: chạy trên MỌI instance nên
// 1 máy sập thì instance còn lại vẫn làm (khoá advisory/OncePer chọn 1 instance mỗi chu kỳ).
func Start(ctx context.Context, log *slog.Logger, jobs ...Job) {
	for _, j := range jobs {
		go func() {
			delay := time.Duration(rand.Int64N(int64(j.Interval)))
			failed := 0
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}
				err := runOnce(ctx, j)
				if ctx.Err() != nil {
					return
				}
				delay = j.Interval
				if err == nil {
					if failed > 0 {
						log.InfoContext(ctx, "job recovered", "job", j.Name, "after_failures", failed)
					}
					failed = 0
					continue
				}
				failed++
				delay = backoff(j.Interval, failed)
				if failed == 1 || failed%10 == 0 { // lỗi kéo dài: log lần đầu rồi 10 lần/lần, không ngập log
					log.ErrorContext(ctx, "job failed", "job", j.Name, "err", err, "failures", failed, "retry_in", delay.String())
				}
			}
		}()
	}
}

// runOnce chạy 1 lượt có hạn chót; job panic thì thành lỗi thường thay vì làm sập cả process API.
func runOnce(ctx context.Context, j Job) (err error) {
	ctx, cancel := context.WithTimeout(ctx, RunTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return j.Run(ctx)
}

func backoff(interval time.Duration, failed int) time.Duration {
	if interval >= maxBackoff {
		return interval
	}
	d := interval << min(failed, 20)
	return min(d, maxBackoff)
}
