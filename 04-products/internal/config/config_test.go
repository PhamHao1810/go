package config

import (
	"strings"
	"testing"
)

func TestLoad_ReportsAllInvalidVars(t *testing.T) {
	t.Setenv("DB_MAX_CONNS", "abc")
	t.Setenv("REQUEST_TIMEOUT", "5")
	t.Setenv("LOG_LEVEL", "loud")
	t.Setenv("JWT_SECRET", "short")
	t.Setenv("ADMIN_EMAIL", "a@b.vn")
	_, err := Load()
	if err == nil {
		t.Fatal("cấu hình sai nhưng không báo lỗi")
	}
	for _, key := range []string{"DB_MAX_CONNS", "REQUEST_TIMEOUT", "LOG_LEVEL", "JWT_SECRET", "ADMIN_PASSWORD"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("lỗi thiếu %s: %v", key, err)
		}
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("JWT_SECRET", strings.Repeat("x", 32))
	t.Setenv("CORS_ORIGINS", "https://a.vn, https://b.vn")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBMaxConns != 20 || !cfg.MigrateOnStart || len(cfg.CORSOrigins) != 2 || cfg.Storage.PresignEndpoint != "localhost:9000" {
		t.Errorf("cfg = %+v", cfg)
	}
}
