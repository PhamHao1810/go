package exercises

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPool(t *testing.T) {
	jobs := make([]int, 50)
	want := make([]int, 50)
	for i := range jobs {
		jobs[i] = i
		want[i] = i * i
	}

	var running, maxRunning atomic.Int32
	square := func(n int) int {
		cur := running.Add(1)
		for { // ghi nhận số goroutine chạy đồng thời lớn nhất
			old := maxRunning.Load()
			if cur <= old || maxRunning.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		running.Add(-1)
		return n * n
	}

	got := WorkerPool(jobs, 5, square)
	if !slices.Equal(got, want) {
		t.Fatalf("WorkerPool() = %v, muốn %v", got, want)
	}
	if m := maxRunning.Load(); m > 5 {
		t.Errorf("có %d goroutine chạy cùng lúc, tối đa cho phép là 5", m)
	} else if m < 2 {
		t.Errorf("chỉ có %d goroutine chạy cùng lúc — chưa song song", m)
	}
}
