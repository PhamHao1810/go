# Lab: 4 "máy" (app1, app2, db1, db2) chạy bằng Docker-in-Docker trên Docker Desktop. Không cần quyền admin, không cần VPS.
# Mỗi máy nhận đúng file cấu hình + update.sh như VPS thật (do deploy.ps1 -Stage tạo); IP 10.10.0.x đóng vai IP WireGuard.
# Không mô phỏng: setup.sh (khoá SSH, ufw, WireGuard) và Cloudflare.
#   .\04-products\deploy\vps\lab.ps1 up      dựng hoặc cập nhật code (lần đầu ~5 phút). Máy đang tắt sẽ được bỏ qua.
#   .\04-products\deploy\vps\lab.ps1 smoke   kiểm tra toàn bộ luồng qua app1 (thêm -Via lab-app2 để đi qua app2)
#   .\04-products\deploy\vps\lab.ps1 down    xoá sạch (cả dữ liệu)
# "Tắt máy" để tập failover: docker stop lab-db1   |   "Bật lại": docker start lab-db1, rồi lab.ps1 up
param(
    [Parameter(Mandatory = $true)] [ValidateSet("up", "smoke", "down")] [string] $Action,
    [string] $Via = "lab-app1"
)
$ErrorActionPreference = "Continue" # docker ghi tiến trình ra stderr; PowerShell 5.1 + "Stop" coi đó là lỗi
$here = $PSScriptRoot
$root = (Resolve-Path "$here\..\..\..").Path
$lab = Join-Path $here "lab"            # .env + chứng chỉ của lab (đã .gitignore)
$tmp = Join-Path $env:TEMP "products-lab" # file lớn (image) để ngoài repo, tránh OneDrive đồng bộ
$net = "lab-net"
$nodes = [ordered]@{ "lab-app1" = "10.10.0.1"; "lab-app2" = "10.10.0.2"; "lab-db1" = "10.10.0.3"; "lab-db2" = "10.10.0.4" }
$images = @{ app = "nginx:1.27-alpine"; db = "postgres:17-alpine redis:7-alpine chrislusf/seaweedfs:4.53 amazon/aws-cli:2.27.0" }

function Invoke-Checked {
    $exe, $rest = $args
    & $exe @rest
    if ($LASTEXITCODE -ne 0) { throw "Lỗi khi chạy: $($args -join ' ')" }
}
function Get-State([string]$n) { "$(docker inspect -f '{{.State.Running}}' $n 2>$null)" } # "true" | "false" | "" (chưa có)

if ($Action -eq "down") {
    foreach ($n in $nodes.Keys) { docker rm -f -v $n 2>&1 | Out-Null } # -v: xoá cả volume dữ liệu của máy, không để rác đầy ổ
    docker network rm $net 2>&1 | Out-Null
    Remove-Item -Recurse -Force $lab, $tmp -ErrorAction SilentlyContinue
    Write-Host "Đã xoá lab."
    return
}

if ($Action -eq "smoke") {
    $cfg = @{}; foreach ($l in Get-Content "$lab\.env") { if ($l -match '^([A-Z_0-9]+)=(.*)$') { $cfg[$Matches[1]] = $Matches[2] } }
    docker run --rm --network $net --add-host "shop.lab:$($nodes[$Via])" -v "$root\04-products\scripts:/s:ro" `
        -e INSECURE=1 -e BASE_URL=https://shop.lab -e ADMIN_EMAIL=$($cfg.ADMIN_EMAIL) -e ADMIN_PASSWORD=$($cfg.ADMIN_PASSWORD) `
        --entrypoint sh alpine/curl:8.12.1 /s/smoke.sh
    return
}

# ---------- up ----------
New-Item -ItemType Directory -Force $lab, $tmp | Out-Null
docker info 2>&1 | Out-Null; if ($LASTEXITCODE) { throw "Docker Desktop chưa chạy" }

