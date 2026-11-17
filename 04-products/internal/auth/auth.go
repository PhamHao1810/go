// Package auth: các hàm mật mã thuần (không I/O) cho đăng nhập: hash mật khẩu, JWT access token, refresh token.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"golearn/04-products/internal/domain"
)

// ----- Mật khẩu -----

func HashPassword(password string, cost int) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	return string(b), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ----- Access token (JWT, sống ngắn, không lưu DB) -----

type Tokens struct {
	Access     string
	AccessExp  time.Time
	Refresh    string // trả cho client 1 lần duy nhất; DB chỉ lưu HashRefresh(Refresh)
	RefreshExp time.Time
}

type claims struct {
	Role domain.Role `json:"role"`
	jwt.RegisteredClaims
}

const issuer = "products-api"

func IssueAccess(secret []byte, p domain.Principal, ttl time.Duration, now time.Time) (string, time.Time, error) {
	exp := now.Add(ttl)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		Role: p.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(p.UserID, 10),
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	})
	s, err := tok.SignedString(secret)
	return s, exp, err
}

var errBadToken = errors.New("bad token")

// ParseAccess kiểm tra chữ ký, hạn dùng, issuer. Chỉ chấp nhận HS256: chặn tấn công đổi "alg" thành "none"/RS256.
func ParseAccess(secret []byte, token string) (domain.Principal, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return domain.Principal{}, err
	}
	id, err := strconv.ParseInt(c.Subject, 10, 64)
	if err != nil || id < 1 || (c.Role != domain.RoleAdmin && c.Role != domain.RoleCustomer) {
		return domain.Principal{}, errBadToken
	}
	return domain.Principal{UserID: id, Role: c.Role}, nil
}

// ----- Refresh token (chuỗi ngẫu nhiên, lưu hash trong DB, dùng 1 lần) -----

func NewRefreshToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashRefresh: SHA-256 là đủ (token ngẫu nhiên 256 bit, không đoán được như mật khẩu → không cần bcrypt).
func HashRefresh(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
