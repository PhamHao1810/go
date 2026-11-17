package rediscache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// OncePer bọc 1 job nền để cả cụm (mọi instance trên mọi máy) chỉ 1 instance chạy mỗi chu kỳ: ai giành được
// khoá "job:<name>" (SET NX, hết hạn trước chu kỳ 1 chút) thì chạy, còn lại bỏ qua.
// Redis lỗi → vẫn chạy: job đã an toàn khi chạy trùng, chỉ tốn thêm chút công.
func OncePer(rdb *redis.Client, name string, every time.Duration, fn func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		won, err := rdb.SetNX(ctx, "job:"+name, 1, every*9/10).Result()
		if err == nil && !won {
			return nil
		}
		return fn(ctx)
	}
}
