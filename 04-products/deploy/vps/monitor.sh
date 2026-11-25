#!/bin/sh
# Cron 5 phút/lần trên MỌI máy (update.sh cài). Báo Telegram khi danh sách vấn đề THAY ĐỔI (không spam), kể cả khi
# đã ổn lại. Máy chết hẳn không tự báo được → các máy khác báo "mất kết nối WireGuard tới 10.10.0.x".
# Cả nhà cung cấp sập (mọi máy chết cùng lúc) thì không máy nào báo được → HC_PING_URL: máy app ping dịch vụ ngoài
# (healthchecks.io) mỗi lần web qua Cloudflare trả lời đúng; quá hạn không có ping → dịch vụ đó báo bạn.
# Chưa cấu hình Telegram thì chỉ ghi vào ~/app/monitor.log.
cd "$(dirname "$0")"
val() { sed -n "s/^$1=//p" .env; }
ROLE=$(val ROLE)
SELF=$(val SELF_IP)
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

if [ -n "$(val ETCD_NAME)" ]; then # mất 2/3 etcd = Patroni không bầu được primary khi cần
  curl -fsS -m 5 "http://$SELF:2379/health" 2>/dev/null | grep -q '"health":"true"' ||
    add "etcd trên máy này không khoẻ (cần 2/3 etcd để Postgres tự failover)"
fi

if [ "$ROLE" = app ]; then
  # Tỉ lệ lỗi 5xx trong 5 phút qua (log Nginx, cột status ngay sau "request"). Ít hơn 200 request thì bỏ qua.
  r=$(docker compose logs --no-log-prefix --since 5m nginx 2>/dev/null |
    awk '/ rt=/ { for (i = 3; i < NF; i++) if ($i ~ /"$/ && $(i + 1) ~ /^[0-9][0-9][0-9]$/) { n++; if ($(i + 1) >= 500) e++; break } }
         END { if (n >= 200 && e * 100 > n * 2) printf "%d/%d", e, n }')
  [ -n "$r" ] && add "Nginx trả lỗi 5xx cao trong 5 phút qua: $r request (docker compose logs nginx | grep ' 50[0-9] ')"
  hc=$(val HC_PING_URL)
  # Đi đường của khách thật (DNS → Cloudflare → 1 máy app → API → DB). /readyz kiểm tra cả DB.
  if [ -n "$hc" ] && curl -fsS -m 10 -o /dev/null "https://$(val DOMAIN)/readyz"; then
    curl -fsS -m 10 -o /dev/null "$hc" || true
  fi
else
  # Patroni: ai là primary, standby có đang nhận dữ liệu không. Chỉ máy đang là primary báo (khỏi 2 máy báo trùng).
  st=$(docker compose exec -T postgres patronictl -c /tmp/patroni.yml list -f tsv 2>/dev/null)
  me="pg_$(echo "$SELF" | tr . _)"
  leader=$(echo "$st" | awk -F'\t' 'NR == 1 { for (i = 1; i <= NF; i++) c[$i] = i; next } $c["Role"] == "Leader" { print $c["Member"] }')
  if [ -z "$st" ]; then
    add "Patroni trên máy này không trả lời (docker compose logs postgres)"
  elif [ -z "$leader" ]; then
    add "Postgres KHÔNG có primary (web không ghi được): docker compose exec postgres patronictl -c /tmp/patroni.yml list"
  elif [ "$leader" = "$me" ]; then
    want=$(( $(val STANDBY_IP | grep -c .) + 1 ))
    echo "$st" | awk -F'\t' -v want="$want" 'NR == 1 { for (i = 1; i <= NF; i++) c[$i] = i; next }
      $c["Role"] != "Leader" && $c["State"] == "streaming" { ok++ }
      END { exit !(ok + 1 < want) }' && add "Postgres standby KHÔNG nhận dữ liệu từ primary (patronictl list: $(echo "$st" | awk -F'\t' 'NR > 1 {printf "%s %s %s; ", $2, $4, $5}'))"
    [ "$SELF" != "$(val PRIMARY_IP)" ] &&
      add "Postgres đã tự failover: primary hiện là $SELF. Web vẫn chạy; chạy deploy.ps1 (thêm -Skip <IP máy chết>) để kho ảnh + backup theo primary mới"
    # job đồng bộ search tắc → khách tìm không thấy hàng mới/giá mới
    n=$(docker compose exec -T postgres psql -U app -d products -tAc "SELECT count(*) FROM search_outbox" 2>/dev/null)
    [ "${n:-0}" -gt 50000 ] && add "search_outbox tồn $n dòng: Meilisearch đồng bộ không kịp"
  fi
fi
if [ "$(val SEARCH)" = 1 ]; then
  curl -fsS -m 5 "http://$SELF:7700/health" >/dev/null 2>&1 || add "Meilisearch không trả lời (search đang chạy bằng Postgres)"
fi
if crontab -l 2>/dev/null | grep -q backup.sh && [ -n "$(find .first-deploy -mmin +1560 2>/dev/null)" ] &&
  [ -z "$(find ~/backups -name 'db-*.dump' -mmin -1560 2>/dev/null)" ]; then
  add "backup DB quá 26 giờ chưa chạy (xem ~/backups/backup.log)"
fi

[ "$p" = "$(cat .monitor-state 2>/dev/null)" ] && exit 0
if [ -n "$p" ]; then msg="CẢNH BÁO $(hostname) $SELF ($ROLE):$p"; else msg="$(hostname) $SELF ($ROLE): đã ổn lại"; fi
echo "$(date '+%F %T') $msg" >> monitor.log
token=$(val TELEGRAM_BOT_TOKEN)
if [ -n "$token" ]; then
  curl -fsS -m 10 "https://api.telegram.org/bot$token/sendMessage" -d chat_id="$(val TELEGRAM_CHAT_ID)" \
    --data-urlencode text="$msg" >/dev/null || exit 1 # gửi lỗi → không lưu trạng thái, 5 phút sau gửi lại
fi
printf '%s' "$p" > .monitor-state
