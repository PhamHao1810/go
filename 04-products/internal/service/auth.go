package service

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"time"

	"golearn/04-products/internal/auth"
	"golearn/04-products/internal/domain"
)

type AuthConfig struct {
	Secret     []byte        // khoá ký JWT, >= 32 byte
	AccessTTL  time.Duration // ngắn (15 phút): token bị lộ cũng chỉ dùng được ít phút
	RefreshTTL time.Duration // dài (30 ngày): dùng để xin access token mới, lưu DB nên thu hồi được
	BcryptCost int
}

type AuthService struct {
	users     UserRepository
	tokens    RefreshTokenRepository
	limiter   LoginLimiter
	tx        TxManager
	assets    AssetClaimer
	cfg       AuthConfig
	log       *slog.Logger
	dummyHash string
	bcrypt    chan struct{} // số lần băm bcrypt đang chạy (tối đa = cap)
	now       func() time.Time
}

// bcryptMaxWait: chờ lượt băm mật khẩu quá ngần này → 503, không để request dồn ứ khi bị spam đăng nhập.
const bcryptMaxWait = time.Second

// limited chạy fn khi còn lượt trong sem. bcrypt tốn ~250ms CPU/lần: không giới hạn thì đăng nhập dồn dập chiếm
// hết CPU, mọi request khác chậm theo. Hết lượt quá bcryptMaxWait → context.DeadlineExceeded (→ 503).
func limited[T any](ctx context.Context, sem chan struct{}, fn func() (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, bcryptMaxWait)
	defer cancel()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
	defer func() { <-sem }()
	return fn()
}

func NewAuthService(users UserRepository, tokens RefreshTokenRepository, limiter LoginLimiter, tx TxManager,
	assets AssetClaimer, cfg AuthConfig, log *slog.Logger) (*AuthService, error) {
	// Hash giả để so khi email không tồn tại: thời gian trả lời như nhau → không dò được email nào đã đăng ký.
	dummy, err := auth.HashPassword("dummy-password-for-timing", cfg.BcryptCost)
	if err != nil {
		return nil, err
	}
	return &AuthService{users: users, tokens: tokens, limiter: limiter, tx: tx, assets: assets, cfg: cfg, log: log,
		dummyHash: dummy, now: time.Now,
		// Mỗi instance chạy tối đa (số nhân / 2) lượt bcrypt cùng lúc. Không giới hạn thì hàng trăm goroutine bcrypt
		// cùng chờ CPU, request thường phải xếp hàng sau chúng (đo trên lab: xem sản phẩm p95 100ms → 12ms).
		bcrypt: make(chan struct{}, max(1, runtime.GOMAXPROCS(0)/2)),
	}, nil
}

func (s *AuthService) Register(ctx context.Context, r domain.Registration) (domain.User, error) {
	r.Email = domain.NormalizeEmail(r.Email)
	if err := r.Validate(); err != nil {
		return domain.User{}, err
	}
	hash, err := limited(ctx, s.bcrypt, func() (string, error) { return auth.HashPassword(r.Password, s.cfg.BcryptCost) })
	if err != nil {
		return domain.User{}, err
	}
	return s.users.Create(ctx, domain.User{Email: r.Email, PasswordHash: hash, Name: r.Name, Role: domain.RoleCustomer})
}

func (s *AuthService) Login(ctx context.Context, email, password string) (auth.Tokens, error) {
	email = domain.NormalizeEmail(email)
	// Redis lỗi thì vẫn cho đăng nhập (fail open): không để sự cố cache khoá toàn bộ người dùng.
	if blocked, err := s.limiter.Blocked(ctx, email); err != nil {
		s.log.WarnContext(ctx, "login limiter failed", "err", err)
	} else if blocked {
		return auth.Tokens{}, domain.ErrTooManyAttempts
	}

	u, err := s.users.GetByEmail(ctx, email)
	found := err == nil
	if !found && !errors.Is(err, domain.ErrUserNotFound) {
		return auth.Tokens{}, err
	}
	hash := u.PasswordHash
	if !found {
		hash = s.dummyHash // email không tồn tại vẫn băm như thường: thời gian trả lời như nhau, không dò được email
	}
	match, err := limited(ctx, s.bcrypt, func() (bool, error) { return auth.CheckPassword(hash, password), nil })
	if err != nil {
		return auth.Tokens{}, err
	}
	if found && match {
		if err := s.limiter.Reset(ctx, email); err != nil {
			s.log.WarnContext(ctx, "login limiter reset failed", "err", err)
		}
		return s.issue(ctx, u)
	}
	if err := s.limiter.Fail(ctx, email); err != nil {
		s.log.WarnContext(ctx, "login limiter failed", "err", err)
	}
	return auth.Tokens{}, domain.ErrInvalidCredentials
}

