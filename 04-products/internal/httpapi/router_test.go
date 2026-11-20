package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golearn/04-products/internal/auth"
	"golearn/04-products/internal/domain"
	"golearn/04-products/internal/httpapi/middleware"
)

// stubService trả kết quả/lỗi định sẵn; ghi lại filter nhận được để kiểm tra việc parse query string.
type stubService struct {
	product domain.Product
	page    domain.Page
	err     error
	panics  bool
	gotList domain.ListFilter
}

func (s *stubService) Create(context.Context, domain.Product) (domain.Product, error) {
	return s.product, s.err
}
func (s *stubService) Get(context.Context, int64) (domain.Product, error) {
	if s.panics {
		panic("boom")
	}
	return s.product, s.err
}
func (s *stubService) List(_ context.Context, f domain.ListFilter) (domain.Page, error) {
	s.gotList = f
	return s.page, s.err
}
func (s *stubService) Update(context.Context, domain.Product) (domain.Product, error) {
	return s.product, s.err
}
func (s *stubService) Delete(context.Context, int64) error { return s.err }
func (s *stubService) Purchase(context.Context, domain.Principal, int64, int64) (domain.Product, error) {
	return s.product, s.err
}
func (s *stubService) SetTags(context.Context, int64, []string) (domain.Product, error) {
	return s.product, s.err
}
func (s *stubService) SetAttributes(context.Context, int64, []domain.AttributeValue) (domain.Product, error) {
	return s.product, s.err
}
func (s *stubService) AddImage(context.Context, domain.Principal, int64, int64) (domain.Product, error) {
	return s.product, s.err
}
func (s *stubService) RemoveImage(context.Context, int64, int64) (domain.Product, error) {
	return s.product, s.err
}

// stubAuth: token "admin" → admin, "user" → customer, còn lại → không hợp lệ.
type stubAuth struct {
	tokens auth.Tokens
	err    error
}

func (a *stubAuth) Register(context.Context, domain.Registration) (domain.User, error) {
	return domain.User{ID: 1, Email: "a@b.vn", PasswordHash: "SECRET-HASH", Role: domain.RoleCustomer}, a.err
}
func (a *stubAuth) Login(context.Context, string, string) (auth.Tokens, error) {
	return a.tokens, a.err
}
func (a *stubAuth) Refresh(context.Context, string) (auth.Tokens, error) { return a.tokens, a.err }
func (a *stubAuth) Logout(context.Context, string) error                 { return a.err }
func (a *stubAuth) Me(_ context.Context, p domain.Principal) (domain.User, error) {
	return domain.User{ID: p.UserID, Role: p.Role, AvatarKey: "images/a.png"}, a.err
}
func (a *stubAuth) SetAvatar(context.Context, domain.Principal, int64) (domain.User, error) {
	return domain.User{}, a.err
}
func (a *stubAuth) VerifyAccess(token string) (domain.Principal, error) {
	switch token {
	case "admin":
		return domain.Principal{UserID: 1, Role: domain.RoleAdmin}, nil
	case "user":
		return domain.Principal{UserID: 2, Role: domain.RoleCustomer}, nil
	}
	return domain.Principal{}, domain.ErrUnauthenticated
}

type stubAssets struct{}

func (stubAssets) CreateUpload(context.Context, domain.Principal, domain.UploadRequest) (domain.Upload, error) {
	return domain.Upload{Asset: domain.Asset{ID: 5}, Method: "PUT", URL: "https://s3/x?sig", Headers: map[string]string{"Content-Type": "image/png"}}, nil
}

