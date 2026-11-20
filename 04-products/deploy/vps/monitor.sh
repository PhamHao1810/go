#!/bin/sh
# Cron 5 phút/lần trên MỌI máy (update.sh cài). Báo Telegram khi danh sách vấn đề THAY ĐỔI (không spam), kể cả khi
# đã ổn lại. Máy chết hẳn không tự báo được → các máy khác báo "mất kết nối WireGuard tới 10.10.0.x".
# Chưa cấu hình Telegram thì chỉ ghi vào ~/app/monitor.log.
cd "$(dirname "$0")"
val() { sed -n "s/^$1=//p" .env; }
ROLE=$(val ROLE)
now=$(date +%s)
p=""
add() { p="$p
- $1"; }

use=$(df -P / | awk 'NR==2 {sub("%", "", $5); print $5}')
[ "$use" -ge 80 ] && add "ổ cứng đã dùng $use%"
[ -f /var/run/reboot-required ] && add "cần reboot để áp bản vá kernel"
# Quá tải kéo dài (tải trung bình 5 phút > 2 x số CPU) hoặc RAM trống < 10%: Nginx máy khác đã tự né API máy này,
# nhưng khách Cloudflare gửi vào máy này vẫn chậm → nâng cấp máy, hoặc tạm tắt Proxy bản ghi DNS của máy này.
load=$(cut -d' ' -f2 /proc/loadavg)
awk -v l="$load" -v c="$(nproc)" 'BEGIN { exit !(l > 2 * c) }' && add "máy quá tải CPU: load $load / $(nproc) CPU"
awk '/^MemTotal/ {t = $2} /^MemAvailable/ {a = $2} END { exit !(a < t / 10) }' /proc/meminfo && add "RAM trống < 10%"

for c in $(docker ps -a --filter label=com.docker.compose.project=products --format '{{.Names}}|{{.State}}|{{.Status}}' |
  awk -F'|' '($2 != "running" && $1 !~ /init/) || $3 ~ /unhealthy/ {print $1}'); do
  add "container $c không chạy/không khoẻ"
done

# WireGuard gửi keepalive 25s/lần: quá 5 phút không bắt tay = máy kia chết hoặc mất mạng.
for ip in $(sudo -n wg show wg0 dump 2>/dev/null | awk -v now="$now" 'NR > 1 && $5 < now - 300 {print $4}'); do
  add "mất kết nối WireGuard tới $ip"
done

if [ "$ROLE" = standby ]; then
  st=$(docker compose exec -T postgres psql -U app -d products -tAc "SELECT status FROM pg_stat_wal_receiver" 2>/dev/null)
  [ "$st" = streaming ] || add "Postgres standby KHÔNG nhận dữ liệu từ primary (${st:-không kết nối})"
fi
if [ "$(val SEARCH)" = 1 ]; then
  curl -fsS -m 5 "http://$(val SELF_IP):7700/health" >/dev/null 2>&1 || add "Meilisearch không trả lời (search đang chạy bằng Postgres)"
fi
if [ "$ROLE" = primary ]; then # job đồng bộ search tắc → khách tìm không thấy hàng mới/giá mới
  n=$(docker compose exec -T postgres psql -U app -d products -tAc "SELECT count(*) FROM search_outbox" 2>/dev/null)
  [ "${n:-0}" -gt 50000 ] && add "search_outbox tồn $n dòng: Meilisearch đồng bộ không kịp"
fi
if crontab -l 2>/dev/null | grep -q backup.sh && [ -n "$(find .first-deploy -mmin +1560 2>/dev/null)" ] &&
  [ -z "$(find ~/backups -name 'db-*.dump' -mmin -1560 2>/dev/null)" ]; then
  add "backup DB quá 26 giờ chưa chạy (xem ~/backups/backup.log)"
fi

[ "$p" = "$(cat .monitor-state 2>/dev/null)" ] && exit 0
if [ -n "$p" ]; then msg="CẢNH BÁO $(hostname) $(val SELF_IP) ($ROLE):$p"; else msg="$(hostname) $(val SELF_IP) ($ROLE): đã ổn lại"; fi
echo "$(date '+%F %T') $msg" >> monitor.log
token=$(val TELEGRAM_BOT_TOKEN)
if [ -n "$token" ]; then
  curl -fsS -m 10 "https://api.telegram.org/bot$token/sendMessage" -d chat_id="$(val TELEGRAM_CHAT_ID)" \
    --data-urlencode text="$msg" >/dev/null || exit 1 # gửi lỗi → không lưu trạng thái, 5 phút sau gửi lại
fi
printf '%s' "$p" > .monitor-state
