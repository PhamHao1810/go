package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"golearn/04-products/internal/domain"
)

type memAssets struct {
	m map[int64]domain.Asset
}

func (r *memAssets) Create(_ context.Context, a domain.Asset) (domain.Asset, error) {
	a.ID = int64(len(r.m) + 1)
	a.Status = domain.AssetPending
	r.m[a.ID] = a
	return a, nil
}

func (r *memAssets) Get(_ context.Context, id int64) (domain.Asset, error) {
	a, ok := r.m[id]
	if !ok {
		return domain.Asset{}, domain.ErrAssetNotFound
	}
	return a, nil
}

func (r *memAssets) MarkReady(_ context.Context, id int64) error {
	a := r.m[id]
	a.Status = domain.AssetReady
	r.m[id] = a
	return nil
}

func (r *memAssets) GarbageIDs(context.Context, int) ([]int64, error) { return nil, nil }
func (r *memAssets) DeleteGarbage(context.Context, int64) (string, bool, error) {
	return "", false, nil
}

// fakeStore: file đã "upload" lên storage.
type fakeStore struct {
	files map[string]fakeFile
}

type fakeFile struct {
	contentType string
	data        []byte
}

func (s *fakeStore) PresignUpload(_ context.Context, key, ct string, _ int64, _ time.Duration) (domain.Upload, error) {
	return domain.Upload{Method: "PUT", URL: "https://storage/" + key + "?sig", Headers: map[string]string{"Content-Type": ct}}, nil
}

func (s *fakeStore) Stat(_ context.Context, key string) (int64, string, bool, error) {
	f, ok := s.files[key]
	return int64(len(f.data)), f.contentType, ok, nil
}

func (s *fakeStore) ReadHead(_ context.Context, key string, n int64) ([]byte, error) {
	return s.files[key].data[:min(n, int64(len(s.files[key].data)))], nil
}

func (s *fakeStore) Remove(context.Context, string) error { return nil }

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 100)...)

func TestClaim(t *testing.T) {
	ctx := context.Background()
	owner, stranger := domain.Principal{UserID: 1}, domain.Principal{UserID: 2}

	cases := []struct {
		name    string
		upload  *fakeFile // nil = client chưa upload
		actor   domain.Principal
		wantErr error
	}{
		{"chưa upload", nil, owner, domain.ErrAssetNotUploaded},
		{"file của người khác", &fakeFile{"image/png", pngBytes}, stranger, domain.ErrAssetNotFound},
		{"khai png nhưng thật ra là HTML", &fakeFile{"image/png", append([]byte("<html><script>"), make([]byte, 94)...)}, owner, domain.ErrAssetMismatch},
		{"sai kích thước", &fakeFile{"image/png", pngBytes[:50]}, owner, domain.ErrAssetMismatch},
		{"hợp lệ", &fakeFile{"image/png", pngBytes}, owner, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, store := &memAssets{m: map[int64]domain.Asset{}}, &fakeStore{files: map[string]fakeFile{}}
			svc := NewAssetService(repo, store, noTx{}, discard)
			up, err := svc.CreateUpload(ctx, owner, domain.UploadRequest{ContentType: "image/png", Size: int64(len(pngBytes))})
			if err != nil {
				t.Fatal(err)
			}
			if c.upload != nil {
				store.files[up.Asset.Key] = *c.upload
			}
			a, err := svc.Claim(ctx, c.actor, up.Asset.ID)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, muốn %v", err, c.wantErr)
			}
			if err == nil && (a.Status != domain.AssetReady || repo.m[a.ID].Status != domain.AssetReady) {
				t.Error("claim thành công nhưng asset chưa ready")
			}
		})
	}
}

func TestCreateUpload_Validates(t *testing.T) {
	svc := NewAssetService(&memAssets{m: map[int64]domain.Asset{}}, &fakeStore{}, noTx{}, discard)
	for _, req := range []domain.UploadRequest{
		{ContentType: "image/svg+xml", Size: 10}, // SVG chứa được script
		{ContentType: "image/png", Size: domain.MaxAssetSize + 1},
		{ContentType: "image/png", Size: 0},
	} {
		var de *domain.Error
		if _, err := svc.CreateUpload(context.Background(), domain.Principal{UserID: 1}, req); !errors.As(err, &de) || de.Code != "VALIDATION_FAILED" {
			t.Errorf("%+v: err = %v", req, err)
		}
	}
}
