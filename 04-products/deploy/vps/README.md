# Deploy 04-products lên nhiều VPS (50k user, chống DDoS, tự hồi phục)

```
Khách ─► Cloudflare (gói Free: chống DDoS, ẩn IP máy chủ, cache ảnh, máy chết thì tự gửi sang máy kia)
           ├─► app1: Nginx :443 ─► api1, api2 ─┐        (443 chỉ nhận IP Cloudflare)
           └─► app2: Nginx :443 ─► api1, api2 ─┤  mạng riêng WireGuard 10.10.0.0/24 (mã hoá)
                                               ├─► db1 primary: Postgres + Redis + kho ảnh
                                               └─► db2 standby: bản sao chạy liên tục + backup hằng đêm + Meilisearch
```

| Rủi ro | Xử lý |
|---|---|
| 1 máy chết là sập web | 2 máy app; máy chết → Cloudflare tự gửi request sang máy còn lại |
| 1 máy bận việc nặng / yếu hơn | Nginx mỗi máy chia cho API của mọi máy app, chọn API đang giữ ít request nhất → tự né máy bận. Đo trên lab (app1 chỉ còn 0,3 CPU): khách vào qua app1 p95 390ms → 62ms. Máy quá tải kéo dài → Telegram báo |
| Thêm máy làm DB/rate limit/job lệch | `deploy.ps1` tự tính theo số máy: số kết nối DB mỗi API, ngưỡng rate limit mỗi máy, danh sách API cho Nginx. Search/danh sách đọc từ db2 khi db2 theo kịp db1. Job nền mỗi chu kỳ chỉ 1 instance chạy (`HOC-TAP.md` mục 5.2) |
| DB chết | db2 luôn có bản sao (chậm sau < 1 giây). Chuyển sang db2: ~1-2 phút, 1 lệnh (mục Failover) |
| DDoS | Cloudflare hấp thụ. IP thật của máy không lộ; cổng 443 chỉ nhận IP Cloudflare, gõ thẳng IP bị từ chối |
| Spam / dò mật khẩu | Rate limit theo IP thật của khách (Nginx) + khoá email sau 10 lần sai (API) + luật của Cloudflare |
| Backup nằm cùng máy DB | Backup chạy trên db2 (khác máy), giữ 7 bản DB gần nhất, ảnh không bao giờ bị xoá khỏi backup |
| Search engine chết | Meilisearch lỗi hoặc cả máy search chết (không mở được kết nối trong 0,3 giây): `/search` tự chuyển sang Postgres full-text (bỏ qua Meilisearch 5 giây rồi thử lại): vẫn tìm/mua được, chỉ mất facet, trang 2+, gõ không dấu/sai chính tả. Meilisearch chỉ *bận* (trả lời chậm) thì báo 503 thay vì đẩy tải sang Postgres. Telegram báo |
| Không ai biết khi có sự cố | Mỗi máy tự kiểm tra 5 phút/lần, báo Telegram: máy khác mất kết nối, máy quá tải CPU/RAM, container lỗi, ổ > 80%, standby ngừng nhân bản, backup không chạy, cần reboot |
| Bản vá kernel cần reboot | Máy app tự reboot lúc 3h (app1) / 4h (app2) — không cùng lúc. Máy DB: báo Telegram để reboot tay |
| Cập nhật code làm gián đoạn | Thay từng instance, từng máy. Đã đo: 0 lỗi / 15.000 request khi cập nhật dưới tải 200 request/giây |
| Lộ cổng DB/Redis | Chỉ nghe trên IP WireGuard + localhost; firewall chặn Internet; WireGuard chỉ nhận đúng các máy trong hệ thống |

## Lab: tập trên máy của bạn (0 đồng, không cần quyền admin, không cần VPS)

4 "máy" app1, app2, db1, db2 chạy bằng Docker trên Docker Desktop. Mỗi máy nhận **đúng** file cấu hình và chạy
**đúng** `update.sh` như VPS thật: Postgres nhân bản sang standby, đồng bộ ảnh, cập nhật không gián đoạn, failover, backup,
cảnh báo. Không có trong lab: `setup.sh` (khoá SSH, firewall, WireGuard) và Cloudflare. Tốn ~3GB RAM, ~6GB ổ.

