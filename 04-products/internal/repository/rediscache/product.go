// Package rediscache cài đặt cache bằng Redis.
package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"golearn/04-products/internal/domain"
)

func Connect(ctx context.Context, addr, password string) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: password})
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return rdb, nil
}

type ProductCache struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewProductCache(rdb *redis.Client, ttl time.Duration) *ProductCache {
	return &ProductCache{rdb: rdb, ttl: ttl}
}

func (c *ProductCache) Get(ctx context.Context, id int64) (domain.Product, bool, error) {
	b, err := c.rdb.Get(ctx, key(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.Product{}, false, nil
	}
	if err != nil {
		return domain.Product{}, false, err
	}
	var p domain.Product
	if err := json.Unmarshal(b, &p); err != nil {
		return domain.Product{}, false, err
	}
	return p, true, nil
}

func (c *ProductCache) Set(ctx context.Context, p domain.Product) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key(p.ID), b, c.ttl).Err()
}

func (c *ProductCache) Delete(ctx context.Context, id int64) error {
	return c.rdb.Del(ctx, key(id)).Err()
}

// listTTL: kết quả search/danh sách cũ tối đa ngần này (sản phẩm mới/sửa giá hiện ra trễ ≤ 2 giây).
// Đủ để 1.000 người cùng xem 1 trang phổ biến trong 1 giây chỉ tốn 1 truy vấn DB.
const listTTL = 2 * time.Second

func (c *ProductCache) GetList(ctx context.Context, k string) (domain.Page, bool, error) {
	b, err := c.rdb.Get(ctx, "plist:v2:"+k).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.Page{}, false, nil
	}
	if err != nil {
		return domain.Page{}, false, err
	}
	var p domain.Page
	return p, true, json.Unmarshal(b, &p)
}

func (c *ProductCache) SetList(ctx context.Context, k string, p domain.Page) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, "plist:v2:"+k, b, listTTL).Err()
}

// searchTTL: kết quả tìm kiếm giữ lâu hơn danh sách: từ khoá phổ biến (hàng nghìn người gõ "áo thun") chỉ tốn
// 1 lần gọi Meilisearch mỗi 30 giây. Sản phẩm mới/giá mới hiện trong kết quả tìm kiếm trễ tối đa ngần này;
// trang chi tiết luôn đúng (cache riêng, xoá ngay khi sửa).
const searchTTL = 30 * time.Second

// GetSearch/SetSearch: kết quả tìm kiếm.
func (c *ProductCache) GetSearch(ctx context.Context, k string) (domain.SearchResult, bool, error) {
	b, err := c.rdb.Get(ctx, "search:v1:"+k).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.SearchResult{}, false, nil
	}
	if err != nil {
		return domain.SearchResult{}, false, err
	}
	var r domain.SearchResult
	return r, true, json.Unmarshal(b, &r)
}

func (c *ProductCache) SetSearch(ctx context.Context, k string, r domain.SearchResult) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, "search:v1:"+k, b, searchTTL).Err()
}

// Có version trong key: đổi cấu trúc domain.Product thì tăng v2 → v3, cache cũ tự bị bỏ qua thay vì đọc sai.
// v3: thêm hãng, danh mục, biến thể, thuộc tính, tag, điểm đánh giá.
func key(id int64) string { return "product:v3:" + strconv.FormatInt(id, 10) }