# 1. Mạng + 4 máy. Máy đã có nhưng đang tắt = "máy chết", giữ nguyên để bạn tập failover.
if (-not (docker network ls -q -f "name=^$net$")) { Invoke-Checked docker network create --subnet 10.10.0.0/24 --gateway 10.10.0.254 $net | Out-Null }
$created = @()
foreach ($n in $nodes.Keys) {
    if ((Get-State $n) -eq "") {
        Write-Host "== Tạo máy $n ($($nodes[$n]))"
        # unless-stopped: Docker Desktop khởi động lại thì máy tự bật; máy bạn "docker stop" để tập failover thì vẫn tắt.
        Invoke-Checked docker run -d --privileged --restart unless-stopped --name $n --hostname $n --network $net --ip $nodes[$n] `
            -e DOCKER_TLS_CERTDIR= docker:dind | Out-Null
        $created += $n
    }
}
$running = @($nodes.Keys | Where-Object { (Get-State $_) -eq "true" })
foreach ($n in $running) {
    for ($i = 0; $i -lt 60; $i++) { docker exec $n docker info 2>&1 | Out-Null; if (-not $LASTEXITCODE) { break }; Start-Sleep 1 }
    docker exec $n mkdir -p /root/app /root/backups
}

# 2. Image: chép từ Docker trên máy bạn sang (không tải lại từ Internet). Image API build mới mỗi lần "up".
Write-Host "== Build image API"
Invoke-Checked docker build -q -f "$root\04-products\Dockerfile" -t products-api:latest $root | Out-Null
Invoke-Checked docker save -o "$tmp\api.tar" products-api:latest
foreach ($n in $created) {
    $role = if ($n -like "*app*") { "app" } else { "db" }
    foreach ($img in $images[$role] -split " ") {
        docker image inspect $img 2>&1 | Out-Null
        if ($LASTEXITCODE) { Invoke-Checked docker pull -q $img | Out-Null }
    }
    Write-Host "== Nạp image vào $n"
    cmd /c "docker save $($images[$role]) | docker exec -i $n docker load -q" # cmd: PowerShell 5.1 làm hỏng dữ liệu nhị phân qua "|"
    if ($LASTEXITCODE) { throw "Nạp image vào $n thất bại" }
}

# 3. Cấu hình lab: chỉ điền giá trị còn thiếu (giữ DB_PRIMARY bạn đã đổi khi tập failover).
$envFile = "$lab\.env"
$cfg = [ordered]@{}
if (Test-Path $envFile) { foreach ($l in Get-Content $envFile) { if ($l -match '^([A-Z_0-9]+)=(.*)$') { $cfg[$Matches[1]] = $Matches[2] } } }
$defaults = [ordered]@{ DOMAIN = "shop.lab"; APP_SERVERS = "10.10.0.1,10.10.0.2"; DB_SERVERS = "10.10.0.3,10.10.0.4"; DB_PRIMARY = "10.10.0.3"
    ADMIN_EMAIL = "admin@shop.lab"; ADMIN_PASSWORD = "admin12345"; PG_SHARED_BUFFERS = "256MB"; PG_EFFECTIVE_CACHE_SIZE = "768MB" }
foreach ($k in $defaults.Keys) { if (-not $cfg[$k]) { $cfg[$k] = $defaults[$k] } }
[IO.File]::WriteAllText($envFile, (($cfg.GetEnumerator() | ForEach-Object { "$($_.Key)=$($_.Value)" }) -join "`n") + "`n")
if (-not (Test-Path "$lab\origin.pem")) {
    # Lab không có Cloudflare: chứng chỉ tự ký thay cho chứng chỉ Origin.
    $openssl = (Get-Command openssl -ErrorAction SilentlyContinue).Source
    if (-not $openssl) { $openssl = "$env:ProgramFiles\Git\usr\bin\openssl.exe" }
    & $openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=shop.lab" -keyout "$lab\origin.key" -out "$lab\origin.pem" 2>&1 | Out-Null
    if (-not (Test-Path "$lab\origin.pem")) { throw "Không tạo được chứng chỉ (cần openssl, có sẵn trong Git for Windows)" }
}

# 4. Tạo file cho từng máy bằng chính deploy.ps1, rồi chạy update.sh như trên VPS thật.
$skip = @($nodes.Keys | Where-Object { $running -notcontains $_ } | ForEach-Object { $nodes[$_] })
if ($skip) { Write-Host "== Bỏ qua máy đang tắt: $($skip -join ', ')" }
Remove-Item -Recurse -Force "$tmp\stage" -ErrorAction SilentlyContinue
& "$here\deploy.ps1" -Stage "$tmp\stage" -Config $envFile -Skip $skip | Out-Null
if (-not $?) { throw "deploy.ps1 -Stage thất bại" }

$primary = ($cfg.DB_PRIMARY)
$order = @($nodes.Keys | Where-Object { $nodes[$_] -eq $primary }) + @($nodes.Keys | Where-Object { $_ -like "*db*" -and $nodes[$_] -ne $primary }) +
    @($nodes.Keys | Where-Object { $_ -like "*app*" })
foreach ($n in $order | Where-Object { $running -contains $_ }) {
    Write-Host "== Deploy $n"
    Invoke-Checked docker cp "$tmp\stage\$($nodes[$n])\." "${n}:/root/app/"
    if ($n -like "*app*") { Invoke-Checked docker cp "$tmp\api.tar" "${n}:/root/app/api.tar" }
    docker exec $n sh /root/app/update.sh 2>&1 | Where-Object { "$_" -match "^==|Xong|rror|FAIL" } | ForEach-Object { "   $_" }
    if ($LASTEXITCODE) { throw "update.sh trên $n thất bại. Xem log: docker exec $n sh -c 'cd /root/app && docker compose logs --tail 50'" }
}
Remove-Item -Recurse -Force "$tmp\stage", "$tmp\api.tar" -ErrorAction SilentlyContinue

Write-Host "`nLab sẵn sàng. Kiểm tra: .\04-products\deploy\vps\lab.ps1 smoke"
Write-Host "Vào 1 máy:  docker exec -it lab-db1 sh   rồi   cd /root/app && docker compose ps"