```powershell
.\04-products\deploy\vps\lab.ps1 up       # dựng (lần đầu ~4 phút); sửa code xong chạy lại để cập nhật
.\04-products\deploy\vps\lab.ps1 smoke    # kiểm tra toàn bộ luồng qua app1 (-Via lab-app2 để đi qua app2)
.\04-products\deploy\vps\lab.ps1 down     # xoá sạch, kể cả dữ liệu
docker exec -it lab-db1 sh                    # "SSH" vào 1 máy, rồi: cd /root/app && docker compose ps
```

Bài tập failover (đã chạy thử: chuyển xong ~90 giây, không mất dữ liệu):

1. `docker stop lab-db1` (primary "chết"), chạy `lab.ps1 smoke` → thấy lỗi.
2. Sửa `04-products\deploy\vps\lab\.env`: `DB_PRIMARY=10.10.0.4`, chạy `lab.ps1 up` → db2 thành primary, `smoke` lại 20/20.
3. `docker start lab-db1`, chạy `lab.ps1 up` → db1 cất dữ liệu cũ sang bên (`pgdata-old-...`), chép lại từ db2, thành standby.

Bài khác: tắt 2 API trên app1 (`docker exec lab-app1 sh -c "cd /root/app && docker compose stop api1 api2"`) rồi
`smoke` vẫn qua app1 → Nginx dùng API của app2 · xoá nhầm 1 sản phẩm rồi khôi phục từ backup (`sh /root/app/backup.sh`
trên máy standby, mục Backup) · xem cảnh báo: tắt redis trên 1 máy rồi chạy `sh /root/app/monitor.sh`, xem `monitor.log`.

## Bước 1: Chuẩn bị

**4 VPS** Ubuntu 24.04 (cùng 1 nhà cung cấp, cùng khu vực để mạng giữa các máy nhanh):

| Máy | Cấu hình đề nghị cho 50k user |
|---|---|
| app1, app2 | 2–4 vCPU, 4GB RAM |
| db1, db2 | 4 vCPU, 8GB RAM, SSD ≥ 80GB (DB + ảnh + backup). VPS 4GB: sửa `shared_buffers=1GB, effective_cache_size=3GB` trong `compose.db.yml` |

Tạm ít máy cũng chạy được: 1 máy app (không chịu được máy app chết) và/hoặc 1 máy DB (không có standby, backup nằm cùng máy).

Thêm: **tên miền** + **tài khoản Cloudflare** (gói Free, không cần thẻ). Máy bạn: Docker Desktop đang chạy.

## Bước 2: Cloudflare

1. Thêm tên miền vào Cloudflare, đổi nameserver ở nơi mua tên miền theo hướng dẫn của Cloudflare.
2. **DNS**: tạo **2 bản ghi A cùng tên** (vd `shop.cua-ban.vn`), 1 trỏ IP app1, 1 trỏ IP app2, **bật đám mây cam (Proxied)**.
   Không tạo bản ghi nào trỏ thẳng IP máy DB hay để đám mây xám: lộ IP là kẻ tấn công vòng qua Cloudflare được.
3. **SSL/TLS → Overview**: chọn **Full (strict)**. **SSL/TLS → Edge Certificates**: bật **Always Use HTTPS**, **HSTS**, Minimum TLS 1.2.
4. **SSL/TLS → Origin Server → Create Certificate** (RSA, hạn 15 năm). Lưu phần Certificate vào
   `04-products\deploy\vps\origin.pem`, Private key vào `04-products\deploy\vps\origin.key` (đã có trong `.gitignore`, **không chia sẻ**).
5. **Security**: bật **Bot Fight Mode**. **Security → WAF → Rate limiting rules** (gói Free được 1 luật): URI path chứa
   `/api/v1/auth/`, 20 request / 10 giây / IP → Block. Bị tấn công: bật **Under Attack Mode** ở trang Overview.

## Bước 3: SSH key + cấu hình

```powershell
ssh-keygen -t ed25519 -f $HOME\.ssh\vps          # đặt passphrase
# Script chạy SSH không hỏi gì, nên key có passphrase phải được nạp sẵn vào ssh-agent:
#   (PowerShell admin, 1 lần) Get-Service ssh-agent | Set-Service -StartupType Automatic; Start-Service ssh-agent
#   (mỗi lần khởi động máy)   ssh-add $HOME\.ssh\vps
# Với TỪNG máy (hỏi mật khẩu root lần cuối):
Get-Content $HOME\.ssh\vps.pub | ssh root@<IP> "mkdir -p ~/.ssh && cat >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"
```

