# Tổng kết 04-products + lộ trình học

Tài liệu này để **hiểu** hệ thống. Tra cứu API/cấu hình xem [README.md](README.md), deploy xem
[deploy/vps/README.md](deploy/vps/README.md). Đường dẫn dạng `file:dòng` tính từ thư mục `04-products/`.

## 1. Bạn đang có gì

Một backend bán hàng viết bằng Go, chạy được từ laptop tới nhiều VPS:

```
Khách ─► Cloudflare (chống DDoS, ẩn IP, cache ảnh)
           ├─► app1: Nginx ─► api1, api2 ─┐
           └─► app2: Nginx ─► api1, api2 ─┤  mạng riêng WireGuard (mã hoá)
                                          ├─► db1 primary: Postgres + Redis + kho ảnh (SeaweedFS)
                                          └─► db2 standby: bản sao liên tục + backup hằng đêm
```

| Phần | Có gì |
|---|---|
| **Sản phẩm** | CRUD, search tiếng Việt theo tiền tố, lọc giá/còn hàng, sắp xếp, phân trang cursor, mua hàng không bao giờ âm kho, 10 ảnh/sản phẩm |
| **Tài khoản** | Đăng ký, đăng nhập (JWT 15 phút + refresh token 30 ngày, xoay vòng), phân quyền customer/admin, avatar |
| **Ảnh** | Upload thẳng lên kho ảnh (không qua API), kiểm tra file thật, tự dọn file rác |
| **Chất lượng** | Format response/lỗi thống nhất, request id, log JSON, timeout, graceful shutdown, migration tự chạy |
| **Hạ tầng** | Docker; local 3 API sau Nginx; production nhiều VPS sau Cloudflare, DB có standby + failover, backup, cảnh báo Telegram, khoá bảo mật máy chủ |
| **Kiểm thử** | Unit test + integration test (Postgres thật), smoke test 20 bước, load test k6, lab 4 máy trên laptop |

Số đo được (máy dev): ~1.550 request/giây, p95 8,4ms, 0% lỗi với 2.000 người dùng ảo · cập nhật code dưới tải
200 request/giây: 0 lỗi / 15.001 · failover DB: ~90 giây, không mất dữ liệu.

Quy mô code: ~3.150 dòng Go + ~1.500 dòng test.

## 2. Bản đồ code

```
cmd/api/main.go          Ráp mọi thứ lại và chạy server (≈ main.ts + AppModule của NestJS)
internal/
  domain/                Khái niệm nghiệp vụ: Product, User, Asset, lỗi, validate. Không biết DB hay HTTP là gì
  service/               Logic nghiệp vụ. ports.go khai báo nó CẦN gì (interface), không quan tâm ai cung cấp
  repository/            Cung cấp những thứ service cần: postgres/, rediscache/, s3store/
  httpapi/               HTTP: route + quyền (router.go), đọc request, DTO, middleware, format response
  auth/                  Băm mật khẩu (bcrypt), ký/đọc JWT, tạo refresh token
  config/ logger/ worker/ Cấu hình từ env, log JSON, job chạy định kỳ
migrations/              SQL tạo bảng, nhúng vào file chạy, tự chạy lúc khởi động
deploy/vps/              Deploy nhiều VPS + lab
scripts/smoke.sh         Kiểm tra end-to-end 20 bước
loadtest/k6.js           Load test
```

**Quy tắc phụ thuộc** (điều quan trọng nhất để scale code): `httpapi → service → domain ← repository`.
Service chỉ biết interface trong `internal/service/ports.go`; Postgres/Redis/S3 cắm vào ở `cmd/api/main.go`.
Nhờ vậy: test service bằng bản giả trong RAM (`internal/service/*_test.go`), đổi R2 ↔ SeaweedFS chỉ đổi env.

## 3. Đi theo 1 request: khách bấm "Mua"

`POST /api/v1/products/7/purchase {"quantity":2}` + `Authorization: Bearer <token>`

