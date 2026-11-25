# Deploy 04-products lên nhiều VPS (50k user, chống DDoS, tự hồi phục)

```
Khách ─► Cloudflare (gói Free: chống DDoS, ẩn IP máy chủ, cache ảnh, máy chết thì tự gửi sang máy kia)
           ├─► app1: Nginx :443 ─► api1, api2 ─┐        (443 chỉ nhận IP Cloudflare)
           └─► app2: Nginx :443 ─► api1, api2 ─┤  mạng riêng WireGuard 10.10.0.0/24 (mã hoá)
                                               ├─► db1: Postgres (Patroni) + Redis + kho ảnh
                                               └─► db2: Postgres (Patroni) + Redis + kho ảnh + backup + Meilisearch
etcd trên app1, app2, db1 (3 phiếu): Patroni giữ "khoá primary" ở đây → primary chết thì máy kia tự lên thay.
```

| Rủi ro | Xử lý |
|---|---|
| 1 máy chết là sập web | 2 máy app. Nginx/API 1 máy chết (máy còn sống) → Cloudflare chuyển ngay, 0 lỗi. **Máy chết hẳn / mất mạng → khách qua Cloudflare chờ ~20s mỗi request** (đo 2026-11-21) — xem Rủi ro còn lại |
| 1 máy bận việc nặng / yếu hơn | Nginx mỗi máy chia cho API của mọi máy app, chọn API đang giữ ít request nhất → tự né máy bận. Đo trên lab (app1 chỉ còn 0,3 CPU): khách vào qua app1 p95 390ms → 62ms. Máy quá tải kéo dài → Telegram báo |
| Thêm máy làm DB/rate limit/job lệch | `deploy.ps1` tự tính theo số máy: số kết nối DB mỗi API, ngưỡng rate limit mỗi máy, danh sách API cho Nginx. Search/danh sách đọc từ db2 khi db2 theo kịp db1. Job nền mỗi chu kỳ chỉ 1 instance chạy (`HOC-TAP.md` mục 5.2) |
| DB chết | Patroni **tự** chuyển primary sang máy kia trong ~30 giây, API tự nối máy mới (không cần deploy). Trong lúc chờ: đọc vẫn chạy (từ standby), ghi lỗi. Máy cũ sống lại tự thành standby. Mục Failover |
| DDoS | Cloudflare hấp thụ. IP thật của máy không lộ; cổng 443 chỉ nhận IP Cloudflare, gõ thẳng IP bị từ chối |
| Spam / dò mật khẩu | Rate limit theo IP thật của khách (Nginx) + khoá email sau 10 lần sai (API) + luật của Cloudflare |
| Backup nằm cùng máy DB | Backup chạy trên db2 (khác máy), giữ 7 bản DB gần nhất, ảnh không bao giờ bị xoá khỏi backup |
| Search engine chết | Meilisearch lỗi hoặc cả máy search chết (không mở được kết nối trong 0,3 giây): `/search` tự chuyển sang Postgres full-text (bỏ qua Meilisearch 5 giây rồi thử lại): vẫn tìm/mua được, chỉ mất facet, trang 2+, gõ không dấu/sai chính tả. Meilisearch chỉ *bận* (trả lời chậm) thì báo 503 thay vì đẩy tải sang Postgres. Telegram báo |
| Không ai biết khi có sự cố | Mỗi máy tự kiểm tra 5 phút/lần, báo Telegram: máy khác mất kết nối, máy quá tải CPU/RAM, container lỗi, ổ > 80%, standby ngừng nhân bản, DB đã tự failover, etcd lỗi, tỉ lệ 5xx > 2%, backup không chạy, cần reboot |
| Cả nhà cung cấp sập (không máy nào báo được) | Dịch vụ ngoài healthchecks.io: không nhận "web vẫn ổn" / "backup xong" đúng hạn thì nó báo bạn (mục Canh từ bên ngoài) |
| Deploy bản lỗi | Image gắn tag = git commit, mỗi máy giữ thêm bản trước: `deploy.ps1 -Rollback` quay lại ~1 phút, không build |
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

