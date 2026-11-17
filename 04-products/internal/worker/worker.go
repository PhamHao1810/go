// Package worker chạy job nền định kỳ (dọn file rác, xoá token hết hạn...).
package worker

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"
)

type Job struct {
	Name     string
	Interval time.Duration
	Run      func(ctx context.Context) error
}

// Start chạy mỗi job trong 1 goroutine tới khi ctx bị huỷ. Lệch ngẫu nhiên lúc bắt đầu để N instance
// không cùng lúc đập vào DB. Job phải an toàn khi nhiều instance chạy song song.
func Start(ctx context.Context, log *slog.Logger, jobs ...Job) {
	for _, j := range jobs {
		go func() {
			delay := time.Duration(rand.Int64N(int64(j.Interval)))
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}
				if err := j.Run(ctx); err != nil && ctx.Err() == nil {
					log.ErrorContext(ctx, "job failed", "job", j.Name, "err", err)
				}
				delay = j.Interval
			}
		}()
	}
}
