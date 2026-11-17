# 04-products: API sản phẩm chịu tải cao

## Cấu trúc

Theo [hướng dẫn chính thức của Go](https://go.dev/doc/modules/layout): entrypoint trong `cmd/`, toàn bộ code trong
`internal/`. Bên trong chia tầng; **mũi tên phụ thuộc chỉ đi vào trong**, compiler chặn mọi import ngược.

```
cmd/api/main.go                  Composition root: đọc config, tạo dependency, ghép các tầng, job nền, graceful shutdown
internal/
  domain/                        Entity, quy tắc nghiệp vụ, lỗi nghiệp vụ. KHÔNG import gì của project
    product.go, filter.go        Product + Validate(), ListFilter (lọc/sắp xếp/phân trang)
    user.go, asset.go            User, Role, Principal (người đang gọi API), Asset (file), Image
    errors.go                    domain.Error {Kind, Code, Message, Fields} + Validator
  service/                       Use case (business logic)
    ports.go                     MỌI interface service cần (repository, cache, storage, transaction)
    product.go, auth.go, asset.go
  auth/                          Hàm mật mã thuần: bcrypt, JWT, refresh token (không I/O)
  repository/                    Cài đặt các interface trong service/ports.go
    postgres/                    1 file / bảng: product.go, user.go (+ refresh token), asset.go
      tx.go                      TxManager: gói nhiều repository vào 1 transaction
      query.go, migrate.go       Ghép SQL filter/sort/cursor; chạy migration lúc khởi động
    rediscache/                  Cache sản phẩm, đếm đăng nhập sai
    s3store/                     Lưu file S3-compatible (SeaweedFS chạy cùng máy; đổi sang AWS S3/R2 chỉ cần đổi env)
  httpapi/                       Tầng HTTP: route + quyền, DTO, parse request. KHÔNG chứa logic nghiệp vụ
    router.go                    Ghép handler + thứ tự middleware + /healthz, /readyz
    *_handler.go, dto.go, request.go
    response/                    Format response chung + map lỗi → HTTP status
    middleware/                  request id, access log, recover, CORS, timeout, xác thực + phân quyền
  worker/                        Job nền định kỳ (dọn file rác, xoá token hết hạn)
  config/, logger/
migrations/                      *.sql, nhúng vào binary (go:embed)
deploy/vps/                      Deploy lên nhiều VPS sau Cloudflare: chống DDoS, DB có bản sao + failover, backup, cảnh báo (xem README trong đó)
scripts/smoke.sh                 Kiểm tra end-to-end trên cụm thật
```

| Tầng | Tương đương NestJS |
|---|---|
| `httpapi/*_handler.go` + `dto.go` | Controller + DTO |
| `httpapi/middleware.Authenticate` | Guard (`@UseGuards`, `@Roles`) |
| `httpapi/response` | Exception Filter + Interceptor định dạng response |
| `service` | Service (Provider) |
| `repository` | Repository (TypeORM/Prisma) |
| `domain` | Entity + class-validator |
| `cmd/api/main.go` | `main.ts` + `AppModule` |

### Khi có nhiều bảng

Thêm 1 bảng/tính năng (ví dụ đơn hàng) = thêm 1 file ở mỗi tầng, không sửa code cũ:

1. `migrations/004_orders.sql` (app tự chạy lần khởi động sau)
2. `domain/order.go`: entity + Validate + lỗi nghiệp vụ
3. `service/ports.go`: thêm `OrderRepository`; `service/order.go`: logic
4. `repository/postgres/order.go`: SQL, luôn dùng `db(ctx, r.pool)` (để chạy được trong transaction)
5. `httpapi/order_handler.go`: route + quyền (`g.User`, `g.Admin`) ngay trong `Register`
6. Ghép trong `main.go` + 1 dòng `NewOrderHandler(...).Register(mux, g)` trong `router.go`

**Thao tác ghi nhiều bảng phải cùng thành công/cùng thất bại** → dùng `TxManager`, repository không cần biết:

```go
err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
    order, err := s.orders.Create(ctx, o)               // bảng orders
    if err != nil { return err }
    _, err = s.products.Purchase(ctx, o.ProductID, o.Qty) // bảng products — lỗi → rollback cả đơn hàng
    return err
})
```

Ví dụ đang chạy thật: `AuthService.Refresh` (thu hồi token cũ + ghi token mới trong 1 transaction).

**Bảng dùng chung (như `assets`)**: chỉ chứa thông tin file; bảng nào dùng file thì trỏ tới bằng foreign key thật
(`users.avatar_asset_id`, bảng nối `product_images`). Không dùng cột `owner_type`/`owner_id` (polymorphic):
DB không đặt được foreign key cho kiểu đó nên không chặn được dữ liệu mồ côi. Thêm bảng mới trỏ tới `assets`
→ thêm 1 dòng `NOT EXISTS` trong điều kiện dọn rác (`repository/postgres/asset.go`).

## API

Quyền: **công khai** (không cần token), **đăng nhập** (`Authorization: Bearer <access_token>`), **admin**.

| Method | Path | Quyền | Ghi chú |
|---|---|---|---|
| `POST` | `/api/v1/auth/register` | công khai | `{"email","password","name"}` → 201, vai trò `customer` |
| `POST` | `/api/v1/auth/login` | công khai | `{"email","password"}` → `access_token` (15 phút) + `refresh_token` (30 ngày) |
| `POST` | `/api/v1/auth/refresh` | công khai | `{"refresh_token"}` → cặp token mới; token cũ hết dùng được |
| `POST` | `/api/v1/auth/logout` | công khai | `{"refresh_token"}` → 204 |
| `GET` | `/api/v1/me` | đăng nhập | Thông tin tài khoản + `avatar_url` |
| `PUT` | `/api/v1/me/avatar` | đăng nhập | `{"asset_id"}` (file chính mình upload) |
| `POST` | `/api/v1/assets` | đăng nhập | `{"content_type","size"}` → `upload_url` (xem "Upload ảnh") |
| `GET` | `/api/v1/products` | công khai | Lọc, sắp xếp, search, phân trang (bên dưới) |
| `GET` | `/api/v1/products/{id}` | công khai | Cache Redis + singleflight; có `images[].url` |
| `POST` | `/api/v1/products/{id}/purchase` | đăng nhập | `{"quantity":1}`; hết hàng → 409, không bao giờ âm kho |
| `POST` | `/api/v1/products` | admin | `{"sku","name","description","price","stock"}` → 201 + header `Location` |
| `PUT` | `/api/v1/products/{id}` | admin | Gửi đủ field + `version` đọc được lần cuối; version cũ → 409 |
| `DELETE` | `/api/v1/products/{id}` | admin | 204 |
| `POST` | `/api/v1/products/{id}/images` | admin | `{"asset_id"}`, tối đa 10 ảnh |
| `DELETE` | `/api/v1/products/{id}/images/{asset_id}` | admin | Gỡ ảnh (file được dọn sau 1 giờ) |
| `GET` | `/healthz`, `/readyz` | công khai | Process sống (503 khi đang tắt) / kết nối được DB |

### Đăng nhập

- Mật khẩu hash bằng bcrypt. Sai email hay sai mật khẩu đều trả cùng lỗi `INVALID_CREDENTIALS` (không dò được email).
- Sai 10 lần / 15 phút / email → `429 TOO_MANY_ATTEMPTS` (đếm trong Redis, dùng chung mọi instance).
- Access token (JWT) sống 15 phút, không lưu DB. Refresh token lưu **hash** trong DB, dùng 1 lần rồi đổi token mới.
  Token cũ bị dùng lại (dấu hiệu bị đánh cắp) → thu hồi mọi phiên của tài khoản đó.
- Admin đầu tiên: đặt `ADMIN_EMAIL` + `ADMIN_PASSWORD`, app tự tạo lúc khởi động.

### Upload ảnh

File đi **thẳng từ client lên storage**, không qua API (không tốn băng thông/CPU của API và load balancer):

```
1. POST /api/v1/assets {"content_type":"image/png","size":20480}
   → {"asset_id": 7, "upload_url": "https://...", "method": "PUT", "headers": {"Content-Type": "image/png"}}
2. PUT <upload_url> với đúng các "headers", body là file (đúng size đã khai; sai → storage từ chối)
3. POST /api/v1/products/{id}/images {"asset_id": 7}   hoặc   PUT /api/v1/me/avatar {"asset_id": 7}
   → API kiểm tra file đã lên, đúng kích thước, đúng định dạng thật (đọc byte đầu file, không tin client)
```

Code trình duyệt:

```js
const up = (await api.post('/api/v1/assets', { content_type: file.type, size: file.size })).data
await fetch(up.upload_url, { method: 'PUT', headers: up.headers, body: file })
await api.post(`/api/v1/products/${id}/images`, { asset_id: up.asset_id })
```

Chỉ nhận JPEG/PNG/WebP ≤ 5MB (không SVG: SVG chứa được JavaScript). File xin upload rồi bỏ đó quá 24 giờ, hoặc
không còn ai dùng quá 1 giờ, được job nền xoá khỏi storage.
### Lọc, sắp xếp, phân trang

`GET /api/v1/products?q=giày chạy&min_price=100000&max_price=500000&in_stock=true&sort=price&limit=20`

| Tham số | Ý nghĩa |
|---|---|
| `q` | Search theo tiền tố từ: `điện th` khớp "Điện thoại" (phải gõ đúng dấu) |
| `min_price`, `max_price` | Khoảng giá (VND) |
| `in_stock=true` | Chỉ sản phẩm còn hàng |
| `sort` | `-created_at` (mới nhất, mặc định), `created_at`, `price`, `-price`, `relevance` (mặc định khi có `q`) |
| `limit` | 1–100, mặc định 20 |
| `cursor` | Lấy từ `meta.next_cursor` của trang trước; giữ nguyên các tham số khác |

Phân trang bằng cursor (keyset) thay vì `page/offset`: trang thứ 10.000 nhanh như trang 1, và không bị trùng/sót
dòng khi có sản phẩm mới được thêm giữa 2 lần tải trang. Không trả tổng số dòng (`COUNT(*)` trên bảng lớn rất chậm).

## Format response

```jsonc
// Thành công
{"data": {"id": 1, "sku": "A1", "name": "Áo thun", "price": 150000, "stock": 10, "version": 1, ...}}

// Danh sách
{"data": [...], "meta": {"limit": 20, "has_more": true, "next_cursor": "eyJzIjoi..."}}

// Lỗi: luôn có code (để code phía client xử lý) và request_id (để tra log khi báo lỗi)
{"error": {"code": "VALIDATION_FAILED", "message": "request has invalid fields",
           "details": [{"field": "price", "message": "must be >= 0"}], "request_id": "4f27f9b0..."}}
```

Mọi response có header `X-Request-ID` (client/LB gửi lên thì dùng lại, không thì server tự sinh).

### Mã lỗi

| HTTP | `code` | Khi nào |
|---|---|---|
| 400 | `VALIDATION_FAILED` | Field sai; `details` liệt kê **tất cả** field sai |
| 400 | `INVALID_JSON`, `BODY_TOO_LARGE` | JSON hỏng / có field lạ / body > 1MB |
| 400 | `INVALID_ID`, `INVALID_CURSOR` | id không phải số dương / cursor bị sửa hoặc không khớp `sort` |
| 400 | `ASSET_MISMATCH` | File upload khác loại/kích thước đã khai (hoặc không phải ảnh thật) |
| 401 | `UNAUTHENTICATED` | Thiếu access token / token sai / hết hạn → gọi `/auth/refresh` |
| 401 | `INVALID_CREDENTIALS`, `INVALID_REFRESH_TOKEN` | Sai email/mật khẩu; refresh token sai/hết hạn/đã dùng → đăng nhập lại |
| 403 | `FORBIDDEN` | Đã đăng nhập nhưng không đủ quyền (vd khách tạo sản phẩm) |
| 404 | `PRODUCT_NOT_FOUND`, `USER_NOT_FOUND`, `ASSET_NOT_FOUND`, `IMAGE_NOT_FOUND`, `ROUTE_NOT_FOUND` | |
| 409 | `SKU_TAKEN`, `EMAIL_TAKEN` | Trùng SKU / email đã đăng ký |
| 409 | `VERSION_CONFLICT` | Người khác đã sửa trước; đọc lại rồi sửa tiếp |
| 409 | `OUT_OF_STOCK` | Không đủ hàng |
| 409 | `ASSET_NOT_UPLOADED`, `TOO_MANY_IMAGES` | Chưa PUT file lên `upload_url` / sản phẩm đã đủ 10 ảnh |
| 429 | `TOO_MANY_ATTEMPTS` | Đăng nhập sai quá nhiều, thử lại sau 15 phút |
| 503 | `SERVICE_UNAVAILABLE` | Quá tải (quá `REQUEST_TIMEOUT`); thử lại sau |
| 500 | `INTERNAL_ERROR` | Lỗi không lường trước; chi tiết chỉ nằm trong log (tra bằng `request_id`) |

## Cấu hình (biến môi trường)

| Biến | Mặc định | |
|---|---|---|
| `ADDR` | `:8080` | |
| `DATABASE_URL` | `postgres://app:app@localhost:5433/products?sslmode=disable` | |
| `DATABASE_READ_URL` | rỗng | Standby để đọc search/danh sách; chỉ dùng khi nó chậm hơn primary < 16MB, không thì đọc primary |
| `DB_MAX_CONNS` | `20` | Tổng của mọi instance phải < `max_connections` của Postgres |
| `MIGRATE_ON_START` | `true` | Tự chạy migration khi khởi động |
| `REDIS_ADDR`, `REDIS_PASSWORD` | `localhost:6380`, rỗng | |
| `CACHE_TTL` | `30s` | |
| `REQUEST_TIMEOUT` | `5s` | Quá hạn → 503 |
| `SHUTDOWN_DELAY` | `0s` | Thời gian báo 503 ở `/healthz` trước khi tắt (sau LB: 15s) |
| `LOG_LEVEL` | `info` | `debug` để log mọi request |
| `CORS_ORIGINS` | rỗng (tắt) | `https://shop.vn,https://admin.shop.vn` hoặc `*` |
| `JWT_SECRET` | **bắt buộc** | ≥ 32 ký tự ngẫu nhiên (`openssl rand -hex 32`). Đổi = mọi access token cũ hết hiệu lực |
| `ACCESS_TOKEN_TTL`, `REFRESH_TOKEN_TTL` | `15m`, `720h` | |
| `BCRYPT_COST` | `12` | 10–14; cao hơn = đoán mật khẩu khó hơn nhưng đăng nhập tốn CPU hơn |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | rỗng | Đặt cả 2 → tự tạo admin đầu tiên lúc khởi động |
| `STORAGE_ENDPOINT` | `localhost:9000` | Địa chỉ API gọi storage |
| `STORAGE_PRESIGN_ENDPOINT` | = `STORAGE_ENDPOINT` | Địa chỉ client upload trong `upload_url`, vd `localhost:9000` (dev) hoặc `https://shop.vn` (VPS, qua reverse proxy) |
| `STORAGE_REGION` | `us-east-1` | |
| `STORAGE_ACCESS_KEY`, `STORAGE_SECRET_KEY` | khoá dev | |
| `STORAGE_BUCKET` | `products` | |
| `STORAGE_USE_SSL` | `false` | |
| `STORAGE_PUBLIC_URL` | `http://localhost:9000/products` | URL công khai/CDN để ghép link ảnh |

## Chạy

```powershell
cd 04-products
docker compose --profile full up -d --build   # 3 API + Nginx (load balancer) ở http://localhost:8080
Get-Content scripts/seed.sql | docker compose exec -T postgres psql -U app -d products   # 200k sản phẩm mẫu

# Hoặc 1 instance để debug: docker compose up -d   rồi ở thư mục gốc:
#   $env:JWT_SECRET="dev-only-secret-do-not-use-in-production"; go run ./04-products/cmd/api
```

Thêm migration: tạo `migrations/004_ten_gi_do.sql`; lần khởi động sau app tự chạy (đã chạy thì bỏ qua).

## Test

```powershell
go test ./04-products/...                      # unit test, không cần Docker
$env:TEST_DATABASE_URL="postgres://app:app@localhost:5433/products?sslmode=disable"
go test ./04-products/... -count=1             # + integration trên Postgres thật: tranh mua, tranh sửa, phân trang,
                                               #   refresh token song song, gắn ảnh, dọn file rác
sh scripts/smoke.sh                            # end-to-end trên cụm đang chạy (Git Bash): đăng nhập, quyền, upload ảnh
```

## Load test

Chạy k6 **bên trong mạng Docker** (đi qua port của Docker Desktop trên Windows sẽ chậm và lỗi kết nối giả):

```powershell
docker run --rm --network 04-products_default -v "${PWD}/loadtest:/scripts" -e BASE_URL=http://nginx -e VUS=2000 grafana/k6 run /scripts/k6.js
```

Đo trên máy dev (2000 user ảo, 80% xem / 15% search / 5% mua, 200k sản phẩm): ~1.550 RPS, p95 8,4ms, 0% lỗi (đã gồm xác thực JWT khi mua).