// Refresh đổi refresh token cũ lấy cặp token mới (rotation). Token cũ bị dùng lại → coi như bị đánh cắp:
// thu hồi MỌI phiên của user đó (cả kẻ gian lẫn chủ tài khoản phải đăng nhập lại).
func (s *AuthService) Refresh(ctx context.Context, refresh string) (auth.Tokens, error) {
	hash := auth.HashRefresh(refresh)
	var tokens auth.Tokens
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		userID, err := s.tokens.Consume(ctx, hash)
		if err != nil {
			return err
		}
		u, err := s.users.GetByID(ctx, userID) // đọc lại role mới nhất từ DB
		if err != nil {
			return err
		}
		tokens, err = s.issue(ctx, u) // ghi token mới trong CÙNG transaction với việc thu hồi token cũ
		return err
	})
	if errors.Is(err, domain.ErrInvalidRefreshToken) {
		if userID, reused, rerr := s.tokens.RevokedOwner(ctx, hash); rerr == nil && reused {
			s.log.WarnContext(ctx, "refresh token reuse detected, revoking all sessions", "user_id", userID)
			if rerr := s.tokens.RevokeAll(ctx, userID); rerr != nil {
				s.log.ErrorContext(ctx, "revoke all failed", "err", rerr, "user_id", userID)
			}
		}
	}
	return tokens, err
}

// Logout thu hồi refresh token. Access token hiện tại vẫn dùng được tới khi hết hạn (tối đa AccessTTL).
func (s *AuthService) Logout(ctx context.Context, refresh string) error {
	return s.tokens.Revoke(ctx, auth.HashRefresh(refresh))
}

func (s *AuthService) Me(ctx context.Context, actor domain.Principal) (domain.User, error) {
	return s.users.GetByID(ctx, actor.UserID)
}

func (s *AuthService) SetAvatar(ctx context.Context, actor domain.Principal, assetID int64) (domain.User, error) {
	if _, err := s.assets.Claim(ctx, actor, assetID); err != nil {
		return domain.User{}, err
	}
	if err := s.users.SetAvatar(ctx, actor.UserID, assetID); err != nil {
		return domain.User{}, err
	}
	return s.users.GetByID(ctx, actor.UserID)
}

// VerifyAccess dùng cho middleware: token sai/hết hạn → ErrUnauthenticated (không lộ lý do cụ thể).
func (s *AuthService) VerifyAccess(token string) (domain.Principal, error) {
	p, err := auth.ParseAccess(s.cfg.Secret, token)
	if err != nil {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	return p, nil
}

// EnsureAdmin tạo tài khoản admin đầu tiên lúc khởi động (ADMIN_EMAIL/ADMIN_PASSWORD). Email đã tồn tại
// mà không phải admin thì KHÔNG nâng quyền: chặn trường hợp ai đó đăng ký trước bằng email admin để chiếm quyền.
func (s *AuthService) EnsureAdmin(ctx context.Context, email, password string) error {
	email = domain.NormalizeEmail(email)
	u, err := s.users.GetByEmail(ctx, email)
	if err == nil {
		if u.Role != domain.RoleAdmin {
			s.log.ErrorContext(ctx, "ADMIN_EMAIL belongs to a non-admin account; not promoting", "email", email)
		}
		return nil
	}
	if !errors.Is(err, domain.ErrUserNotFound) {
		return err
	}
	r := domain.Registration{Email: email, Password: password, Name: "Admin"}
	if err := r.Validate(); err != nil {
		return err
	}
	hash, err := auth.HashPassword(password, s.cfg.BcryptCost)
	if err != nil {
		return err
	}
	_, err = s.users.Create(ctx, domain.User{Email: email, PasswordHash: hash, Name: "Admin", Role: domain.RoleAdmin})
	if errors.Is(err, domain.ErrEmailTaken) { // instance khác vừa tạo xong
		return nil
	}
	if err == nil {
		s.log.InfoContext(ctx, "admin account created", "email", email)
	}
	return err
}

// PurgeExpiredTokens xoá refresh token hết hạn quá 7 ngày (giữ lại 1 thời gian để vẫn phát hiện được token bị dùng lại).
func (s *AuthService) PurgeExpiredTokens(ctx context.Context) error {
	_, err := s.tokens.DeleteExpired(ctx, s.now().Add(-7*24*time.Hour))
	return err
}

func (s *AuthService) issue(ctx context.Context, u domain.User) (auth.Tokens, error) {
	now := s.now()
	access, accessExp, err := auth.IssueAccess(s.cfg.Secret, domain.Principal{UserID: u.ID, Role: u.Role}, s.cfg.AccessTTL, now)
	if err != nil {
		return auth.Tokens{}, err
	}
	refresh := auth.NewRefreshToken()
	refreshExp := now.Add(s.cfg.RefreshTTL)
	if err := s.tokens.Insert(ctx, u.ID, auth.HashRefresh(refresh), refreshExp); err != nil {
		return auth.Tokens{}, err
	}
	return auth.Tokens{Access: access, AccessExp: accessExp, Refresh: refresh, RefreshExp: refreshExp}, nil
}