func as(token string, req *http.Request) *http.Request {
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

type result struct {
	status int
	header http.Header
	body   struct {
		Data  json.RawMessage `json:"data"`
		Meta  map[string]any  `json:"meta"`
		Error *struct {
			Code      string                   `json:"code"`
			Details   []struct{ Field string } `json:"details"`
			RequestID string                   `json:"request_id"`
		} `json:"error"`
	}
}

func serve(t *testing.T, svc ProductService, req *http.Request) result {
	return serveWith(t, svc, &stubAuth{}, req)
}

func serveWith(t *testing.T, svc ProductService, authSvc AuthService, req *http.Request) result {
	t.Helper()
	var draining atomic.Bool
	h := NewRouter(Deps{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Products: svc, Auth: authSvc, Assets: stubAssets{},
		AssetURL: func(key string) string { return "https://cdn.test/" + key },
		Draining: &draining, Ready: func(context.Context) error { return nil },
		RequestTimeout: time.Second, CORSOrigins: []string{"https://shop.vn"},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var res result
	res.status, res.header = rec.Code, rec.Header()
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &res.body); err != nil {
			t.Fatalf("body không phải JSON: %q", rec.Body.String())
		}
	}
	return res
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path, body string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"không tìm thấy", "GET", "/api/v1/products/1", "", domain.ErrProductNotFound, 404, "PRODUCT_NOT_FOUND"},
		{"trùng sku", "POST", "/api/v1/products", `{"sku":"A"}`, domain.ErrSKUTaken, 409, "SKU_TAKEN"},
		{"sửa bằng version cũ", "PUT", "/api/v1/products/1", `{"version":1}`, domain.ErrVersionConflict, 409, "VERSION_CONFLICT"},
		{"hết hàng", "POST", "/api/v1/products/1/purchase", `{"quantity":1}`, domain.ErrOutOfStock, 409, "OUT_OF_STOCK"},
		{"quá tải", "GET", "/api/v1/products/1", "", context.DeadlineExceeded, 503, "SERVICE_UNAVAILABLE"},
		{"lỗi lạ không lộ chi tiết", "DELETE", "/api/v1/products/1", "", io.ErrUnexpectedEOF, 500, "INTERNAL_ERROR"},
		{"id sai", "GET", "/api/v1/products/abc", "", nil, 400, "INVALID_ID"},
		{"json hỏng", "POST", "/api/v1/products", `{"sku":`, nil, 400, "INVALID_JSON"},
		{"field lạ", "POST", "/api/v1/products", `{"skuu":"A"}`, nil, 400, "INVALID_JSON"},
		{"route lạ", "GET", "/api/v2/nothing", "", nil, 404, "ROUTE_NOT_FOUND"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := serve(t, &stubService{err: c.err}, as("admin", httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))))
			if res.status != c.wantStatus || res.body.Error == nil || res.body.Error.Code != c.wantCode {
				t.Fatalf("status=%d error=%+v, muốn %d %s", res.status, res.body.Error, c.wantStatus, c.wantCode)
			}
			if res.body.Error.RequestID == "" || res.body.Error.RequestID != res.header.Get(middleware.RequestIDHeader) {
				t.Errorf("request_id trong body (%q) phải khớp header", res.body.Error.RequestID)
			}
		})
	}
}

func TestValidationErrorListsFields(t *testing.T) {
	err := (domain.Product{Price: -1}).Validate()
	res := serve(t, &stubService{err: err}, as("admin", httptest.NewRequest("POST", "/api/v1/products", strings.NewReader(`{}`))))
	if res.status != 400 || res.body.Error.Code != "VALIDATION_FAILED" || len(res.body.Error.Details) != 3 {
		t.Fatalf("status=%d error=%+v", res.status, res.body.Error)
	}
}

func TestSuccessEnvelope(t *testing.T) {
	svc := &stubService{product: domain.Product{ID: 7, SKU: "A", Name: "Áo", Images: []domain.Image{{AssetID: 3, Key: "images/x.png"}}}}
	res := serve(t, svc, as("admin", httptest.NewRequest("POST", "/api/v1/products", strings.NewReader(`{"sku":"A","name":"Áo"}`))))
	if res.status != 201 || res.header.Get("Location") != "/api/v1/products/7" || !strings.Contains(string(res.body.Data), `"sku":"A"`) {
		t.Fatalf("status=%d location=%q data=%s", res.status, res.header.Get("Location"), res.body.Data)
	}
	if !strings.Contains(string(res.body.Data), `"url":"https://cdn.test/images/x.png"`) {
		t.Errorf("ảnh phải có URL công khai ghép từ key: %s", res.body.Data)
	}

	svc = &stubService{page: domain.Page{Items: []domain.Product{{ID: 1}}, NextCursor: "abc"}}
	res = serve(t, svc, httptest.NewRequest("GET", "/api/v1/products?q=áo&min_price=10&max_price=99&in_stock=true&sort=-price&limit=5", nil))
	if res.status != 200 || res.body.Meta["next_cursor"] != "abc" || res.body.Meta["has_more"] != true {
		t.Fatalf("status=%d meta=%v", res.status, res.body.Meta)
	}
	f := svc.gotList
	if f.Query != "áo" || *f.MinPrice != 10 || *f.MaxPrice != 99 || !f.InStock || f.Sort != domain.SortPriceDesc || f.Limit != 5 {
		t.Errorf("parse query string sai: %+v", f)
	}

	res = serve(t, &stubService{page: domain.Page{Items: []domain.Product{}}}, httptest.NewRequest("GET", "/api/v1/products", nil))
	if string(res.body.Data) != "[]" || res.body.Meta["next_cursor"] != nil {
		t.Errorf("trang rỗng: data=%s meta=%v (muốn [] và next_cursor null)", res.body.Data, res.body.Meta)
	}
}

func TestListRejectsBadQueryParams(t *testing.T) {
	res := serve(t, &stubService{}, httptest.NewRequest("GET", "/api/v1/products?min_price=abc&in_stock=maybe&limit=0", nil))
	if res.status != 400 || len(res.body.Error.Details) != 3 {
		t.Fatalf("status=%d error=%+v, muốn 400 với 3 field lỗi", res.status, res.body.Error)
	}
}

func TestPanicBecomesJSON500(t *testing.T) {
	res := serve(t, &stubService{panics: true}, httptest.NewRequest("GET", "/api/v1/products/1", nil))
	if res.status != 500 || res.body.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("status=%d error=%+v", res.status, res.body.Error)
	}
}

