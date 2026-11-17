package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golearn/04-products/internal/domain"
)

func newUser(t *testing.T, users *UserRepo) domain.User {
	t.Helper()
	u, err := users.Create(context.Background(), domain.User{Email: uniq() + "@test.vn", PasswordHash: "h", Name: "T", Role: domain.RoleCustomer})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestPostgres_UsersAndRefreshTokens(t *testing.T) {
	repo := newTestRepo(t)
	users, tokens := NewUserRepo(repo.pool), NewRefreshTokenRepo(repo.pool)
	ctx := context.Background()

	u := newUser(t, users)
	if _, err := users.Create(ctx, domain.User{Email: u.Email, PasswordHash: "h", Name: "x", Role: domain.RoleCustomer}); !errors.Is(err, domain.ErrEmailTaken) {
		t.Errorf("trùng email: err = %v", err)
	}
	if got, err := users.GetByEmail(ctx, u.Email); err != nil || got.ID != u.ID || got.Role != domain.RoleCustomer {
		t.Fatalf("GetByEmail = %+v, %v", got, err)
	}

	// 20 request refresh cùng lúc bằng 1 token → đúng 1 thành công.
	hash := []byte(uniq())
	if err := tokens.Insert(ctx, u.ID, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var ok atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := tokens.Consume(ctx, hash); err == nil {
				ok.Add(1)
			} else if !errors.Is(err, domain.ErrInvalidRefreshToken) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Errorf("%d request refresh thành công bằng cùng 1 token, muốn 1", ok.Load())
	}
	if owner, reused, _ := tokens.RevokedOwner(ctx, hash); !reused || owner != u.ID {
		t.Errorf("RevokedOwner = %d, %v", owner, reused)
	}

	expired := []byte(uniq())
	_ = tokens.Insert(ctx, u.ID, expired, time.Now().Add(-time.Minute))
	if _, err := tokens.Consume(ctx, expired); !errors.Is(err, domain.ErrInvalidRefreshToken) {
		t.Errorf("token hết hạn vẫn dùng được: err = %v", err)
	}
}

func TestPostgres_ProductImagesAndGarbageCollection(t *testing.T) {
	repo := newTestRepo(t)
	users, assets := NewUserRepo(repo.pool), NewAssetRepo(repo.pool)
	ctx := context.Background()
	u := newUser(t, users)

	newAsset := func() domain.Asset {
		a, err := assets.Create(ctx, domain.Asset{Key: "test/" + uniq() + ".png", ContentType: "image/png", Size: 10, UploadedBy: u.ID})
		if err != nil {
			t.Fatal(err)
		}
		_ = assets.MarkReady(ctx, a.ID)
		return a
	}

	p, _ := repo.Create(ctx, domain.Product{SKU: uniq(), Name: "Có ảnh", Price: 1, Stock: 1})
	a1, a2 := newAsset(), newAsset()
	for _, a := range []domain.Asset{a1, a2, a1} { // gắn a1 lần 2: không lỗi, không trùng
		if err := repo.AddImage(ctx, p.ID, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := repo.Get(ctx, p.ID)
	if len(got.Images) != 2 || got.Images[0].AssetID != a1.ID || got.Images[0].Key != a1.Key {
		t.Fatalf("images = %+v", got.Images)
	}
	if err := repo.AddImage(ctx, 999_999_999, a1.ID); !errors.Is(err, domain.ErrProductNotFound) {
		t.Errorf("gắn ảnh vào sản phẩm không tồn tại: err = %v", err)
	}
	for range domain.MaxProductImages - 2 {
		_ = repo.AddImage(ctx, p.ID, newAsset().ID)
	}
	if err := repo.AddImage(ctx, p.ID, newAsset().ID); !errors.Is(err, domain.ErrTooManyImages) {
		t.Errorf("ảnh thứ 11: err = %v", err)
	}

	// Avatar đang dùng + ảnh đang gắn: KHÔNG được coi là rác dù đã cũ.
	avatar := newAsset()
	_ = users.SetAvatar(ctx, u.ID, avatar.ID)
	orphan := newAsset() // không ai dùng
	if err := repo.RemoveImage(ctx, p.ID, a2.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveImage(ctx, p.ID, a2.ID); !errors.Is(err, domain.ErrImageNotFound) {
		t.Errorf("gỡ ảnh lần 2: err = %v", err)
	}
	// Giả lập đã qua 2 giờ.
	_, _ = repo.pool.Exec(ctx, `UPDATE assets SET updated_at = now() - interval '2 hours' WHERE id = ANY($1)`,
		[]int64{a1.ID, a2.ID, avatar.ID, orphan.ID})

	ids, err := assets.GarbageIDs(ctx, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	isGarbage := map[int64]bool{}
	for _, id := range ids {
		isGarbage[id] = true
	}
	for name, c := range map[string]struct {
		id   int64
		want bool
	}{"ảnh đang gắn": {a1.ID, false}, "avatar đang dùng": {avatar.ID, false}, "ảnh đã gỡ": {a2.ID, true}, "file mồ côi": {orphan.ID, true}} {
		if isGarbage[c.id] != c.want {
			t.Errorf("%s: là rác = %v, muốn %v", name, isGarbage[c.id], c.want)
		}
	}
	if _, ok, err := assets.DeleteGarbage(ctx, a1.ID); ok || err != nil {
		t.Errorf("DeleteGarbage xoá ảnh đang dùng: ok=%v err=%v", ok, err)
	}
	if key, ok, err := assets.DeleteGarbage(ctx, orphan.ID); !ok || err != nil || key != orphan.Key {
		t.Errorf("DeleteGarbage file mồ côi: key=%q ok=%v err=%v", key, ok, err)
	}

	// Xoá sản phẩm → ảnh của nó thành rác.
	_ = repo.Delete(ctx, p.ID)
	_, _ = repo.pool.Exec(ctx, `UPDATE assets SET updated_at = now() - interval '2 hours' WHERE id = $1`, a1.ID)
	if _, ok, _ := assets.DeleteGarbage(ctx, a1.ID); !ok {
		t.Error("ảnh của sản phẩm đã xoá không được dọn")
	}
}
