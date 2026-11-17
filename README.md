# Go Backend: lộ trình học thực hành (từ Node.js/NestJS)

Repo học theo kiểu **làm bài cho test xanh**: đọc demo, làm bài tập, chạy test, rồi nâng cấp dần
một project thật (`03-shortener`) cho tới khi chịu được tải lớn.

```
01-basics/        Giai đoạn 1: cú pháp, struct, interface, error, generics
  demo/           đọc + chạy để hiểu khái niệm
  exercises/      6 bài, điền vào TODO cho tới khi test PASS
02-concurrency/   Giai đoạn 2: goroutine, channel, select, context, mutex
  demo/
  exercises/      5 bài
03-shortener/     Giai đoạn 3 trở đi: REST API thật (Postgres + Redis + Docker + k6)
04-products/      API sản phẩm chịu tải: CRUD, search, chống bán âm kho, 3 instance sau Nginx
```

## 0. Chuẩn bị

- Go đã được cài (`go version` ra 1.27). Nếu terminal cũ chưa nhận lệnh `go`, mở lại terminal.
- VS Code: cài extension **Go** (của Go Team at Google), rồi `Ctrl+Shift+P` → *Go: Install/Update Tools* → chọn tất cả.
- Nên cài linter: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`
- Docker Desktop (cần cho phần 03).

## 1. Giai đoạn 1: Go cơ bản (tuần 1–3)

```powershell
go run ./01-basics/demo                  # đọc code demo trước
go test ./01-basics/exercises/ -v        # ban đầu sẽ FAIL hết, đó là bình thường
go test ./01-basics/exercises/ -run TestStack -v   # chạy riêng 1 bài
```

Làm lần lượt: `slices_maps.go` → `shapes.go` → `account.go` → `stack.go`.
Song song đó, làm hết [A Tour of Go](https://go.dev/tour) và đọc [Go by Example](https://gobyexample.com).

## 2. Giai đoạn 2: Concurrency (tuần 4–6)

```powershell
go run ./02-concurrency/demo
go test ./02-concurrency/exercises/ -v
```

Làm lần lượt: `sum.go` → `timeout.go` → `counter.go` → `pool.go` → `first.go`.

Race detector: `go test -race ./...` trên Windows cần gcc (cài [MSYS2](https://www.msys2.org) + mingw-w64), hoặc chạy trong WSL.

## 3. Project 03-shortener (tuần 7 trở đi)

API rút gọn link: `POST /api/links`, `GET /{code}` (redirect), `GET /api/links/{code}` (thống kê).

### Chạy

```powershell
cd 03-shortener
docker compose up -d                     # Postgres + Redis
cd ..
go run ./03-shortener/cmd/api            # chạy API ở :8080
go test ./03-shortener/...               # unit test (không cần Docker)
```

Gọi thử (PowerShell):

```powershell
$r = Invoke-RestMethod -Method Post http://localhost:8080/api/links -ContentType 'application/json' -Body '{"url":"https://go.dev"}'
$r
curl.exe -i http://localhost:8080/$($r.code)          # 302 Found
Invoke-RestMethod http://localhost:8080/api/links/$($r.code)   # xem clicks
```

Tắt: `docker compose down` (thêm `-v` để xoá luôn dữ liệu DB).

### Đọc code theo thứ tự này

| File | Tương đương NestJS |
|---|---|
| `cmd/api/main.go` | `main.ts` + `AppModule` (wiring, graceful shutdown) |
| `internal/link/handler.go` | Controller + Exception Filter |
| `internal/link/service.go` | Service (cache-aside, interface Repository/Cache) |
| `internal/link/postgres.go`, `redis.go` | Repository (TypeORM/Prisma) |
| `internal/httpx/httpx.go` | Middleware / Interceptor |
| `internal/config/config.go` | `@nestjs/config` |
| `internal/link/link_test.go` | Jest + supertest (fake repo, httptest) |

### Load test

```powershell
go run ./03-shortener/cmd/api      # terminal 1
# terminal 2:
docker run --rm -v "${PWD}/03-shortener/loadtest:/scripts" -e BASE_URL=http://host.docker.internal:8080 grafana/k6 run /scripts/k6.js
```

Đọc kết quả: `http_reqs` (RPS), `http_req_duration` p(95), `http_req_failed`.

## 4. Bài tập nâng cấp shortener (đánh dấu khi xong)

Mỗi bài: viết code + test, chạy load test trước/sau và ghi lại số liệu.

**Giai đoạn 3: Web backend**
- [ ] `POST /api/links` cho phép `custom_code` tuỳ chọn (validate bằng `go-playground/validator`)
- [ ] Middleware Request ID (đọc/sinh header `X-Request-ID`, gắn vào log)
- [ ] Middleware API key cho các route `/api/*` (giống Guard)
- [ ] Middleware rate limit theo IP (`golang.org/x/time/rate`)
- [ ] Chuyển `migrations/` sang **goose**, chuyển `postgres.go` sang **sqlc**
- [ ] Viết OpenAPI spec và sinh docs (`oapi-codegen` hoặc `huma`)

**Giai đoạn 4: Database và cache**
- [ ] Đếm click bằng Redis `INCR`, thêm worker gom batch ghi Postgres mỗi 5s (bỏ cái UPDATE mỗi request)
- [ ] Dùng `singleflight` trong `Resolve` để chống cache stampede
- [ ] Cache link không tồn tại trong thời gian ngắn (negative cache) để chặn bị spam code rác
- [ ] Tinh chỉnh `pgxpool` (MaxConns, MinConns, MaxConnLifetime) và đo khác biệt bằng k6
- [ ] Integration test với **testcontainers-go** (Postgres/Redis thật)

**Giai đoạn 5: Hệ thống phân tán**
- [ ] Phát event `link.clicked` ra NATS hoặc Kafka; viết service `analytics` riêng để consume
- [ ] Outbox pattern khi tạo link
- [ ] Viết thêm API gRPC (`connect-go`) song song với REST

**Giai đoạn 6: Observability và deploy**
- [ ] Endpoint `/metrics` (Prometheus): RPS, latency histogram, error rate
- [ ] OpenTelemetry tracing, đẩy về Jaeger (thêm vào docker-compose)
- [ ] Bật `net/http/pprof` ở port riêng; profile CPU khi load test: `go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30`
- [ ] Grafana dashboard
- [ ] CI GitHub Actions: lint, test, build image

**Giai đoạn 7: Scale tới 100k concurrent**
- [ ] Chạy 3+ instance API sau Nginx (thêm vào compose), load test lại
- [ ] Thêm PgBouncer, 1 read replica; đọc từ replica cho `GET /api/links/{code}`
- [ ] Deploy lên Kubernetes (kind/minikube): Deployment, HPA, readiness probe, PodDisruptionBudget
- [ ] k6 tăng dần 1k → 10k → 100k VU (chạy k6 phân tán), ghi báo cáo: nút thắt nào xuất hiện ở mốc nào, đã sửa ra sao

## 5. Project 04-products: API sản phẩm chịu tải cao

CRUD sản phẩm, search, filter, phân trang cursor, chống bán âm kho, đăng nhập + phân quyền (JWT), upload ảnh
lên storage tự host, chạy nhiều instance sau load balancer,
deploy lên nhiều VPS sau Cloudflare (chống DDoS, DB có bản sao + failover, backup, cảnh báo Telegram). Kiến trúc phân tầng `domain → service → repository → httpapi`.

**Bắt đầu từ [04-products/HOC-TAP.md](04-products/HOC-TAP.md)** (tổng kết hệ thống + lộ trình học). Tra cứu: [04-products/README.md](04-products/README.md) (cấu trúc, API, format response, mã lỗi, cấu hình, test, load test)
và [04-products/deploy/vps/README.md](04-products/deploy/vps/README.md) (deploy).

## Tài liệu

- *Let's Go* và *Let's Go Further* (Alex Edwards)
- *100 Go Mistakes and How to Avoid Them* (Teiva Harsanyi)
- *Concurrency in Go* (Katherine Cox-Buday)
- *Designing Data-Intensive Applications* (Martin Kleppmann)
