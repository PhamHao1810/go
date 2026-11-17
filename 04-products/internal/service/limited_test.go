package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimited_NeverExceedsCapacity(t *testing.T) {
	sem := make(chan struct{}, 3)
	var running, peak atomic.Int32
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := limited(context.Background(), sem, func() (bool, error) {
				n := running.Add(1)
				for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
				}
				time.Sleep(5 * time.Millisecond) // giả lập bcrypt
				running.Add(-1)
				return true, nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak.Load() > 3 {
		t.Fatalf("có lúc %d lượt chạy cùng lúc, tối đa phải là 3", peak.Load())
	}
}

func TestLimited_FullQueueGivesUp(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{} // lượt duy nhất đang bị chiếm
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ran := false
	_, err := limited(ctx, sem, func() (bool, error) { ran = true; return true, nil })
	if !errors.Is(err, context.DeadlineExceeded) || ran {
		t.Fatalf("hết lượt phải trả DeadlineExceeded (→ 503) và không chạy fn; err=%v ran=%v", err, ran)
	}
}