Bài tập failover (đã chạy thử 2026-11-21): xem ai đang là primary:
`docker exec lab-db1 sh -c "cd /root/app && docker compose exec postgres patronictl -c /tmp/patroni.yml list"`

1. Chạy đo gián đoạn (mỗi 0,5s 1 lượt đọc + 1 lượt ghi):
   `docker run --rm --network lab-net --add-host shop.lab:10.10.0.2 -v ${PWD}\04-products\scripts:/s:ro -e BASE_URL=https://shop.lab -e INSECURE=1 -e FAKE_IPS=1 --entrypoint sh alpine/curl:8.12.1 /s/probe.sh`
2. Cửa sổ khác: `docker kill lab-db1` ("rút điện" máy primary). Đã đo: db2 tự lên primary sau 28 giây; đọc lỗi 1 lượt, ghi lỗi ~29 giây.
3. `docker start lab-db1` → tự thành standby (pg_rewind), không cần làm gì. `lab.ps1 up` → kho ảnh/backup theo primary mới.

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
- Lúc search chạy bằng Postgres, mỗi instance API chỉ chạy tối đa `SEARCH_PG_LIMIT` (mặc định 2) lượt full-text cùng lúc,
  dư thì báo bận (503): đo thật, không giới hạn thì full-text kéo sập cả trang chi tiết/đặt hàng.
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

## Failover: máy DB primary chết — tự động

Postgres chạy dưới **Patroni**; "khoá primary" nằm trong etcd trên 3 máy (deploy.ps1 chọn 3 máy đầu trong .env). Cần 2/3
etcd còn sống để bầu → 1 máy bất kỳ chết vẫn tự failover.

- Primary chết / mất mạng: khoá hết hạn sau ~30 giây → standby tự lên primary. API nối "máy DB nào đang ghi được"
  (`target_session_attrs=read-write`), Redis dùng máy DB đầu còn sống, Nginx đọc ảnh từ standby: **không cần làm gì**.
  Trong ~30 giây đó: trang danh sách/search vẫn chạy (đọc standby), đặt hàng/đăng ký lỗi.
- Primary chỉ mất liên lạc (máy vẫn chạy): nó tự hạ xuống read-only khi không giữ được khoá → không bao giờ 2 máy cùng nhận ghi.
- Máy cũ sống lại: tự thành standby (pg_rewind; không được thì chép lại từ đầu). Không cần làm gì.
- Có thể mất vài thao tác ghi trong giây cuối trước khi primary chết (nhân bản bất đồng bộ). Standby chậm > 16MB thì
  Patroni **không** tự lên (thà chờ còn hơn mất nhiều dữ liệu).
- Telegram báo "Postgres đã tự failover". Khi rảnh: `deploy.ps1 -Skip <IP máy chết>` (máy chết hẳn) hoặc `deploy.ps1` (đã
  sống lại) để kho ảnh nhận upload + backup theo primary mới. Script tự hỏi Patroni ai là primary và sửa `DB_PRIMARY` trong .env.

Xem trạng thái / chuyển primary có chủ đích (vd trước khi reboot máy DB primary, gián đoạn ghi vài giây):
```
ssh deploy@<IP máy DB> "cd app && docker compose exec postgres patronictl -c /tmp/patroni.yml list"
ssh deploy@<IP máy DB> "cd app && docker compose exec postgres patronictl -c /tmp/patroni.yml switchover --force"
```

