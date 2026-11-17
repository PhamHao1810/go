package link

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ----- Bản giả (fake) trong bộ nhớ, thay cho Postgres/Redis khi unit test -----

type memRepo struct {
	mu    sync.Mutex
	links map[string]Link
	gets  int
}

func newMemRepo() *memRepo { return &memRepo{links: map[string]Link{}} }

func (m *memRepo) Create(_ context.Context, l Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.links[l.Code]; ok {
		return ErrCodeTaken
	}
	m.links[l.Code] = l
	return nil
}

func (m *memRepo) GetByCode(_ context.Context, code string) (Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	l, ok := m.links[code]
	if !ok {
		return Link{}, ErrNotFound
	}
	return l, nil
}

func (m *memRepo) IncrementClicks(_ context.Context, code string, n int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.links[code]
	l.Clicks += n
	m.links[code] = l
	return nil
}

type memCache struct {
	mu sync.Mutex
	m  map[string]string
}

func (c *memCache) Get(_ context.Context, code string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[code], nil
}

func (c *memCache) Set(_ context.Context, code, url string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[code] = url
	return nil
}

func newTestService() (*Service, *memRepo) {
	repo := newMemRepo()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(repo, &memCache{m: map[string]string{}}, log), repo
}

// ----- Test service -----

func TestShorten_InvalidURL(t *testing.T) {
	svc, _ := newTestService()
	for _, in := range []string{"", "abc", "ftp://x.com", "http://"} {
		if _, err := svc.Shorten(context.Background(), in); !errors.Is(err, ErrInvalidURL) {
			t.Errorf("Shorten(%q) err = %v, muốn ErrInvalidURL", in, err)
		}
	}
}

func TestResolve_UsesCacheAfterFirstHit(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()
	l, err := svc.Shorten(ctx, "https://go.dev")
	if err != nil {
		t.Fatal(err)
	}

	for range 3 {
		got, err := svc.Resolve(ctx, l.Code)
		if err != nil || got != "https://go.dev" {
			t.Fatalf("Resolve() = (%q, %v)", got, err)
		}
	}
	if repo.gets != 1 {
		t.Errorf("repo.GetByCode gọi %d lần, muốn 1 (các lần sau phải lấy từ cache)", repo.gets)
	}
	if c := repo.links[l.Code].Clicks; c != 3 {
		t.Errorf("clicks = %d, muốn 3", c)
	}
}

func TestResolve_NotFound(t *testing.T) {
	svc, _ := newTestService()
	if _, err := svc.Resolve(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, muốn ErrNotFound", err)
	}
}

// ----- Test handler bằng httptest (giống supertest bên Node) -----

func TestHandler_CreateThenRedirect(t *testing.T) {
	svc, _ := newTestService()
	mux := http.NewServeMux()
	NewHandler(svc, "http://short.test", slog.New(slog.NewTextHandler(io.Discard, nil))).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/links", strings.NewReader(`{"url":"https://go.dev"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	i := strings.Index(body, "http://short.test/")
	if i < 0 {
		t.Fatalf("response thiếu short_url: %s", body)
	}
	code := body[i+len("http://short.test/") : i+len("http://short.test/")+7]

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/"+code, nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://go.dev" {
		t.Errorf("GET /%s = %d Location=%q", code, rec.Code, rec.Header().Get("Location"))
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/links", strings.NewReader(`{"url":"not-a-url"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("URL sai: status = %d, muốn 400", rec.Code)
	}
}
