package link

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewRedisCache(rdb *redis.Client, ttl time.Duration) *RedisCache {
	return &RedisCache{rdb: rdb, ttl: ttl}
}

func (c *RedisCache) Get(ctx context.Context, code string) (string, error) {
	v, err := c.rdb.Get(ctx, cacheKey(code)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

func (c *RedisCache) Set(ctx context.Context, code, url string) error {
	return c.rdb.Set(ctx, cacheKey(code), url, c.ttl).Err()
}

func cacheKey(code string) string { return "link:" + code }
