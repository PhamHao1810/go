package domain

import (
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"
)

type Role string

const (
	RoleCustomer Role = "customer"
	RoleAdmin    Role = "admin"
)

type User struct {
	ID            int64
	Email         string
	PasswordHash  string
	Name          string
	Role          Role
	AvatarAssetID *int64
	AvatarKey     string // key file avatar trong storage (rỗng nếu chưa có), repo JOIN sẵn
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Principal: người đang gọi API, lấy từ access token. Truyền tường minh vào service (actor) thay vì
// để service tự đọc từ ctx → đọc code là biết hàm nào cần biết "ai đang làm".
type Principal struct {
	UserID int64
	Role   Role
}

func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

type Registration struct {
	Email    string
	Password string
	Name     string
}

func (r Registration) Validate() error {
	var v Validator
	addr, err := mail.ParseAddress(r.Email)
	v.Check(err == nil && addr.Address == r.Email && len(r.Email) <= 254, "email", "must be a valid email address")
	// bcrypt chỉ dùng 72 byte đầu: dài hơn sẽ âm thầm bị cắt → chặn luôn cho rõ ràng.
	v.Check(len(r.Password) >= 8 && len(r.Password) <= 72, "password", "must be 8-72 bytes")
	v.Check(strings.TrimSpace(r.Name) != "" && utf8.RuneCountInString(r.Name) <= 100, "name", "required, max 100 characters")
	return v.Err()
}