**Thay hẳn 1 máy đã chết** (mua VPS mới, IP mới): sửa IP đó **tại đúng vị trí cũ** trong `APP_SERVERS`/`DB_SERVERS` (giữ IP
nội bộ 10.10.0.x), rồi `deploy.ps1 -Setup`. Nếu máy chết là 1 trong 3 máy etcd: trước đó, trên 1 máy etcd còn sống,
`docker compose exec etcd etcdctl member list` → `member remove <id máy cũ>` → `member add e10_10_0_X --peer-urls=http://10.10.0.X:2380`,
và trên máy mới đổi `--initial-cluster-state=new` thành `existing` trong `compose.etcd.yml` cho lần chạy đầu.

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
cat deploy.log                                           # bản API nào chạy lúc nào (máy app)
```

Độ trễ thật của khách (máy app, log Nginx 1 giờ qua; `rt` = cả request, `urt` = phần API):
`docker compose logs --no-log-prefix --since 1h nginx | grep -o 'urt=[0-9.]*' | cut -d= -f2 | sort -n | awk '{a[NR]=$1} END {print "n="NR, "p50="a[int(NR*.5)], "p95="a[int(NR*.95)], "p99="a[int(NR*.99)]}'`

Quay lại bản API trước (vừa deploy bản lỗi): `.\04-products\deploy\vps\deploy.ps1 -Rollback` (chạy lại = tiến lên bản mới).

Xem CPU/steal/RAM/mạng/từng container của mọi máy gần như real time (~3 giây/lần), từ thư mục gốc repo:
`bash 04-products/deploy/vps/watch.sh` (Git Bash hoặc PowerShell; key đã `ssh-add`). `steal` cao = nhà cung cấp lấy bớt CPU.

Reboot máy DB (khi Telegram báo cần): reboot **standby** bất cứ lúc nào; **primary** thì `switchover` trước (ở trên) rồi reboot.

## Canh từ bên ngoài (healthchecks.io, miễn phí)

Mọi máy cùng 1 nhà cung cấp: nhà cung cấp sập thì không máy nào gửi được Telegram. healthchecks.io làm ngược lại: **mình**
gửi tín hiệu "vẫn ổn" cho nó, quá hạn không thấy thì **nó** báo bạn (email/Telegram).

1. Tạo tài khoản healthchecks.io, thêm Integration Telegram (hoặc email).
2. Tạo check **web**: Period 5 phút, Grace 10 phút. Copy Ping URL vào `.env`: `HC_PING_URL=https://hc-ping.com/<uuid>`.
   Mỗi máy app, mỗi 5 phút, gọi `https://<domain>/readyz` qua Cloudflare (đúng đường khách đi, có kiểm tra DB), được thì ping.
3. Tạo check **backup**: Period 1 ngày, Grace 2 giờ → `HC_BACKUP_URL=https://hc-ping.com/<uuid khác>`. backup.sh báo xong / lỗi.
4. `deploy.ps1`.

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

**Đã đo (2026-11-21, 1 triệu sản phẩm, Patroni; app 2 vCPU/2GB, DB 2 vCPU/4GB, steal 16–25% CPU khi tải cao).**
Ổn = 0% lỗi; tăng dần, mỗi mức giữ 70 giây:

| Kịch bản | Ổn | Sập | Nút thắt |
|---|---|---|---|
| 1. Có search, Meilisearch (40% lượt) | 1.000 VU, 523 req/s, p95 search 329ms | 1.250 VU: search 99% lỗi 503 (các trang khác 0%) | Meilisearch hết 2 core db2 |
| 2a. Không search | 1.000 VU, 542 req/s | 2.000 VU: 22% lỗi (chi tiết quá 3s) | Postgres hết 2 core db1 |
| 2b. Có search, Meilisearch tắt (search bằng Postgres) | 1.250 VU: chi tiết/danh mục/đặt hàng 0% lỗi; search chỉ ~25% lượt được phục vụ | — | full-text Postgres, bị giới hạn `SEARCH_PG_LIMIT` |

2b trước khi có giới hạn: 500 VU đã 8% lỗi, 1.000 VU trang chi tiết lỗi 81% (full-text ăn hết CPU cả 2 máy DB).