Tạo `04-products\deploy\vps\.env` (script tự thêm mật khẩu DB/Redis/JWT/storage ngẫu nhiên):

```
DOMAIN=shop.cua-ban.vn
APP_SERVERS=<IP app1>,<IP app2>
DB_SERVERS=<IP db1>,<IP db2>
DB_PRIMARY=<IP db1>
ADMIN_EMAIL=ban@email.com
ADMIN_PASSWORD=<mật khẩu mạnh, 12+ ký tự>
TELEGRAM_BOT_TOKEN=<tuỳ chọn: token từ @BotFather>
TELEGRAM_CHAT_ID=<tuỳ chọn: chat id của bạn>
```

- Thứ tự IP quyết định IP nội bộ (10.10.0.1, .2...). **Thêm máy thì thêm vào cuối, đừng đổi thứ tự.**
- Telegram (miễn phí): chat với @BotFather → `/newbot` lấy token; nhắn bot 1 tin rồi mở
  `https://api.telegram.org/bot<token>/getUpdates` lấy `chat.id`.
- `ADMIN_*` tạo admin đầu tiên lúc khởi động; tạo xong nên xoá rồi deploy lại.
- **File `.env` chứa mọi mật khẩu: không chia sẻ, không commit, giữ 1 bản sao an toàn** (mất file = không deploy tiếp được).

## Bước 4: Khoá bảo mật + deploy

```powershell
.\04-products\deploy\vps\deploy.ps1 -Setup     # lần đầu, hoặc khi thêm máy (vài phút mỗi máy)
.\04-products\deploy\vps\deploy.ps1            # các lần sau: cập nhật code
```

`-Setup` trên từng máy: cập nhật OS + tự vá bảo mật, tạo user `deploy`, SSH chỉ bằng key (cấm root, cấm mật khẩu),
fail2ban, firewall, WireGuard, Docker (giới hạn log), swap 2GB. Lần đầu đăng nhập bằng root; sau đó root bị cấm SSH,
script tự dùng user `deploy`.

Thử: mở `https://shop.cua-ban.vn/healthz`, rồi kiểm tra toàn bộ luồng bằng Git Bash:
`BASE_URL=https://shop.cua-ban.vn ADMIN_EMAIL=... ADMIN_PASSWORD=... sh 04-products/scripts/smoke.sh`

Nếu PowerShell báo không được chạy script: `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`.

**Migration DB** chạy lúc instance mới khởi động, khi instance cũ vẫn phục vụ, nên phải tương thích ngược:
thêm cột/bảng thoải mái; đổi tên/xoá cột thì tách 2 lần deploy.

## Search: Meilisearch (tuỳ chọn)

Thêm vào `.env`: `SEARCH_SERVER=<IP 1 máy trong DB_SERVERS>` (vd db2), rồi `deploy.ps1`. Máy đó chạy Meilisearch (chỉ nghe
trên WireGuard, master key tự sinh `MEILI_MASTER_KEY`), API của mọi máy app trỏ tới nó. Bỏ trống = search bằng Postgres.

- Đồng bộ: trigger ghi `search_outbox` cùng transaction với thay đổi (sản phẩm, biến thể, tag, thuộc tính, ảnh, đổi tên
  hãng/danh mục) → job `search-sync` đẩy lên Meilisearch, trễ ~1-2 giây. Meilisearch chết thì outbox giữ lại, lên lại thì tự bù.
- Dựng lại toàn bộ index (sau khi seed dữ liệu lớn, đổi máy search, index hỏng) — search không gián đoạn (index phụ rồi swap):
  `ssh deploy@<IP app1> "cd app && docker compose exec -T api1 /api reindex"`
- Máy search chết hẳn: đổi `SEARCH_SERVER` sang máy DB kia (hoặc bỏ trống) → `deploy.ps1` → `reindex` như trên.

## Seed dữ liệu lớn (load test)

`scripts/seed-catalog.sql` sinh N sản phẩm (mặc định 1 triệu) kèm danh mục 3 cấp, 500 hãng, biến thể, tag, thuộc tính,
20.000 khách, đánh giá, đơn hàng. Chạy trên db1 (~30-60 phút với VPS 2 CPU), rồi reindex:

```powershell
scp 04-products\scripts\seed-catalog.sql deploy@<IP db1>:
ssh deploy@<IP db1> "cd app && docker compose exec -T postgres psql -U app -d products -v n=1000000 < ~/seed-catalog.sql"
ssh deploy@<IP app1> "cd app && docker compose exec -T api1 /api reindex"
```

Chạy lại sau khi bị ngắt giữa chừng: tự làm tiếp từ sản phẩm cuối cùng. Xoá hết: `scripts/unseed-catalog.sql` (cùng cách
chạy), rồi reindex. Load test kiểu khách thật trên dữ liệu này: `loadtest/k6-catalog.js` (đầu file có hướng dẫn).

## Failover: máy DB primary chết

Telegram báo "mất kết nối WireGuard tới 10.10.0.3" và web trả lỗi 500. Kiểm tra máy đó chết thật (trang quản lý VPS),
rồi chuyển sang standby:

1. Sửa `.env`: `DB_PRIMARY=<IP db2>`.
2. `.\04-products\deploy\vps\deploy.ps1 -Skip <IP db1>` — db2 thành primary, các máy app trỏ sang db2. Đã đo: ~80 giây,
   không mất dữ liệu đã ghi (chỉ có thể mất vài thao tác trong giây cuối trước khi primary chết).
3. Khi db1 sống lại: `.\04-products\deploy\vps\deploy.ps1` (không -Skip). db1 tự cất dữ liệu cũ sang bên
   (`pgdata-old-<thời gian>`, xoá khi chắc chắn không cần), chép lại toàn bộ từ db2 và thành standby mới.

**Đừng** chuyển khi primary chỉ chập chờn vài giây: 2 máy cùng nhận ghi là dữ liệu tách đôi. Chắc chắn máy chết/không cứu được mới làm.

## Vận hành

```powershell
ssh -i $HOME\.ssh\vps deploy@<IP>
cd app
docker compose ps                                        # trạng thái
docker compose logs -f --tail 100 api1 api2              # log API (máy app)
docker compose logs nginx | grep ' 429 '                 # ai bị rate limit (máy app)
docker compose exec postgres psql -U app products        # vào DB (máy DB)
cat monitor.log                                          # lịch sử cảnh báo
crontab -l                                               # phải có monitor.sh (mọi máy) + backup.sh (standby)
sudo wg show                                             # kết nối giữa các máy
```

Xem CPU/steal/RAM/mạng/từng container của mọi máy gần như real time (~3 giây/lần), từ thư mục gốc repo:
`bash 04-products/deploy/vps/watch.sh` (Git Bash hoặc PowerShell; key đã `ssh-add`). `steal` cao = nhà cung cấp lấy bớt CPU.

Reboot máy DB (khi Telegram báo cần): reboot **standby** bất cứ lúc nào; **primary** thì chọn giờ vắng
(web lỗi ~1 phút) hoặc failover sang standby trước.

## Backup

db2 mỗi đêm 3h: dump DB (giữ 7 bản) + chép ảnh mới vào `~/backups`. Tất cả máy cùng 1 nhà cung cấp: nhà cung cấp
gặp sự cố lớn/khoá tài khoản là mất hết → **mỗi tuần tải backup về máy bạn** (bản DB mới nhất + ảnh, giữ 8 bản):

```powershell
.\04-products\deploy\vps\pull-backups.ps1            # tải ngay về $HOME\backup-vps (xem pull.log)
.\04-products\deploy\vps\pull-backups.ps1 -Schedule  # 1 lần: Task Scheduler tự tải Chủ nhật 10:00 (máy tắt thì tải khi bật lại)
```

Khôi phục DB (trên máy primary, trong `~/app`):
`docker compose exec -T postgres pg_restore -U app -d products --clean < db-<ngày>.dump`. Đã thử trên VPS thật (2026-11-20): bản 280MB
(1 triệu sản phẩm) khôi phục vào Postgres tạm mất 134 giây, số dòng mọi bảng khớp primary.

## Kiểm tra sức chịu tải (nên làm 1 lần trước khi mở bán)

k6 chạy trên máy bạn, bắn **thẳng vào Nginx 2 máy app** (bỏ qua Cloudflare vì nó chặn như tấn công), mỗi VU giả 1 IP
khách qua `CF-Connecting-IP` để rate limit tính như thật. Dữ liệu: seed 1 triệu sản phẩm (mục Seed dữ liệu lớn).

