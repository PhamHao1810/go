# Deploy 04-products lên nhiều VPS. Danh sách máy + cấu hình ở deploy\vps\.env (xem README). Chạy ở thư mục gốc repo:
#   Lần đầu / thêm máy:    .\04-products\deploy\vps\deploy.ps1 -Setup
#   Cập nhật code:          .\04-products\deploy\vps\deploy.ps1
#   Quay lại bản API trước: .\04-products\deploy\vps\deploy.ps1 -Rollback   (không build, ~1 phút)
#   1 máy chết hẳn:         .\...\deploy.ps1 -Skip <IP máy chết>   (DB primary chết thì Patroni đã tự chuyển; lệnh này
#                           chỉ để chuyển kho ảnh + backup theo primary mới, không gấp)
param(
    [switch]   $Setup,
    [switch]   $Rollback,
    [string[]] $Skip = @(),
    [string]   $Stage, # chỉ ghi file của từng máy vào thư mục này (không SSH, không build) để kiểm tra
    [string]   $Key = "$HOME\.ssh\vps",
    [string]   $Config = "$PSScriptRoot\.env" # file cấu hình; origin.pem, origin.key nằm cùng thư mục với nó
)
$ErrorActionPreference = "Stop"
$here = $PSScriptRoot
$root = (Resolve-Path "$here\..\..\..").Path
$tmp = Join-Path $env:TEMP "products-vps"
New-Item -ItemType Directory -Force $tmp | Out-Null
$sshOpts = @("-i", $Key, "-o", "StrictHostKeyChecking=accept-new", "-o", "BatchMode=yes", "-o", "ConnectTimeout=15")

function Invoke-Remote([string]$login, [string]$cmd) {
    $out = & ssh @sshOpts $login $cmd
    if ($LASTEXITCODE -ne 0) { throw "Lệnh trên $login thất bại: $cmd" }
    return "$out".Trim()
}
function Send-Files([string]$login, [string[]]$files, [string]$dest) {
    & scp @sshOpts @files "${login}:$dest"
    if ($LASTEXITCODE -ne 0) { throw "Copy file lên $login thất bại" }
}
function Write-EnvFile([string]$path, [hashtable]$vars) {
    $text = ($vars.GetEnumerator() | Sort-Object Key | ForEach-Object { "$($_.Key)=$($_.Value)" }) -join "`n"
    [IO.File]::WriteAllText($path, "$text`n") # không BOM, xuống dòng LF cho Linux
}
function New-Secret {
    $b = New-Object byte[] 24
    [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b)
    return -join ($b | ForEach-Object { $_.ToString("x2") })
}
function Split-List([string]$s) { @("$s" -split "," | ForEach-Object { $_.Trim() } | Where-Object { $_ }) }

# ---------- 1. Cấu hình ----------
$envFile = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Config) # đường dẫn đầy đủ
$cfgDir = Split-Path -Parent $envFile
$cfg = @{}
if (Test-Path $envFile) {
    foreach ($line in Get-Content $envFile) { if ($line -match '^([A-Z_0-9]+)=(.*)$') { $cfg[$Matches[1]] = $Matches[2] } }
}
$generated = $false
foreach ($k in "POSTGRES_PASSWORD", "REDIS_PASSWORD", "JWT_SECRET", "STORAGE_ACCESS_KEY", "STORAGE_SECRET_KEY", "MEILI_MASTER_KEY") {
    if (-not $cfg[$k]) { $cfg[$k] = New-Secret; $generated = $true }
}
if ($generated) { Write-EnvFile $envFile $cfg; Write-Host "Đã tạo mật khẩu ngẫu nhiên trong $envFile (KHÔNG chia sẻ file này)" }

