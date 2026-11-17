package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"golearn/04-products/internal/auth"
	"golearn/04-products/internal/domain"
)

// ----- Bản giả -----

type memUsers struct {
	mu     sync.Mutex
	byID   map[int64]domain.User
	nextID int64
}

func (m *memUsers) Create(_ context.Context, u domain.User) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.byID {
		if x.Email == u.Email {
			return domain.User{}, domain.ErrEmailTaken
		}
	}
	m.nextID++
	u.ID = m.nextID
	m.byID[u.ID] = u
	return u, nil
}

func (m *memUsers) GetByEmail(_ context.Context, email string) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.byID {
		if u.Email == email {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrUserNotFound
}

func (m *memUsers) GetByID(_ context.Context, id int64) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return u, nil
}

func (m *memUsers) SetAvatar(_ context.Context, userID, assetID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.byID[userID]
	u.AvatarAssetID = &assetID
	m.byID[userID] = u
	return nil
}

type memToken struct {
	userID  int64
	expires time.Time
	revoked bool
}

type memTokens struct {
	mu sync.Mutex
	m  map[string]*memToken
}

func (m *memTokens) Insert(_ context.Context, userID int64, hash []byte, exp time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[string(hash)] = &memToken{userID: userID, expires: exp}
	return nil
}

func (m *memTokens) Consume(_ context.Context, hash []byte) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.m[string(hash)]
	if !ok || t.revoked || time.Now().After(t.expires) {
		return 0, domain.ErrInvalidRefreshToken
	}
	t.revoked = true
	return t.userID, nil
}

func (m *memTokens) RevokedOwner(_ context.Context, hash []byte) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.m[string(hash)]
	if ok && t.revoked {
		return t.userID, true, nil
	}
	return 0, false, nil
}

func (m *memTokens) Revoke(_ context.Context, hash []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.m[string(hash)]; ok {
		t.revoked = true
	}
	return nil
}

func (m *memTokens) RevokeAll(_ context.Context, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.m {
		if t.userID == userID {
			t.revoked = true
		}
	}
	return nil
}

func (m *memTokens) DeleteExpired(context.Context, time.Time) (int64, error) { return 0, nil }

type memLimiter struct {
	mu    sync.Mutex
	fails map[string]int
}

func (l *memLimiter) Blocked(_ context.Context, key string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fails[key] >= 3, nil
}

func (l *memLimiter) Fail(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key]++
	return nil
}

func (l *memLimiter) Reset(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
	return nil
}

// noTx chạy fn trực tiếp (bản giả không có transaction thật).
type noTx struct{}

func (noTx) WithinTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

func newAuth(t *testing.T) (*AuthService, *memUsers, *memTokens) {
	t.Helper()
	users := &memUsers{byID: map[int64]domain.User{}}
	tokens := &memTokens{m: map[string]*memToken{}}
	svc, err := NewAuthService(users, tokens, &memLimiter{fails: map[string]int{}}, noTx{}, fakeClaimer{},
		AuthConfig{Secret: []byte(strings.Repeat("s", 32)), AccessTTL: time.Minute, RefreshTTL: time.Hour, BcryptCost: bcrypt.MinCost},
		discard)
	if err != nil {
		t.Fatal(err)
	}
	return svc, users, tokens
}

// ----- Test -----