1. **Cloudflare** lọc DDoS/bot, chuyển tới 1 máy app (máy chết → máy kia).
2. **Nginx** (`deploy/vps/nginx.conf.tmpl`): rate limit theo IP thật của khách, chọn instance API ít việc nhất.
3. **Middleware** (`internal/httpapi/router.go:30`, thư mục `middleware/`): gắn request id → ghi log → bắt panic →
   CORS → timeout 5s → `Authenticate` kiểm tra JWT, đặt người dùng vào context.
4. **Handler** (`internal/httpapi/product_handler.go`): đọc id + body (`request.go:33` chặn field lạ, body > 1MB).
5. **Service** (`internal/service/product.go:95`): gọi repository, xoá cache sản phẩm đó.
6. **Repository** (`internal/repository/postgres/product.go:78`): 1 câu `UPDATE ... WHERE stock >= 2`.
7. **Response** (`internal/httpapi/response/response.go`): `{"data": {...}}`; lỗi thì `{"error": {"code": "OUT_OF_STOCK", ...}}` + đúng HTTP status.

## 4. Các bài toán và cách giải (phần đáng học nhất)

### 4.1 Hàng nghìn người mua cùng lúc 1 món còn 5 cái
- **Sai lầm hay gặp:** đọc stock → kiểm tra trong code → ghi lại. 2 request đọc cùng lúc đều thấy còn hàng → âm kho.
- **Lời giải:** để DB kiểm tra và trừ trong **1 câu lệnh nguyên tử**:
  `UPDATE products SET stock = stock - $2 WHERE id = $1 AND stock >= $2` (`postgres/product.go:78`).
  Không trả dòng nào = hết hàng. Thêm `CHECK (stock >= 0)` ở `migrations/001_init.sql:8` làm lớp chặn cuối.
- **Tự kiểm chứng:** test `TestPostgres_ConcurrentPurchaseNeverOversells` (`postgres/product_test.go:40`).

### 4.2 Hai admin sửa cùng 1 sản phẩm
- **Vấn đề:** người lưu sau ghi đè mất thay đổi của người trước (lost update).
- **Lời giải: optimistic locking.** Mỗi sản phẩm có `version`; client gửi lại version đã đọc,
  `UPDATE ... WHERE id = $1 AND version = $7` (`postgres/product.go:56`). Version cũ → 409, client tải lại rồi sửa.
  Test: `TestPostgres_ConcurrentUpdateOnlyOneWins` (`postgres/product_test.go:71`). Mua hàng cũng tăng version để admin không ghi đè số tồn kho vừa bị trừ.

### 4.3 Sản phẩm hot: 10.000 request/giây đọc cùng 1 món
- **Cache-aside** (`service/product.go:39`): đọc Redis trước, không có mới đọc DB rồi ghi vào Redis 30s. Sửa/mua → xoá cache.
- **Cache stampede:** cache vừa hết hạn, 10.000 request cùng lao vào DB. `singleflight` gom lại: chỉ 1 request
  đọc DB, 9.999 cái kia chờ dùng chung kết quả.
- Redis lỗi → vẫn đọc DB bình thường (cache chỉ để nhanh, không được làm sập hệ thống).
- **Search/danh sách phổ biến** cũng được cache, nhưng chỉ **2 giây** theo từng bộ lọc (`service/product.go`, `List`):
  1.000 người cùng search "giày" trong 1 giây → 1 truy vấn DB. Không xoá khi sửa sản phẩm, tự hết hạn (trễ ≤ 2s).
  Đo trên lab (400 người search cùng từ khoá): CPU Postgres 230% → **7,5%**, 18.200 → 20.400 request/giây.
- Bẫy của singleflight (test bắt được): request thấy cache trống, nhưng tới lúc vào singleflight thì lượt trước đã
  xong → mở lượt mới, chạm DB lần nữa (200 request → 4 lần). Sửa: kiểm tra cache lại **bên trong** singleflight.

**Go chạy song song thế nào?** Mỗi request là 1 goroutine (~2–8KB, `net/http` tự tạo). Go xếp hàng nghìn goroutine
lên N thread = N nhân CPU; goroutine chờ DB/mạng thì nhường nhân cho goroutine khác → 1 process dùng hết mọi nhân,
không cần tự tạo thread (Node.js phải chạy nhiều process). Đo trên lab: 1 máy app dùng 4,5 nhân cùng lúc,
23.400 request/giây, 0 lỗi. Điều kiện: không có khoá chung (`Mutex`) trên đường đi của request — code này không có.

