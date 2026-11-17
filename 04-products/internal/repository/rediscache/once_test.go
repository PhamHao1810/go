package rediscache

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cần Redis thật: $env:TEST_REDIS_ADDR="localhost:6380"
func TestOncePer_OnlyOneInstanceRunsPerWindow(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR chưa đặt")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { rdb.Close() })
	name := "test-" + time.Now().Format("150405.000000000")

	var runs atomic.Int32
	job := func(context.Context) error { runs.Add(1); return nil }
	var wg sync.WaitGroup
	for range 8 { // 8 instance cùng tới lượt
		wg.Add(1)
		go func() { defer wg.Done(); _ = OncePer(rdb, name, 2*time.Second, job)(context.Background()) }()
	}
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("chạy %d lần, muốn đúng 1", runs.Load())
	}
	time.Sleep(2 * time.Second) // sang chu kỳ mới
	_ = OncePer(rdb, name, 2*time.Second, job)(context.Background())
	if runs.Load() != 2 {
		t.Fatalf("chu kỳ mới phải chạy lại, đang %d", runs.Load())
	}
}

func TestOncePer_RedisDownStillRuns(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { rdb.Close() })
	ran := false
	_ = OncePer(rdb, "x", time.Minute, func(context.Context) error { ran = true; return nil })(context.Background())
	if !ran {
		t.Fatal("Redis lỗi thì job vẫn phải chạy")
	}
}