# @(...) bắt buộc: PowerShell biến danh sách 1 phần tử thành chuỗi, rồi "+" nối chuỗi thay vì nối danh sách.
$apps = @(Split-List $cfg.APP_SERVERS)
$dbs = @(Split-List $cfg.DB_SERVERS)
$all = $apps + $dbs
$problems = @()
if (-not $cfg.DOMAIN -or $cfg.DOMAIN -match '[/:]') { $problems += "DOMAIN=<tên miền>, vd shop.cua-ban.vn (không có https://)" }
if ($apps.Count -lt 1) { $problems += "APP_SERVERS=<IP app1>,<IP app2>" }
if ($dbs.Count -lt 1 -or $dbs.Count -gt 2) { $problems += "DB_SERVERS=<IP db1>,<IP db2> (1 hoặc 2 máy)" }
if ($dbs -notcontains $cfg.DB_PRIMARY) { $problems += "DB_PRIMARY=<1 IP trong DB_SERVERS>" }
if ($cfg.SEARCH_SERVER -and $dbs -notcontains $cfg.SEARCH_SERVER) { $problems += "SEARCH_SERVER=<1 IP trong DB_SERVERS> (máy chạy Meilisearch), hoặc bỏ trống để search bằng Postgres" }
if (($all | Sort-Object -Unique).Count -ne $all.Count) { $problems += "1 IP không được nằm ở 2 chỗ trong APP_SERVERS/DB_SERVERS" }
if ($Setup -and $Skip) { $problems += "-Setup cần mọi máy đang chạy (bỏ -Skip)" }
foreach ($f in "origin.pem", "origin.key") { if (-not (Test-Path "$cfgDir\$f")) { $problems += "file $f (chứng chỉ Cloudflare Origin, README bước 2)" } }
if ($problems) { throw "Thiếu/sai trong ${envFile}:`n  - $($problems -join "`n  - ")" }

# IP WireGuard cố định theo thứ tự trong .env: app trước, DB sau. Đừng đổi thứ tự các IP đã có (chỉ thêm vào cuối).
$wg = @{}
for ($i = 0; $i -lt $all.Count; $i++) { $wg[$all[$i]] = "10.10.0.$($i + 1)" }
$search = $cfg.SEARCH_SERVER # máy DB chạy Meilisearch (tuỳ chọn)
$targets = $all | Where-Object { $Skip -notcontains $_ }
# etcd (bầu primary Postgres) trên 3 máy đầu: 1 máy bất kỳ chết vẫn còn 2/3. Ít hơn 3 máy thì 1 etcd trên máy DB đầu.
$etcd = if ($all.Count -ge 3) { @($all[0..2]) } else { @($dbs[0]) }
$etcdName = @{}; foreach ($h in $etcd) { $etcdName[$h] = "e" + ($wg[$h] -replace '\.', '_') }
$etcdCluster = ($etcd | ForEach-Object { "$($etcdName[$_])=http://$($wg[$_]):2380" }) -join ","
$etcdHosts = ($etcd | ForEach-Object { "$($wg[$_]):2379" }) -join ","

if ($Rollback) { # mỗi máy app quay lại image API trước đó (update.sh giữ sẵn), lần lượt từng máy
    foreach ($h in $apps | Where-Object { $Skip -notcontains $_ }) {
        Write-Host "== Rollback $h"
        & ssh @sshOpts "deploy@$h" "sh ~/app/update.sh rollback"
        if ($LASTEXITCODE -ne 0) { throw "Rollback $h thất bại, dừng lại" }
    }
    return
}

# ---------- 2. Lần đầu / thêm máy: khoá bảo mật + mạng WireGuard ----------
if ($Setup -and -not $Stage) {
    $login = @{}; $sudo = @{}; $pub = @{}
    foreach ($h in $all) {
        $ErrorActionPreference = "Continue" # PowerShell 5.1: stderr của lệnh ngoài + Stop = lỗi giả
        & ssh @sshOpts "deploy@$h" true 2>$null
        $ErrorActionPreference = "Stop"
        if ($LASTEXITCODE -eq 0) { $login[$h] = "deploy@$h"; $sudo[$h] = "sudo " } else { $login[$h] = "root@$h"; $sudo[$h] = "" }
        Write-Host "== Tạo khoá WireGuard trên $h ($($login[$h]))"
        $pub[$h] = Invoke-Remote $login[$h] ($sudo[$h] + "sh -c 'command -v wg >/dev/null || (apt-get update -q && DEBIAN_FRONTEND=noninteractive apt-get install -yq wireguard-tools) >/dev/null 2>&1; umask 077; [ -s /etc/wireguard/private.key ] || wg genkey > /etc/wireguard/private.key; wg pubkey < /etc/wireguard/private.key'")
        if ($pub[$h] -notmatch '^[A-Za-z0-9+/]{43}=$') { throw "Khoá WireGuard của $h không hợp lệ: '$($pub[$h])'" }
    }
    for ($i = 0; $i -lt $all.Count; $i++) {
        $h = $all[$i]
        $conf = "[Interface]`nAddress = $($wg[$h])/24`nListenPort = 51820`n"
        foreach ($p in $all | Where-Object { $_ -ne $h }) {
            $conf += "`n[Peer]`nPublicKey = $($pub[$p])`nEndpoint = ${p}:51820`nAllowedIPs = $($wg[$p])/32`nPersistentKeepalive = 25`n"
        }
        [IO.File]::WriteAllText("$tmp\wg0.conf", $conf)
        $role = if ($apps -contains $h) { "app" } else { "db" }
        $reboot = if ($role -eq "app") { "0$(3 + [array]::IndexOf($apps, $h) % 6):00" } else { "" } # app1 3h, app2 4h...
        Write-Host "== Khoá bảo mật $h ($role) — vài phút"
        Send-Files $login[$h] @("$here\setup.sh", "$here\cf-ufw.sh", "$tmp\wg0.conf") ""
        & ssh @sshOpts $login[$h] "$($sudo[$h])sh ./setup.sh $role $reboot"
        if ($LASTEXITCODE -ne 0) { throw "setup.sh trên $h thất bại" }
    }
    Remove-Item "$tmp\wg0.conf"
}

