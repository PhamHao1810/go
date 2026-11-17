package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"golearn/04-products/internal/domain"
)

var secret = []byte("test-secret-at-least-32-bytes-long!!")

func TestAccessToken_RoundTrip(t *testing.T) {
	tok, _, err := IssueAccess(secret, domain.Principal{UserID: 42, Role: domain.RoleAdmin}, time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseAccess(secret, tok)
	if err != nil || p.UserID != 42 || p.Role != domain.RoleAdmin {
		t.Fatalf("ParseAccess = %+v, %v", p, err)
	}
}

func TestAccessToken_Rejects(t *testing.T) {
	valid, _, _ := IssueAccess(secret, domain.Principal{UserID: 1, Role: domain.RoleCustomer}, time.Minute, time.Now())
	expired, _, _ := IssueAccess(secret, domain.Principal{UserID: 1, Role: domain.RoleCustomer}, time.Minute, time.Now().Add(-time.Hour))
	otherKey, _, _ := IssueAccess([]byte("another-secret-another-secret-123"), domain.Principal{UserID: 1, Role: domain.RoleCustomer}, time.Minute, time.Now())

	// Kẻ tấn công sửa role trong payload thành admin, giữ nguyên chữ ký cũ.
	parts := strings.Split(valid, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"role":"admin","sub":"1","iss":"products-api","exp":9999999999}`))
	tampered := strings.Join(parts, ".")

	// Token "alg": "none" (không chữ ký).
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": "1", "role": "admin", "iss": "products-api",
		"exp": time.Now().Add(time.Hour).Unix()}).SignedString(jwt.UnsafeAllowNoneSignatureType)

	for name, tok := range map[string]string{"hết hạn": expired, "sai khoá": otherKey, "sửa payload": tampered, "alg none": none, "rác": "abc"} {
		if _, err := ParseAccess(secret, tok); err == nil {
			t.Errorf("%s: token bị chấp nhận", name)
		}
	}
}

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse", bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "correct horse") || CheckPassword(h, "wrong horse") {
		t.Error("CheckPassword sai")
	}
	if a, b := NewRefreshToken(), NewRefreshToken(); a == b || len(a) < 40 {
		t.Errorf("refresh token không ngẫu nhiên/đủ dài: %q %q", a, b)
	}
}
