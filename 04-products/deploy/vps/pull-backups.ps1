# Tải backup mới nhất (DB dump + ảnh) từ máy backup về máy bạn: bản nằm NGOÀI nhà cung cấp VPS.
# Chạy tay:      .\04-products\deploy\vps\pull-backups.ps1
# Tự động mỗi tuần (1 lần, PowerShell thường):  .\04-products\deploy\vps\pull-backups.ps1 -Schedule
# Cần key đã ssh-add (ssh-agent của Windows giữ key qua cả lần khởi động lại máy).
param(
    [string] $Dest = "$HOME\backup-vps",
    [int]    $Keep = 8, # số bản DB giữ trên máy bạn (mỗi tuần 1 bản → ~2 tháng)
    [switch] $Schedule
)
$ErrorActionPreference = "Stop"

if ($Schedule) {
    $action = New-ScheduledTaskAction -Execute "powershell.exe" `
        -Argument "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$PSCommandPath`" -Dest `"$Dest`" -Keep $Keep"
    $trigger = New-ScheduledTaskTrigger -Weekly -DaysOfWeek Sunday -At 10am
    # Máy tắt đúng giờ hẹn → chạy ngay khi bật lại.
    $settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -RunOnlyIfNetworkAvailable -ExecutionTimeLimit (New-TimeSpan -Hours 1)
    Register-ScheduledTask -TaskName "products-vps-pull-backups" -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null
    "Đã hẹn: Chủ nhật 10:00 hằng tuần → $Dest (xem/sửa trong Task Scheduler: products-vps-pull-backups)"
    return
}

$cfg = @{}
Get-Content "$PSScriptRoot\.env" | ForEach-Object { if ($_ -match '^([A-Z_]+)=(.*)$') { $cfg[$Matches[1]] = $Matches[2].Trim() } }
# Backup nằm trên máy standby (backup.sh); không có standby thì trên primary.
$src = @($cfg.DB_SERVERS -split "," | ForEach-Object { $_.Trim() } | Where-Object { $_ -and $_ -ne $cfg.DB_PRIMARY })[0]
if (-not $src) { $src = $cfg.DB_PRIMARY }
$ssh = "$env:WINDIR\System32\OpenSSH\ssh.exe"
$scp = "$env:WINDIR\System32\OpenSSH\scp.exe"
$opts = @("-o", "BatchMode=yes", "-o", "ConnectTimeout=15")
$log = Join-Path $Dest "pull.log"
New-Item -ItemType Directory -Force $Dest | Out-Null

try {
    $latest = & $ssh @opts "deploy@$src" "ls -1t backups/db-*.dump 2>/dev/null | head -1"
    if ($LASTEXITCODE -ne 0 -or -not $latest) { throw "không thấy file backup nào trên $src (backup.sh có chạy không?)" }
    & $scp -q @opts "deploy@${src}:$latest" "$Dest\"
    if ($LASTEXITCODE -ne 0) { throw "tải $latest thất bại" }
    # ponytail: chép lại toàn bộ thư mục ảnh mỗi lần; ảnh lên vài GB thì đổi sang rsync/robocopy chỉ chép file mới.
    & $scp -q -r @opts "deploy@${src}:backups/media" "$Dest\"
    if ($LASTEXITCODE -ne 0) { throw "tải ảnh thất bại" }
    Get-ChildItem "$Dest\db-*.dump" | Sort-Object Name -Descending | Select-Object -Skip $Keep | Remove-Item
    $msg = "$(Get-Date -Format s) ok $latest ($([math]::Round((Get-Item (Join-Path $Dest (Split-Path $latest -Leaf))).Length / 1MB)) MB)"
} catch {
    $msg = "$(Get-Date -Format s) LỖI $_"
    Add-Content $log $msg -Encoding utf8
    throw
}
Add-Content $log $msg -Encoding utf8
$msg