func TestRegisterLoginMe(t *testing.T) {
	svc, _, _ := newAuth(t)
	ctx := context.Background()

	u, err := svc.Register(ctx, domain.Registration{Email: "  An@Shop.VN ", Password: "password123", Name: "An"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "an@shop.vn" || u.Role != domain.RoleCustomer || u.PasswordHash == "password123" {
		t.Fatalf("user = %+v (email phải chuẩn hoá, role customer, mật khẩu phải được hash)", u)
	}
	if _, err := svc.Register(ctx, domain.Registration{Email: "an@shop.vn", Password: "password123", Name: "An"}); !errors.Is(err, domain.ErrEmailTaken) {
		t.Errorf("đăng ký trùng email: err = %v", err)
	}

	tok, err := svc.Login(ctx, "AN@shop.vn", "password123")
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.VerifyAccess(tok.Access)
	if err != nil || p.UserID != u.ID || p.Role != domain.RoleCustomer {
		t.Fatalf("VerifyAccess = %+v, %v", p, err)
	}
	if me, _ := svc.Me(ctx, p); me.ID != u.ID {
		t.Errorf("Me trả user %d", me.ID)
	}
}

func TestLogin_SameErrorAndLockout(t *testing.T) {
	svc, _, _ := newAuth(t)
	ctx := context.Background()
	_, _ = svc.Register(ctx, domain.Registration{Email: "an@shop.vn", Password: "password123", Name: "An"})

	if _, err := svc.Login(ctx, "nobody@shop.vn", "x"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("email không tồn tại: err = %v", err)
	}
	for range 3 {
		if _, err := svc.Login(ctx, "an@shop.vn", "wrong-pass"); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Errorf("sai mật khẩu: err = %v (phải giống hệt lỗi email không tồn tại)", err)
		}
	}
	// Sai 3 lần (ngưỡng của bản giả) → khoá, kể cả khi lần này nhập đúng.
	if _, err := svc.Login(ctx, "an@shop.vn", "password123"); !errors.Is(err, domain.ErrTooManyAttempts) {
		t.Errorf("sau 3 lần sai: err = %v, muốn ErrTooManyAttempts", err)
	}
}

func TestRefresh_RotatesAndDetectsReuse(t *testing.T) {
	svc, _, _ := newAuth(t)
	ctx := context.Background()
	_, _ = svc.Register(ctx, domain.Registration{Email: "an@shop.vn", Password: "password123", Name: "An"})
	t1, _ := svc.Login(ctx, "an@shop.vn", "password123")

	t2, err := svc.Refresh(ctx, t1.Refresh)
	if err != nil || t2.Refresh == t1.Refresh {
		t.Fatalf("refresh lần 1: %v (token mới phải khác token cũ)", err)
	}
	// Kẻ gian dùng lại token cũ → bị từ chối VÀ mọi phiên bị thu hồi, kể cả t2 của chủ tài khoản.
	if _, err := svc.Refresh(ctx, t1.Refresh); !errors.Is(err, domain.ErrInvalidRefreshToken) {
		t.Fatalf("dùng lại token cũ: err = %v", err)
	}
	if _, err := svc.Refresh(ctx, t2.Refresh); !errors.Is(err, domain.ErrInvalidRefreshToken) {
		t.Errorf("sau khi phát hiện dùng lại, phiên t2 vẫn còn sống: err = %v", err)
	}

	t3, _ := svc.Login(ctx, "an@shop.vn", "password123")
	if err := svc.Logout(ctx, t3.Refresh); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(ctx, t3.Refresh); !errors.Is(err, domain.ErrInvalidRefreshToken) {
		t.Errorf("refresh sau logout: err = %v", err)
	}
}

func TestEnsureAdmin_DoesNotPromoteExistingCustomer(t *testing.T) {
	svc, users, _ := newAuth(t)
	ctx := context.Background()
	// Ai đó đăng ký trước bằng email admin.
	_, _ = svc.Register(ctx, domain.Registration{Email: "admin@shop.vn", Password: "attacker-pass", Name: "X"})
	if err := svc.EnsureAdmin(ctx, "admin@shop.vn", "real-admin-pass"); err != nil {
		t.Fatal(err)
	}
	if u, _ := users.GetByEmail(ctx, "admin@shop.vn"); u.Role != domain.RoleCustomer {
		t.Fatal("tài khoản customer có sẵn bị nâng lên admin")
	}

	if err := svc.EnsureAdmin(ctx, "boss@shop.vn", "real-admin-pass"); err != nil {
		t.Fatal(err)
	}
	if u, _ := users.GetByEmail(ctx, "boss@shop.vn"); u.Role != domain.RoleAdmin || !auth.CheckPassword(u.PasswordHash, "real-admin-pass") {
		t.Errorf("admin mới: %+v", u)
	}
	if err := svc.EnsureAdmin(ctx, "boss@shop.vn", "real-admin-pass"); err != nil {
		t.Errorf("chạy lại EnsureAdmin (khởi động lần 2) phải không lỗi: %v", err)
	}
}
