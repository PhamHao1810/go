package rediscache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// LoginLimiter đếm số lần đăng nhập sai theo key (email) trong 1 cửa sổ thời gian. Lưu ở Redis nên mọi
// instance API dùng chung bộ đếm (nếu đếm trong RAM, kẻ tấn công chỉ cần rải request qua nhiều instance).
type LoginLimiter struct {
	rdb    *redis.Client
	max    int64
	window time.Duration
}

func NewLoginLimiter(rdb *redis.Client, max int64, window time.Duration) *LoginLimiter {
	return &LoginLimiter{rdb: rdb, max: max, window: window}
}

func (l *LoginLimiter) Blocked(ctx context.Context, key string) (bool, error) {
	n, err := l.rdb.Get(ctx, limiterKey(key)).Int64()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	return n >= l.max, err
}

func (l *LoginLimiter) Fail(ctx context.Context, key string) error {
	_, err := l.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Incr(ctx, limiterKey(key))
		p.ExpireNX(ctx, limiterKey(key), l.window) // cửa sổ tính từ lần sai đầu tiên
		return nil
	})
	return err
}

func (l *LoginLimiter) Reset(ctx context.Context, key string) error {
	return l.rdb.Del(ctx, limiterKey(key)).Err()
}

func limiterKey(key string) string { return "login_fail:" + key }