Năng lực theo core (vCPU VPS có steal): **Postgres primary ~300 req/s/core** khi bão hoà (~420 khi chưa), **Meilisearch
~110 lượt search/s/core** (1 triệu document, facet), **full-text Postgres ~28 search/s/core**, **máy app** ~340 req/s/core
(Nginx + 2 API, dùng < 50% CPU ở mọi mức). 1 VU ≈ 0,5 req/s. Muốn gấp đôi: thêm core db1 (hoặc đọc chi tiết từ standby),
tách Meilisearch ra máy riêng (db2 4GB đang phải swap khi chạy cả Postgres + Meilisearch); thêm máy app chưa giúp gì.

**Diễn tập hỏng máy dưới tải 600 VU (2026-11-21):**

| Sự cố | Kết quả |
|---|---|
| DB primary (kiêm Meilisearch) mất mạng 2 phút | Primary cũ tự hạ read-only sau 15s, máy kia lên primary sau 21s. Đặt hàng lỗi ~25s; chi tiết lỗi 3% (10s đầu); search giảm (Postgres, có giới hạn). Máy cũ quay lại: pg_rewind vài giây |
| DB standby (kiêm Meilisearch) reboot thật | Lên lại < 1 phút. Chỉ search giảm ~55s; mọi trang khác 0% lỗi |
| Máy app chết hẳn 2 phút | Tải vào máy app còn lại: 0% lỗi. Khách qua Cloudflare: gần như mọi request chờ ~20s |
| Nginx máy app tắt (máy còn sống) | Qua Cloudflare: 0 lỗi, trung bình 0,53s |

Độ trễ thật từ Việt Nam (trang chi tiết): qua Cloudflare p50 170ms / p95 714ms (điểm HKG); gọi thẳng máy chủ p50 85ms / p95 94ms.

## Rủi ro còn lại

- **Failover DB mất ~30 giây không ghi được** (đọc vẫn chạy). Rút ngắn được (ttl Patroni) nhưng dễ failover nhầm khi VPS bị steal CPU.
- **Upload ảnh** lỗi từ lúc primary chết tới khi bạn chạy `deploy.ps1` (kho ảnh primary/standby đổi theo deploy). Xem ảnh vẫn chạy.
- **Redis không nhân bản**: mỗi máy DB 1 Redis, API dùng máy đầu còn sống. Lúc chuyển: cache cũ tối đa 30 giây, bộ đếm đăng
  nhập sai reset. Không mất dữ liệu. Cần chặt hơn: Redis Sentinel.
- **Ảnh** đồng bộ sang standby gần như tức thì; ảnh upload trong vài giây cuối trước khi primary chết có thể mất.
- **Máy app chết hẳn → khách qua Cloudflare chờ ~20s/request** cho tới khi máy sống lại hoặc bạn tắt Proxy/xoá bản ghi DNS
  của máy đó (đo 2026-11-21; Nginx chết mà máy còn sống thì Cloudflare chuyển ngay). Sửa: Cloudflare Tunnel chạy trên cả 2
  máy app (miễn phí, máy chết thì tunnel đứt, Cloudflare bỏ ngay; không cần mở cổng 443) hoặc Cloudflare Load Balancing (trả phí).
- **Meilisearch vừa khởi động lại thì chậm vài phút** (index chưa nằm trong RAM; db2 4GB chạy chung Postgres): search báo bận.
- **IP máy lộ** (vd từng trỏ DNS xám, gửi email từ máy): kẻ tấn công nhắm thẳng IP được, firewall chỉ chặn được ở mức máy.
  Lộ rồi thì đổi VPS mới (IP mới).
- **Rate limit theo IP**: khách 4G chung IP (CGNAT) có thể bị 429 khi rất đông → tăng `rate` trong `nginx.conf.tmpl`.
- **User `deploy` = quyền root** (nhóm docker + sudo). Lộ SSH key là lộ cả hệ thống: đặt passphrase, đừng copy key lung tung.
- **Access token** còn dùng được tối đa 15 phút sau khi đăng xuất (đặc tính JWT, xem README chính).
