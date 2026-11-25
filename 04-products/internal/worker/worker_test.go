package worker

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// Job panic không được làm sập process; lượt sau vẫn chạy.
func TestStart_PanicDoesNotKillWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runs atomic.Int64
	Start(ctx, discard, Job{Name: "p", Interval: 5 * time.Millisecond, Run: func(context.Context) error {
		if runs.Add(1) == 1 {
			panic("boom")
		}
		return nil
	}})
	deadline := time.Now().Add(2 * time.Second)
	for runs.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if runs.Load() < 3 {
		t.Fatalf("sau panic job chỉ chạy %d lần", runs.Load())
	}
}

// Job treo (vd chờ kết nối DB lúc quá tải) bị huỷ khi hết RunTimeout, lượt sau vẫn chạy.
func TestStart_HungJobIsCancelled(t *testing.T) {
	old := RunTimeout
	RunTimeout = 20 * time.Millisecond
	defer func() { RunTimeout = old }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runs atomic.Int64
	Start(ctx, discard, Job{Name: "h", Interval: 5 * time.Millisecond, Run: func(ctx context.Context) error {
		runs.Add(1)
		<-ctx.Done() // treo tới khi bị huỷ
		return ctx.Err()
	}})
	time.Sleep(300 * time.Millisecond)
	if runs.Load() < 2 {
		t.Fatalf("job treo chặn vòng lặp: chỉ chạy %d lần", runs.Load())
	}
}

func TestBackoff(t *testing.T) {
	for _, c := range []struct {
		interval time.Duration
		failed   int
		want     time.Duration
	}{
		{time.Second, 1, 2 * time.Second},
		{time.Second, 3, 8 * time.Second},
		{time.Second, 50, maxBackoff},           // không tràn số dù lỗi rất lâu
		{10 * time.Minute, 5, 10 * time.Minute}, // chu kỳ dài: giữ nguyên
	} {
		if got := backoff(c.interval, c.failed); got != c.want {
			t.Errorf("backoff(%v, %d) = %v, muốn %v", c.interval, c.failed, got, c.want)
		}
	}
}
