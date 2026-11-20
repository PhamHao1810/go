// Package config đọc cấu hình từ biến môi trường. Giá trị sai → app dừng ngay lúc khởi động
// và liệt kê hết biến sai, thay vì âm thầm dùng mặc định rồi chạy lệch trên production.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr            string
	DatabaseURL     string
	DatabaseReadURL string // tuỳ chọn: standby để đọc search/danh sách (chỉ dùng khi nó theo kịp primary)
	DBMaxConns      int32
	MigrateOnStart  bool
	RedisAddr       string
	RedisPassword   string
	CacheTTL        time.Duration
	RequestTimeout  time.Duration // quá hạn → 503, không để request xếp hàng chờ DB mãi khi quá tải
	ShutdownDelay   time.Duration // thời gian báo "đang tắt" cho load balancer trước khi tắt thật
	LogLevel        slog.Level
	CORSOrigins     []string // rỗng = tắt CORS; "*" = mọi origin

	MeiliURL      string        // rỗng = không dùng Meilisearch, search chạy bằng Postgres
	MeiliKey      string        // master key (hoặc API key có quyền search + ghi documents)
	SearchTimeout time.Duration // Meilisearch trả lời chậm hơn → dùng Postgres

	JWTSecret       []byte // BẮT BUỘC, >= 32 byte; đổi giá trị = mọi access token cũ mất hiệu lực
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	BcryptCost      int
	AdminEmail      string // có giá trị → tự tạo tài khoản admin đầu tiên lúc khởi động
	AdminPassword   string

	Storage Storage
}

type Storage struct {
	Endpoint        string
	PresignEndpoint string
	Region          string
	AccessKey       string
	SecretKey       string
	Bucket          string
	UseSSL          bool
	PublicURL       string
}

// Port 5433/6380 (không phải 5432/6379) để chạy song song được với 03-shortener.
func Load() (Config, error) {
	var p parser
	cfg := Config{
		Addr:            p.str("ADDR", ":8080"),
		DatabaseURL:     p.str("DATABASE_URL", "postgres://app:app@localhost:5433/products?sslmode=disable"),
		DatabaseReadURL: p.str("DATABASE_READ_URL", ""),
		DBMaxConns:      int32(p.int("DB_MAX_CONNS", 20, 1, 1000)),
		MigrateOnStart:  p.bool("MIGRATE_ON_START", true),
		RedisAddr:       p.str("REDIS_ADDR", "localhost:6380"),
		RedisPassword:   p.str("REDIS_PASSWORD", ""),
		CacheTTL:        p.dur("CACHE_TTL", 30*time.Second),
		RequestTimeout:  p.dur("REQUEST_TIMEOUT", 5*time.Second),
		ShutdownDelay:   p.dur("SHUTDOWN_DELAY", 0),
		MeiliURL:        p.str("MEILI_URL", ""),
		MeiliKey:        p.str("MEILI_KEY", ""),
		SearchTimeout:   p.dur("SEARCH_TIMEOUT", 800*time.Millisecond), // đủ cho lần đọc nguội từ đĩa ngay sau reindex

		JWTSecret:       []byte(p.str("JWT_SECRET", "")),
		AccessTokenTTL:  p.dur("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL: p.dur("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		BcryptCost:      p.int("BCRYPT_COST", 12, 10, 14),
		AdminEmail:      p.str("ADMIN_EMAIL", ""),
		AdminPassword:   p.str("ADMIN_PASSWORD", ""),

		// Mặc định khớp storage SeaweedFS trong docker-compose.yml (dev). Production: xem deploy/vps/compose.yml.
		Storage: Storage{
			Endpoint:  p.str("STORAGE_ENDPOINT", "localhost:9000"),
			Region:    p.str("STORAGE_REGION", "us-east-1"),
			AccessKey: p.str("STORAGE_ACCESS_KEY", "devaccesskey"),
			SecretKey: p.str("STORAGE_SECRET_KEY", "devsecretkey"),
			Bucket:    p.str("STORAGE_BUCKET", "products"),
			UseSSL:    p.bool("STORAGE_USE_SSL", false),
			PublicURL: p.str("STORAGE_PUBLIC_URL", "http://localhost:9000/products"),
		},
	}
	cfg.Storage.PresignEndpoint = p.str("STORAGE_PRESIGN_ENDPOINT", cfg.Storage.Endpoint)
	if len(cfg.JWTSecret) < 32 {
		p.errs = append(p.errs, errors.New("JWT_SECRET: required, at least 32 characters (vd: openssl rand -hex 32)"))
	}
	if (cfg.AdminEmail == "") != (cfg.AdminPassword == "") {
		p.errs = append(p.errs, errors.New("ADMIN_EMAIL and ADMIN_PASSWORD must be set together"))
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(p.str("LOG_LEVEL", "info"))); err != nil {
		p.errs = append(p.errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}
	for _, o := range strings.Split(p.str("CORS_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			cfg.CORSOrigins = append(cfg.CORSOrigins, o)
		}
	}
	return cfg, errors.Join(p.errs...)
}

type parser struct{ errs []error }

func (p *parser) str(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (p *parser) int(key string, def, min, max int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		p.errs = append(p.errs, fmt.Errorf("%s=%q: must be an integer in [%d, %d]", key, v, min, max))
	}
	return n
}

func (p *parser) dur(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		p.errs = append(p.errs, fmt.Errorf("%s=%q: must be a duration like 5s, 500ms", key, v))
	}
	return d
}

func (p *parser) bool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s=%q: must be true or false", key, v))
	}
	return b
}
