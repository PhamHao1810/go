package exercises

import (
	"errors"
	"testing"
	"time"
)

func TestCallWithTimeout_Fast(t *testing.T) {
	got, err := CallWithTimeout(100*time.Millisecond, func() string { return "ok" })
	if err != nil || got != "ok" {
		t.Fatalf(`CallWithTimeout() = (%q, %v), muốn ("ok", nil)`, got, err)
	}
}

func TestCallWithTimeout_Slow(t *testing.T) {
	start := time.Now()
	_, err := CallWithTimeout(50*time.Millisecond, func() string {
		time.Sleep(500 * time.Millisecond)
		return "quá muộn"
	})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("muốn ErrTimeout, nhận %v", err)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("mất %v mới trả về — phải trả ngay khi hết 50ms", d)
	}
}
