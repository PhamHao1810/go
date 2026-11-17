package exercises

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestFirst_ReturnsFastestAndCancelsOthers(t *testing.T) {
	var cancelled atomic.Int32
	slow := func(ctx context.Context) (string, error) {
		select {
		case <-time.After(time.Second):
			return "slow", nil
		case <-ctx.Done():
			cancelled.Add(1)
			return "", ctx.Err()
		}
	}
	fast := func(ctx context.Context) (string, error) {
		time.Sleep(10 * time.Millisecond)
		return "fast", nil
	}

	got, err := First(context.Background(), slow, fast, slow)
	if err != nil || got != "fast" {
		t.Fatalf(`First() = (%q, %v), muốn ("fast", nil)`, got, err)
	}

	// cho các hàm chậm tối đa 200ms để nhận tín hiệu huỷ
	deadline := time.Now().Add(200 * time.Millisecond)
	for cancelled.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := cancelled.Load(); n != 2 {
		t.Errorf("chỉ %d/2 hàm chậm bị huỷ — nhớ cancel context sau khi có kết quả", n)
	}
}

func TestFirst_AllFail(t *testing.T) {
	boom := func(ctx context.Context) (string, error) { return "", errors.New("boom") }
	if _, err := First(context.Background(), boom, boom); err == nil {
		t.Fatal("muốn có lỗi khi tất cả đều thất bại")
	}
}