func TestRequestIDAndCORS(t *testing.T) {
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("X-Request-ID", "abc-123")
	if res := serve(t, &stubService{}, req); res.header.Get("X-Request-ID") != "abc-123" {
		t.Errorf("không giữ X-Request-ID hợp lệ của client")
	}
	req = httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("X-Request-ID", "bad\nid")
	if res := serve(t, &stubService{}, req); res.header.Get("X-Request-ID") == "bad\nid" {
		t.Errorf("chấp nhận X-Request-ID chứa ký tự xuống dòng")
	}

	req = httptest.NewRequest("OPTIONS", "/api/v1/products", nil)
	req.Header.Set("Origin", "https://shop.vn")
	req.Header.Set("Access-Control-Request-Method", "POST")
	if res := serve(t, &stubService{}, req); res.status != 204 || res.header.Get("Access-Control-Allow-Origin") != "https://shop.vn" {
		t.Errorf("preflight: status=%d allow-origin=%q", res.status, res.header.Get("Access-Control-Allow-Origin"))
	}
	req = httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("Origin", "https://evil.com")
	if res := serve(t, &stubService{}, req); res.header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("origin lạ vẫn được cho phép")
	}
}

func TestAuthGuards(t *testing.T) {
	cases := []struct {
		method, path, token string
		want                int
	}{
		{"GET", "/api/v1/products/1", "", 200},               // xem: công khai
		{"POST", "/api/v1/products", "", 401},                // sửa: chưa đăng nhập
		{"POST", "/api/v1/products", "garbage", 401},         // token sai
		{"POST", "/api/v1/products", "user", 403},            // customer không được tạo sản phẩm
		{"POST", "/api/v1/products", "admin", 201},           // admin được
		{"DELETE", "/api/v1/products/1", "user", 403},        //
		{"POST", "/api/v1/products/1/images", "user", 403},   //
		{"POST", "/api/v1/products/1/purchase", "", 401},     // mua: phải đăng nhập
		{"POST", "/api/v1/products/1/purchase", "user", 200}, //
		{"GET", "/api/v1/me", "", 401},                       //
		{"POST", "/api/v1/assets", "", 401},                  // upload: phải đăng nhập
	}
	bodies := map[string]string{"/api/v1/products": `{"sku":"A","name":"x"}`, "/api/v1/products/1/purchase": `{"quantity":1}`,
		"/api/v1/products/1/images": `{"asset_id":1}`, "/api/v1/assets": `{"content_type":"image/png","size":1}`}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(bodies[c.path]))
		if c.token != "" {
			as(c.token, req)
		}
		res := serve(t, &stubService{product: domain.Product{ID: 1}}, req)
		if res.status != c.want {
			t.Errorf("%s %s (token=%q): status=%d, muốn %d", c.method, c.path, c.token, res.status, c.want)
		}
		if res.status == 401 && res.header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s %s: 401 phải kèm header WWW-Authenticate", c.method, c.path)
		}
	}
}

func TestAuthEndpoints(t *testing.T) {
	exp := time.Now().Add(15 * time.Minute)
	authSvc := &stubAuth{tokens: auth.Tokens{Access: "acc", AccessExp: exp, Refresh: "ref", RefreshExp: exp.Add(time.Hour)}}

	res := serveWith(t, &stubService{}, authSvc, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"a@b.vn","password":"x"}`)))
	if res.status != 200 || res.header.Get("Cache-Control") != "no-store" || !strings.Contains(string(res.body.Data), `"token_type":"Bearer"`) {
		t.Fatalf("login: status=%d cache=%q data=%s", res.status, res.header.Get("Cache-Control"), res.body.Data)
	}

	res = serveWith(t, &stubService{}, authSvc, httptest.NewRequest("POST", "/api/v1/auth/register", strings.NewReader(`{"email":"a@b.vn","password":"password1","name":"A"}`)))
	if res.status != 201 || strings.Contains(string(res.body.Data), "SECRET-HASH") || strings.Contains(string(res.body.Data), "password") {
		t.Fatalf("register: status=%d, response lộ mật khẩu/hash: %s", res.status, res.body.Data)
	}

	res = serveWith(t, &stubService{}, authSvc, as("user", httptest.NewRequest("GET", "/api/v1/me", nil)))
	if res.status != 200 || !strings.Contains(string(res.body.Data), `"avatar_url":"https://cdn.test/images/a.png"`) {
		t.Fatalf("me: status=%d data=%s", res.status, res.body.Data)
	}

	authSvc.err = domain.ErrTooManyAttempts
	res = serveWith(t, &stubService{}, authSvc, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"a@b.vn","password":"x"}`)))
	if res.status != 429 || res.body.Error.Code != "TOO_MANY_ATTEMPTS" {
		t.Fatalf("login bị khoá: status=%d error=%+v", res.status, res.body.Error)
	}

	res = serve(t, &stubService{}, as("user", httptest.NewRequest("POST", "/api/v1/assets", strings.NewReader(`{"content_type":"image/png","size":10}`))))
	if res.status != 201 || !strings.Contains(string(res.body.Data), `"method":"PUT"`) || !strings.Contains(string(res.body.Data), `"upload_url"`) {
		t.Fatalf("upload: status=%d data=%s", res.status, res.body.Data)
	}
}
