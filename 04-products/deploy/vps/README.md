# Deploy 04-products lên nhiều VPS (50k user, chống DDoS, tự hồi phục)

```
Khách ─► Cloudflare (gói Free: chống DDoS, ẩn IP máy chủ, cache ảnh, máy chết thì tự gửi sang máy kia)
           ├─► app1: Nginx :443 ─► api1, api2 ─┐        (443 chỉ nhận IP Cloudflare)
           └─► app2: Nginx :443 ─► api1, api2 ─┤  mạng riêng WireGuard 10.10.0.0/24 (mã hoá)
                                               ├─► db1 primary: Postgres + Redis + kho ảnh
                                               └─► db2 standby: bản sao chạy liên tục + backup hằng đêm
```

| Rủi ro | Xử lý |
|---|---|
| 1 máy chết là sập web | 2 máy app; máy chết → Cloudflare tự gửi request sang máy còn lại |
| 1 máy bận việc nặng / yếu hơn | Nginx mỗi máy chia cho API của mọi máy app, chọn API đang giữ ít request nhất → tự né máy bận. Đo trên lab (app1 chỉ còn 0,3 CPU): khách vào qua app1 p95 390ms → 62ms. Máy quá tải kéo dài → Telegram báo |
| Thêm máy làm DB/rate limit/job lệch | `deploy.ps1` tự tính theo số máy: số kết nối DB mỗi API, ngưỡng rate limit mỗi máy, danh sách API cho Nginx. Search/danh sách đọc từ db2 khi db2 theo kịp db1. Job nền mỗi chu kỳ chỉ 1 instance chạy (`HOC-TAP.md` mục 5.2) |
| DB chết | db2 luôn có bản sao (chậm sau < 1 giây). Chuyển sang db2: ~1-2 phút, 1 lệnh (mục Failover) |
| DDoS | Cloudflare hấp thụ. IP thật của máy không lộ; cổng 443 chỉ nhận IP Cloudflare, gõ thẳng IP bị từ chối |
| Spam / dò mật khẩu | Rate limit theo IP thật của khách (Nginx) + khoá email sau 10 lần sai (API) + luật của Cloudflare |
| Backup nằm cùng máy DB | Backup chạy trên db2 (khác máy), giữ 14 ngày, ảnh không bao giờ bị xoá khỏi backup |
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
sudo wg show                                             # kết nối giữa các máy
```

Reboot máy DB (khi Telegram báo cần): reboot **standby** bất cứ lúc nào; **primary** thì chọn giờ vắng
(web lỗi ~1 phút) hoặc failover sang standby trước.

## Backup

db2 mỗi đêm 3h: dump DB (giữ 14 bản) + chép ảnh mới vào `~/backups`. Tất cả máy cùng 1 nhà cung cấp: nhà cung cấp
gặp sự cố lớn/khoá tài khoản là mất hết → **mỗi tuần tải backup về máy bạn**:

```powershell
scp -i $HOME\.ssh\vps -r "deploy@<IP db2>:backups" .\backup-vps
```

Khôi phục DB (trên máy primary, trong `~/app`):
`docker compose exec -T postgres pg_restore -U app -d products --clean < db-<ngày>.dump`. Đã thử: khôi phục đủ dữ liệu.

## Kiểm tra sức chịu tải (nên làm 1 lần trước khi mở bán)

Chạy trên db2, bắn thẳng vào API (không qua Cloudflare, vì Cloudflare có thể chặn như tấn công):

```powershell
scp -i $HOME\.ssh\vps 04-products\loadtest\k6.js deploy@<IP db2>:app/
ssh -i $HOME\.ssh\vps deploy@<IP db2> "docker run --rm --network host -v ~/app:/s grafana/k6 run -e BASE_URL=http://10.10.0.1:8081 -e ADMIN_EMAIL=... -e ADMIN_PASSWORD=... /s/k6.js"
```

Script **tạo dữ liệu thật** (200 sản phẩm, 1 khách): chạy trước khi mở bán rồi xoá đi. Xem `http_req_duration p(95)` và
`http_req_failed`. Trên máy dev, 3 instance API + 1 Postgres đạt ~1.550 request/giây, p95 8,4ms; 50k user thường chỉ tạo
vài trăm request/giây lúc cao điểm. Nút thắt đầu tiên sẽ là CPU máy DB (search).

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
