package link

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"time"
)

// Repository và Cache là interface định nghĩa ở phía DÙNG (service), không phải phía cài đặt.
// Nhờ vậy service_test.go thay được bằng bản giả trong bộ nhớ, không cần Postgres/Redis.
type Repository interface {
	Create(ctx context.Context, l Link) error
	GetByCode(ctx context.Context, code string) (Link, error)
	IncrementClicks(ctx context.Context, code string, n int64) error
}

type Cache interface {
	Get(ctx context.Context, code string) (string, error) // ("", nil) nếu cache miss
	Set(ctx context.Context, code, url string) error
}

type Service struct {
	repo  Repository
	cache Cache
	log   *slog.Logger
}

// Constructor injection: Go không cần DI container, chỉ cần truyền dependency vào.
func NewService(repo Repository, cache Cache, log *slog.Logger) *Service {
	return &Service{repo: repo, cache: cache, log: log}
}

func (s *Service) Shorten(ctx context.Context, rawURL string) (Link, error) {
	u, err := url.ParseRequestURI(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Link{}, ErrInvalidURL
	}
	// Mã ngẫu nhiên có thể trùng (hiếm) -> thử lại vài lần.
	for range 3 {
		l := Link{Code: newCode(7), URL: rawURL, CreatedAt: time.Now().UTC()}
		err := s.repo.Create(ctx, l)
		if errors.Is(err, ErrCodeTaken) {
			continue
		}
		if err != nil {
			return Link{}, fmt.Errorf("create link: %w", err)
		}
		return l, nil
	}
	return Link{}, fmt.Errorf("shorten after 3 tries: %w", ErrCodeTaken)
}

// Resolve tìm URL gốc theo pattern cache-aside: đọc Redis trước, miss thì đọc Postgres rồi ghi lại Redis.
func (s *Service) Resolve(ctx context.Context, code string) (string, error) {
	target, err := s.cache.Get(ctx, code)
	if err != nil {
		// Redis lỗi thì vẫn phục vụ từ DB, không trả 500 cho user.
		s.log.WarnContext(ctx, "cache get failed", "err", err)
	}
	if target == "" {
		l, err := s.repo.GetByCode(ctx, code)
		if err != nil {
			return "", err
		}
		target = l.URL
		if err := s.cache.Set(ctx, code, target); err != nil {
			s.log.WarnContext(ctx, "cache set failed", "err", err)
		}
	}

	// Ghi DB mỗi lần click: đây sẽ là NÚT THẮT khi bạn load test.
	// Bài tập giai đoạn 4: đổi sang Redis INCR + worker gom batch ghi DB mỗi vài giây.
	if err := s.repo.IncrementClicks(ctx, code, 1); err != nil {
		s.log.WarnContext(ctx, "increment clicks failed", "err", err)
	}
	return target, nil
}

func (s *Service) Get(ctx context.Context, code string) (Link, error) {
	return s.repo.GetByCode(ctx, code)
}

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func newCode(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rand.IntN(len(alphabet))]
	}
	return string(b)
}
