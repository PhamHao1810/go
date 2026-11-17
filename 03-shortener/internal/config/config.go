// Package config đọc cấu hình từ biến môi trường (giống @nestjs/config + .env).
package config

import (
	"os"
	"time"
)

type Config struct {
	Addr        string
	BaseURL     string
	DatabaseURL string
	RedisAddr   string
	CacheTTL    time.Duration
}

func Load() Config {
	return Config{
		Addr:        getenv("ADDR", ":8080"),
		BaseURL:     getenv("BASE_URL", "http://localhost:8080"),
		DatabaseURL: getenv("DATABASE_URL", "postgres://app:app@localhost:5432/shortener?sslmode=disable"),
		RedisAddr:   getenv("REDIS_ADDR", "localhost:6379"),
		CacheTTL:    time.Hour,
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