### 4.4 Search nhanh
- Cột `search` là `tsvector` tự sinh từ tên + SKU, có index GIN (`migrations/001_init.sql:13-16`).
  `q=điện th` → `điện & th:*` (khớp tiền tố từ) (`postgres/query.go:152`).
- Bài học đo được: search sắp theo độ liên quan từng đẩy Postgres lên 1000% CPU (p95 329ms) vì phải chấm điểm
  mọi dòng khớp. Giới hạn chấm tối đa 1000 dòng → p95 9,6ms.

### 4.5 Phân trang không chậm dần
- `OFFSET 100000` bắt DB đọc rồi bỏ 100.000 dòng. **Keyset/cursor**: nhớ (created_at, id) của dòng cuối,
  trang sau `WHERE (created_at, id) < (...)` → nhảy thẳng qua index (`postgres/query.go:62`, index ở `002_list_indexes.sql`).
- Cursor mã hoá base64 để client không phụ thuộc cấu trúc bên trong.

### 4.6 Nhiều bảng trong 1 giao dịch
- `TxManager.WithinTx` (`postgres/tx.go:33`) đặt transaction vào `context`; mọi repository gọi `db(ctx, pool)`
  sẽ tự dùng transaction nếu có. Service ghép nhiều repository mà không biết gì về SQL.
- Ví dụ: refresh token (`service/auth.go:84`) đọc + đánh dấu token + đọc user + tạo token mới trong 1 transaction.

### 4.7 Đăng nhập an toàn
| Mối nguy | Cách chặn | Ở đâu |
|---|---|---|
| Lộ DB là lộ mật khẩu | bcrypt cost 12 (chậm có chủ đích) | `auth/auth.go` |
| Đoán email nào tồn tại qua thời gian phản hồi | Email không tồn tại vẫn so bcrypt với hash giả; cùng 1 thông báo lỗi | `service/auth.go:55` |
| Dò mật khẩu | Khoá email 15 phút sau 10 lần sai (đếm ở Redis, mọi instance dùng chung) + rate limit Nginx | `rediscache/limiter.go` |
| Lộ refresh token | Lưu SHA-256 thay vì token gốc; mỗi lần dùng đổi token mới; token cũ bị dùng lại = bị đánh cắp → thu hồi mọi phiên | `service/auth.go:84` |
| Giả JWT | Chỉ nhận HS256 + bắt buộc hạn dùng; secret ≥ 32 ký tự bắt buộc lúc khởi động | `auth/auth.go`, `config/config.go` |
| Đăng nhập dồn dập (bcrypt ~250ms CPU/lần) làm chậm cả hệ thống | Mỗi instance chỉ chạy tối đa (số nhân/2) lượt bcrypt cùng lúc; chờ lượt > 1s → 503. Lab: khách xem sản phẩm p95 100ms → **12ms** khi bị spam đăng nhập | `service/auth.go` (`limited`) |

### 4.8 Upload ảnh mà không làm nghẽn API
1. Client xin chỗ upload (`service/asset.go:31`): API tạo tên file ngẫu nhiên + URL có chữ ký, sống 10 phút,
   **ký luôn Content-Type và kích thước** → client gửi khác là storage từ chối.
2. Client PUT file thẳng lên storage (không tốn CPU/băng thông API).
3. Gắn vào sản phẩm/avatar (`asset.go:53`): API kiểm tra file có thật, đúng kích thước, đọc 512 byte đầu để
   nhận dạng định dạng thật (chặn HTML/script giả làm ảnh), đúng người upload.
4. Job nền (`asset.go:95`) xoá file xin rồi bỏ đó > 24h hoặc không ai dùng > 1h. Khoá ngoại trong DB ngăn xoá file đang dùng.
- Thiết kế bảng: 1 bảng `assets` chung cho mọi loại ảnh, liên kết bằng khoá ngoại thật (`users.avatar_asset_id`,
  bảng `product_images`) chứ không dùng `owner_type/owner_id` (DB không kiểm tra được tính đúng).