# ---------- 3. Primary Postgres thật (Patroni có thể đã tự failover) ----------
# Hỏi Patroni từng máy DB; không ai trả lời (chưa có Patroni) thì tin DB_PRIMARY trong .env. Lab tự hỏi rồi ghi vào .env.
$patroni = $false
if (-not $Stage) {
    foreach ($h in $dbs | Where-Object { $Skip -notcontains $_ }) {
        $code = Invoke-Remote "deploy@$h" "curl -s -m 5 -o /dev/null -w '%{http_code}' http://$($wg[$h]):8008/primary || true"
        if ($code -eq "200") {
            $patroni = $true
            if ($cfg.DB_PRIMARY -ne $h) {
                Write-Host "Patroni đã tự failover: primary hiện là $h (.env ghi $($cfg.DB_PRIMARY)) → cập nhật DB_PRIMARY"
                $cfg.DB_PRIMARY = $h
                Write-EnvFile $envFile $cfg
            }
        }
    }
}
if ($Skip -contains $cfg.DB_PRIMARY) { throw "máy DB_PRIMARY ($($cfg.DB_PRIMARY)) đang bị bỏ qua nhưng không máy DB nào khác đang là primary (Patroni chưa failover xong? chờ 1 phút rồi chạy lại)" }
$primary = $cfg.DB_PRIMARY
$standby = $dbs | Where-Object { $_ -ne $primary } | Select-Object -First 1
$liveStandby = if ($standby -and $Skip -notcontains $standby) { $standby } else { $null }
$dbLive = @($primary) + @($liveStandby | Where-Object { $_ }) # primary đứng đầu: API nối đúng máy ngay lần thử đầu
$dbHosts = ($dbLive | ForEach-Object { "$($wg[$_]):5432" }) -join ","

# Tag image = git commit (+ "-dirty-<giờ>" nếu còn file chưa commit): biết production đang chạy code nào, rollback được.
$sha = "$(& git -C $root rev-parse --short HEAD)".Trim()
if (& git -C $root status --porcelain) { $sha += "-dirty-" + (Get-Date -Format "MMddHHmm") }

# ---------- 4. File cho từng máy ----------
$s3 = @{ identities = @(
        @{ name = "app"; credentials = @(@{ accessKey = $cfg.STORAGE_ACCESS_KEY; secretKey = $cfg.STORAGE_SECRET_KEY })
            actions = @("Admin", "Read", "Write", "List", "Tagging") },
        @{ name = "anonymous"; actions = @("Read:media") }) } # ai cũng xem được ảnh; không ai liệt kê/ghi được