1. Mở tạm cổng 443 cho IP nhà bạn trên **mọi máy app** (mạng nhà 2 đường internet = 2 IP: chạy `curl https://api.ipify.org`
   vài lần để biết hết): `sudo ufw insert 1 allow proto tcp from <IP nhà> to any port 443 comment k6-temp`
2. Mỗi máy app 1 tiến trình k6, mỗi tiến trình một nửa số VU (PowerShell, thư mục gốc repo):
   ```powershell
   $env:BASE_URL="https://<domain>"; $env:INSECURE="1"; $env:FAKE_IPS="1"; $env:PRODUCT_MIN="<id SD- nhỏ nhất>"; $env:PRODUCT_MAX="<lớn nhất>"
   $env:RESOLVE="<domain>=<IP app1>"; k6 run --stage 45s:500,90s:500,10s:0 04-products\loadtest\k6-catalog.js
   # cửa sổ khác: giống hệt, RESOLVE trỏ <IP app2>. NO_SEARCH=1 để bỏ lượt tìm kiếm (đo riêng API + Postgres).
   ```
3. Cửa sổ thứ 3: `watch.sh` xem máy nào lên đỉnh trước. Xong: `sudo ufw delete allow proto tcp from <IP nhà> to any port 443`.

Script **tạo dữ liệu thật** (khách `k6cat-*@test.vn`, đơn hàng trừ kho sản phẩm seed): xoá bằng `unseed-catalog.sql`.

**Đã đo (2026-11-20, 1 triệu sản phẩm; app 2 vCPU/2GB, DB 2 vCPU/4GB, db1 bị steal ~24% CPU):**

| Kịch bản | Ổn (p95 đạt ngưỡng) | Không lỗi nhưng chậm | Sập | Nút thắt |
|---|---|---|---|---|
| Có search (40% lượt) | ~1.000 VU, 400 req/s, 0,05% lỗi | — | 1.500 VU: 29% lỗi (search 503) | Meilisearch hết 2 core db2 |
| Không search | ~1.000 VU, 440 req/s | 2.000 VU, p95 chi tiết 2,1s | 3.000 VU: 47% lỗi | Postgres hết 2 core db1 |

Năng lực theo core: **Postgres primary ~300 req/s/core** (đọc chi tiết + đặt hàng), **Meilisearch ~80 lượt search/s/core**
(1 triệu document, facet), **máy app** dùng < 50% CPU ở mọi mức (Nginx + 2 API ~ 0,5 core cho 600 req/s).
1 VU ≈ 0,4–0,5 req/s (khách nghỉ 1–3 giây giữa 2 thao tác). Muốn gấp đôi: thêm core cho db1 trước (hoặc đọc chi tiết
sản phẩm từ standby), rồi tách Meilisearch ra máy riêng; thêm máy app chưa giúp gì.

## Rủi ro còn lại

- **Failover DB là bấm tay** (~2 phút từ lúc bạn chạy lệnh). Tự động hoàn toàn (Patroni + etcd, cần ≥ 3 máy DB) chưa làm.
- **Redis không nhân bản**: failover làm mất cache (tự đầy lại) và bộ đếm đăng nhập sai (reset). Không mất dữ liệu.
- **Ảnh** đồng bộ sang standby gần như tức thì; ảnh upload trong vài giây cuối trước khi primary chết có thể mất.
- **Zero-downtime failover của Cloudflare** (gửi sang máy app còn lại) đã được Cloudflare mô tả cho bản ghi Proxied;
  tài liệu không nói rõ theo gói. Thử: tắt Nginx máy app1 rồi tải web vài lần, không thấy lỗi là được.
- **IP máy lộ** (vd từng trỏ DNS xám, gửi email từ máy): kẻ tấn công nhắm thẳng IP được, firewall chỉ chặn được ở mức máy.
  Lộ rồi thì đổi VPS mới (IP mới).
- **Rate limit theo IP**: khách 4G chung IP (CGNAT) có thể bị 429 khi rất đông → tăng `rate` trong `nginx.conf.tmpl`.
- **User `deploy` = quyền root** (nhóm docker + sudo). Lộ SSH key là lộ cả hệ thống: đặt passphrase, đừng copy key lung tung.
- **Access token** còn dùng được tối đa 15 phút sau khi đăng xuất (đặc tính JWT, xem README chính).