### 4.9 Lỗi và response
- Domain trả lỗi có **loại** (Invalid, NotFound, Conflict...) + mã (`domain/errors.go`).
  `response.Error` (`response/response.go:67`) đổi loại → HTTP status. Handler không tự chọn status.
- Lỗi lạ → 500 + log đầy đủ, client chỉ thấy `INTERNAL_ERROR` + `request_id` để tra log.

### 4.10 Chạy nhiều instance an toàn
- **Migration:** mọi instance khởi động cùng lúc; `pg_advisory_lock` (`postgres/migrate.go:25`) cho 1 cái chạy, cái khác chờ.
- **Graceful shutdown** (`cmd/api/main.go`): nhận SIGTERM → `/healthz` trả 503 → ngừng nhận kết nối mới → chờ request đang chạy xong.
- **Timeout** mọi request 5s → quá tải thì trả 503 nhanh thay vì xếp hàng vô hạn.
- Không lưu trạng thái trong RAM của instance (session, cache, bộ đếm đều ở Redis/Postgres) → thêm instance là xong.

## 5. Hạ tầng: mỗi lớp giải quyết việc gì

| Lớp | Giải quyết | File |
|---|---|---|
| Cloudflare | DDoS, ẩn IP máy, cache ảnh, tự chuyển máy app khi 1 máy chết | README deploy, bước 2 |
| Firewall `ufw` | Chỉ mở 22; 443 chỉ cho IP Cloudflare (`cf-ufw.sh`, tự cập nhật hằng tuần) | `deploy/vps/setup.sh` |
| `network_mode: host` | Cổng Docker publish đi vòng qua ufw → không publish, dùng mạng của máy để ufw quản hết | `compose.*.yml` |
| WireGuard | Các máy nói chuyện qua mạng riêng mã hoá; DB/Redis chỉ nghe trên IP 10.10.0.x | `setup.sh`, `deploy.ps1` |
| Nginx | TLS, rate limit, từ chối domain lạ; chia cho cả 4 API của 2 máy theo `least_conn` (mục 5.1) | `nginx.conf.tmpl` |
| 2 instance/máy app | Thay lần lượt khi cập nhật → không gián đoạn | `update.sh` |
| Postgres streaming replication | db2 luôn là bản sao (chậm < 1s) | `pg-start.sh` |
| SeaweedFS `filer.sync` | Ảnh tự chép sang db2 | `compose.db.yml` |
| Failover | Đổi `DB_PRIMARY` + deploy; máy cũ quay lại tự cất dữ liệu và thành standby | `pg-start.sh`, `update.sh` |
| Backup | Trên db2 (khác máy primary): dump DB 14 ngày + ảnh không bao giờ xoá | `backup.sh` |
| Cảnh báo | 5 phút/lần: máy khác mất kết nối, container lỗi, ổ đầy, standby ngừng, backup không chạy | `monitor.sh` |
| Khoá máy chủ | SSH chỉ key, cấm root; fail2ban; tự vá bảo mật; giới hạn log; swap | `setup.sh` |

### 5.1 Chia tải "nhìn tình hình máy"

- **Tầng Cloudflare** chia đều 2 máy app (không biết máy nào bận) và né máy chết hẳn.
- **Tầng Nginx** mỗi máy coi 4 API (2 của mình + 2 của máy kia) là 1 nhóm, `least_conn` gửi cho API đang giữ ít
  request nhất. Máy bận trả lời chậm → request dồn ở đó → tự nhận ít request mới. Không cần biết trước máy nào mạnh.
- **Giới hạn:** máy nghẹt gần chết thì chính Nginx/TLS trên máy đó chậm, tầng Nginx không cứu được → `monitor.sh`
  báo "máy quá tải" để bạn gỡ máy khỏi DNS hoặc nâng cấp. Tự động hoá việc này cần Load Balancing trả phí của Cloudflare.

Đo trên lab (Cloudflare giả lập chia 50/50, 300 request/giây, p95 thời gian trả lời của khách vào qua app1):