[IO.File]::WriteAllText("$tmp\s3.json", ($s3 | ConvertTo-Json -Depth 5))

function Get-NodeFiles([string]$h) {
    $compose = if ($apps -contains $h) { "compose.app.yml" } else { "compose.db.yml" }
    if ($etcd -contains $h) { $compose += ":compose.etcd.yml" }
    $common = @{ DOMAIN = $cfg.DOMAIN; SELF_IP = $wg[$h]; POSTGRES_PASSWORD = $cfg.POSTGRES_PASSWORD; REDIS_PASSWORD = $cfg.REDIS_PASSWORD
        TELEGRAM_BOT_TOKEN = $cfg.TELEGRAM_BOT_TOKEN; TELEGRAM_CHAT_ID = $cfg.TELEGRAM_CHAT_ID; COMPOSE_FILE = $compose
        ETCD_NAME = $etcdName[$h]; ETCD_CLUSTER = $etcdCluster; DB_HOSTS = $dbHosts }
    if ($apps -contains $h) {
        $n = $apps.Count
        # Đọc search/danh sách: ưu tiên standby, standby chết thì nối primary (ReadRouter tự biết đó không phải standby).
        $read = if ($liveStandby) {
            "postgres://app:$($cfg.POSTGRES_PASSWORD)@$($wg[$liveStandby]):5432,$($wg[$primary]):5432/products?sslmode=disable&target_session_attrs=prefer-standby&connect_timeout=2" } else { "" }
        $vars = $common + @{ ROLE = "app"; DB_IP = $wg[$primary]; DATABASE_READ_URL = $read; API_TAG = $sha
            REDIS_ADDRS = ($dbLive | ForEach-Object { "$($wg[$_]):6379" }) -join ","
            STORAGE_BACKUP_IP = $(if ($liveStandby) { $wg[$liveStandby] } else { "" }) # Nginx đọc ảnh từ standby khi primary chết
            HC_PING_URL = $cfg.HC_PING_URL
            PEER_IPS = (@($apps | Where-Object { $_ -ne $h } | ForEach-Object { $wg[$_] }) -join ",") # Nginx chia cho API mọi máy app
            # Postgres max_connections 200: chừa 50 cho replication/backup/quản trị, 150 chia cho 2 API mỗi máy app.
            DB_MAX_CONNS = [math]::Max(5, [math]::Min(20, [math]::Floor(150 / (2 * $n))))
            # Rate limit đếm riêng từng máy → chia cho số máy để tổng cả cụm giữ ~50/giây và 30/phút mỗi IP.
            RATE_API = "$([math]::Ceiling(3000 / $n))r/m"; RATE_AUTH = "$([math]::Max(2, [math]::Ceiling(30 / $n)))r/m"
            JWT_SECRET = $cfg.JWT_SECRET; STORAGE_ACCESS_KEY = $cfg.STORAGE_ACCESS_KEY; STORAGE_SECRET_KEY = $cfg.STORAGE_SECRET_KEY
            ADMIN_EMAIL = $cfg.ADMIN_EMAIL; ADMIN_PASSWORD = $cfg.ADMIN_PASSWORD; CORS_ORIGINS = $cfg.CORS_ORIGINS
            # Máy Meilisearch đang bị -Skip (chết) → API không dùng nó, search chạy bằng Postgres.
            MEILI_URL = $(if ($search -and $Skip -notcontains $search) { "http://$($wg[$search]):7700" } else { "" })
            MEILI_KEY = $cfg.MEILI_MASTER_KEY }
        $files = "compose.app.yml", "compose.etcd.yml", "nginx.conf.tmpl", "update.sh", "monitor.sh", "$cfgDir\origin.pem", "$cfgDir\origin.key"
    } else {
        $role = if ($h -eq $primary) { "primary" } else { "standby" }
        $sb = if ($liveStandby) { $wg[$liveStandby] } else { "" }
        $vars = $common + @{ ROLE = $role; PRIMARY_IP = $wg[$primary]; STANDBY_IP = $sb; ETCD_HOSTS = $etcdHosts
            HC_BACKUP_URL = $cfg.HC_BACKUP_URL
            STORAGE_ACCESS_KEY = $cfg.STORAGE_ACCESS_KEY; STORAGE_SECRET_KEY = $cfg.STORAGE_SECRET_KEY
            PG_SHARED_BUFFERS = $cfg.PG_SHARED_BUFFERS; PG_EFFECTIVE_CACHE_SIZE = $cfg.PG_EFFECTIVE_CACHE_SIZE
            SEARCH = $(if ($h -eq $search) { "1" } else { "" }); MEILI_MASTER_KEY = $cfg.MEILI_MASTER_KEY }
        $files = "compose.db.yml", "compose.etcd.yml", "pg-start.sh", "update.sh", "monitor.sh", "backup.sh"
    }
    Write-EnvFile "$tmp\.env" $vars
    $list = @($files | ForEach-Object { if ([IO.Path]::IsPathRooted($_)) { $_ } else { "$here\$_" } }) + "$tmp\.env"
    if ($vars.ROLE -ne "app") { $list += "$tmp\s3.json" }
    return $list
}