| Tình trạng app1 | Trước (API máy kia chỉ dự phòng) | Sau (4 API chung nhóm) | Khách vào qua app2 |
|---|---|---|---|
| Khoẻ như app2 | — | 1,9ms | 1,9ms |
| Bận (0,3 CPU) | 390ms | **62ms** | ~2,5ms |
| Gần chết (0,15 CPU) | 4,25s, ~470 request không kịp gửi | 3,55s, 3 request lỗi | ~3–5ms |

### 5.2 Khi thêm máy: những gì tự điều chỉnh theo số máy

`deploy.ps1` đếm số máy trong `.env` rồi tính sẵn cho từng máy, bạn chỉ thêm IP:

| Rủi ro khi thêm máy | Cách giải | Kiểm chứng |
|---|---|---|
| db2 ngồi không, mọi việc đọc dồn vào db1 | Search/danh sách đọc từ standby **chỉ khi nó theo kịp** (chậm < 16MB WAL, kiểm tra 5s/lần). Standby chết/tụt lại → tự đọc primary; truy vấn đang chạy lỗi → đọc lại primary ngay (`postgres/replica.go`, `ProductRepo.List`) | Lab: 6.000 request search → standby 11.685 truy vấn, primary +10. Tắt standby giữa lúc tải: 0 lỗi / 7.501, bật lại tự quay về standby |
| Nhiều máy app vượt `max_connections` của Postgres | `DB_MAX_CONNS` = 150 ÷ (2 × số máy app), tối đa 20, tối thiểu 5 (chừa 50 cho replication/backup) | 3 máy: 20/instance (120); 8 máy: 9/instance (144) |
| Từ máy app thứ 3 Nginx chỉ biết 1 máy kia | `PEER_IPS` = mọi máy app khác; `update.sh` sinh đủ dòng `server` | 3 máy: mỗi Nginx có 6 API, `nginx -t` hợp lệ |
| Rate limit đếm riêng từng máy → kẻ spam được N lần | Ngưỡng mỗi máy = ngưỡng tổng ÷ số máy (`RATE_API`, `RATE_AUTH`) | 2 máy: 1500/phút + 15/phút mỗi máy ≈ tổng 50/giây + 30/phút |
| Job nền chạy lặp ở mọi instance | `rediscache.OncePer`: instance nào giành được khoá Redis `job:<tên>` thì chạy; Redis lỗi vẫn chạy (job an toàn khi trùng) | Test: 8 instance cùng lúc → chạy đúng 1 lần |
| DB khởi động lại / mạng chập chờn | Lỗi mạng, `57P..`, `08...` → 503 + `Retry-After` → Nginx chuyển GET sang API khác; Nginx chỉ chờ mở kết nối 500ms | Test `response_test.go` |

Bài học thật từ lúc dựng (đáng nhớ hơn lý thuyết):
- Container mới có IP mới, Nginx 10s mới hỏi DNS lại → thay 2 instance liền nhau = 6% request lỗi 502. Sửa: chờ giữa 2 lần thay.
- `sed -i` tạo file mới, container vẫn đọc file cũ (mount theo inode) → lần deploy thứ 2 Nginx âm thầm giữ cấu hình cũ. Sửa: ghi đè nội dung.
- PowerShell biến danh sách 1 phần tử thành chuỗi → `"a" + "b"` thành `"ab"`. Sửa: bọc `@(...)`.
- Rate limit theo IP: load test từ 1 máy bị chặn 76% → cần IP thật của từng khách (header Cloudflare).
- **"Hố đen":** 1 máy app mất kết nối DB, API trả 500 *ngay lập tức* → Nginx không thử lại 500, `least_conn` còn dồn thêm
  vì "trả lời nhanh" → 33–38% request lỗi dù máy kia khoẻ. Sửa: lỗi mạng tới DB/storage trả 503
  (`response/response.go`) → Nginx chuyển sang API khác và né API đó 5s → 0% lỗi. Bug thật vẫn 500, để 1 bug không làm Nginx loại hết API.
- **ReadRouter nhận nhầm primary là standby:** Postgres từng sập rồi tự phục hồi (crash recovery) thì
  `pg_last_wal_replay_lsn()` khác NULL dù là primary (đã thấy trên Postgres dev sau khi Docker bị tắt ngang).
  Sửa: hỏi thêm `pg_is_in_recovery()`. Test integration bắt được nhờ chạy trên DB vừa sập.
- Tắt standby giữa lúc tải: 0,08% lỗi vì Postgres tắt *có trật tự* trả `57P01` ("terminating connection due to
  administrator command") — không phải lỗi mạng nên không bị bắt. Thêm nhóm mã `57P`/`08` → 0 lỗi.

## 6. Lộ trình học (làm theo thứ tự)

Mỗi bước: đọc code → chạy → **tự phá thử** → xem hệ thống phản ứng thế nào.

| # | Học gì | Làm gì |
|---|---|---|
| 1 | Chạy được | `docker compose --profile full up -d --build` rồi `sh scripts/smoke.sh` (20 PASS) |
| 2 | Kiến trúc lớp | Đọc `cmd/api/main.go` từ trên xuống; với mỗi dependency, tìm interface nó thoả mãn trong `service/ports.go` |
| 3 | 1 request | Đặt breakpoint/log ở từng bước mục 3, gọi `POST /purchase` |
| 4 | Concurrency DB | Đọc mục 4.1–4.2; sửa `Purchase` thành "đọc rồi ghi" và chạy `go test ./internal/repository/postgres/` → thấy test đỏ |
| 5 | Cache | Tắt Redis (`docker compose stop redis`) → API vẫn chạy; xem log cảnh báo |
| 6 | Auth | Dùng lại refresh token cũ 2 lần → mọi phiên bị thu hồi; sai mật khẩu 11 lần → 429 |
| 7 | Upload | Theo luồng mục 4.8 bằng curl; thử upload sai kích thước, đổi đuôi file .txt thành .png |
| 8 | Hiệu năng | `loadtest/k6.js` (README chính); xoá index GIN trong DB rồi đo lại search |
| 9 | Nhiều máy | `deploy/vps/lab.ps1 up` → làm bài tập failover trong README deploy |
| 10 | Viết thêm | Thêm bảng `orders` theo hướng dẫn "Khi có nhiều bảng" trong README chính (mua hàng = trừ kho + tạo đơn trong 1 transaction) |

Lệnh test: `go test ./04-products/...` (unit) · thêm
`$env:TEST_DATABASE_URL="postgres://app:app@localhost:5433/products?sslmode=disable"` để chạy cả integration test.

## 7. Chưa có / giới hạn (để học tiếp)

| Chưa có | Hướng làm |
|---|---|
| Đăng xuất chưa vô hiệu access token ngay (còn ≤ 15 phút) | Lưu "thu hồi trước thời điểm X" theo user ở Redis, middleware kiểm tra |
| 2 tab refresh cùng lúc → bị coi là đánh cắp, đăng xuất mọi nơi | Cho phép dùng lại token cũ trong vài giây (grace period) |
| Khoá theo email bị lạm dụng để khoá người khác 15 phút | Khoá theo cặp email + IP, hoặc CAPTCHA |
| Gắn ảnh đồng thời có thể vượt 10 ảnh | Khoá dòng sản phẩm (`SELECT ... FOR UPDATE`) khi gắn |
| Quên mật khẩu, xác thực email, đơn hàng, thanh toán | Tính năng mới |
| Resize/nén ảnh | Job nền hoặc dịch vụ ảnh |
| Metrics/tracing (Prometheus, OpenTelemetry) | Giai đoạn 6 trong README gốc |
| Failover DB tự động | Patroni + etcd (≥ 3 máy DB) |
| POST mua hàng bị lỗi 503/timeout: không biết đơn đã trừ kho chưa, thử lại có thể mua 2 lần | Header `Idempotency-Key`: lưu kết quả theo key, gửi lại cùng key thì trả kết quả cũ. Trước khi có: frontend chỉ tự thử lại GET |
| DB primary vẫn là nơi ghi duy nhất | Đọc đã san sang standby (mục 5.2); ghi vượt sức 1 máy → tách bảng/sharding (xa) |
| Lab chưa thử: khoá SSH, firewall, WireGuard, Cloudflare | Chỉ kiểm được trên VPS thật |