if ($Stage) {
    foreach ($h in $targets) {
        $d = Join-Path $Stage $h
        New-Item -ItemType Directory -Force $d | Out-Null
        Copy-Item (Get-NodeFiles $h) $d
    }
    Remove-Item "$tmp\.env", "$tmp\s3.json"
    Write-Host "Đã ghi file từng máy vào $Stage"
    return
}

# ---------- 5. Build image API đúng kiến trúc từng máy app ----------
$archOf = @{}
foreach ($h in $apps | Where-Object { $Skip -notcontains $_ }) {
    $archOf[$h] = if ((Invoke-Remote "deploy@$h" "uname -m") -eq "aarch64") { "arm64" } else { "amd64" }
}
foreach ($arch in ($archOf.Values | Sort-Object -Unique)) {
    Write-Host "== Build image linux/$arch"
    & docker buildx build --platform "linux/$arch" -f "$root\04-products\Dockerfile" -t "products-api:$sha" `
        --output "type=docker,dest=$tmp\api-$arch.tar" $root
    if ($LASTEXITCODE -ne 0) { throw "Build image $arch thất bại" }
}

# ---------- 6. Deploy, lần lượt từng máy (web không gián đoạn) ----------
# Máy app trước: dựng 2/3 etcd để Patroni máy DB có chỗ bầu. Lần đầu lên Patroni: primary cũ trước (Patroni nhận dữ
# liệu của nó làm primary), standby sau. Đã có Patroni: standby trước, primary sau → nếu container Postgres primary
# phải tạo lại thì Patroni chuyển sang standby đã cập nhật, chỉ 1 lần gián đoạn ghi ~10s.
$dbOrder = if ($patroni) { @($standby) + @($primary) } else { @($primary) + @($standby) }
$order = @($apps) + @($dbOrder | Where-Object { $_ }) | Where-Object { $Skip -notcontains $_ }
# etcd lên ở mọi máy bỏ phiếu TRƯỚC: cụm etcd mới chỉ chốt phiên bản khi đủ mọi thành viên, thiếu 1 máy thì Patroni
# không dùng được etcd (chỉ ảnh hưởng lần dựng đầu; về sau 1 máy etcd chết vẫn chạy).
foreach ($h in $etcd | Where-Object { $Skip -notcontains $_ }) {
    Send-Files "deploy@$h" (Get-NodeFiles $h) "app/"
    Invoke-Remote "deploy@$h" "cd ~/app && docker compose up -d etcd" | Out-Null
}
foreach ($h in $order) {
    Write-Host "== Deploy $h"
    Send-Files "deploy@$h" (Get-NodeFiles $h) "app/"
    if ($archOf[$h]) { Send-Files "deploy@$h" @("$tmp\api-$($archOf[$h]).tar") "app/api.tar" }
    & ssh @sshOpts "deploy@$h" "sh ~/app/update.sh"
    if ($LASTEXITCODE -ne 0) { throw "Deploy $h thất bại, dừng lại (các máy sau chưa bị đụng tới)" }
}
Remove-Item "$tmp\.env", "$tmp\s3.json", "$tmp\api-*.tar" -ErrorAction SilentlyContinue
Write-Host "Xong ($sha): https://$($cfg.DOMAIN)/healthz"
