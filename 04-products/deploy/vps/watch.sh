#!/bin/bash
# Xem CPU / steal / RAM / mạng / từng container của MỌI VPS gần như real time (~3 giây/lần). Ctrl+C để thoát.
# Không cài gì lên VPS: mỗi máy 1 kết nối SSH chạy vòng lặp đo, máy bạn vẽ lại bảng.
# Chạy ở thư mục gốc repo (key đã ssh-add trong PowerShell), bằng Git Bash hoặc từ PowerShell (bash = WSL cũng được):
#   bash 04-products/deploy/vps/watch.sh
# steal cao = nhà cung cấp VPS đang lấy CPU của máy bạn cho máy khác (máy "hàng xóm" ồn).
cd "$(dirname "$0")" || exit 1
# Dùng ssh.exe của Windows (thấy key đã ssh-add + known_hosts của Windows) dù chạy từ Git Bash (/c) hay WSL (/mnt/c).
SSH=ssh
for p in /c/Windows/System32/OpenSSH/ssh.exe /mnt/c/Windows/System32/OpenSSH/ssh.exe; do [ -x "$p" ] && SSH=$p && break; done
val() { sed -n "s/^$1=//p" .env | tr -d '\r'; }
primary=$(val DB_PRIMARY)

REMOTE='IF=$(ip route | awk "/default/ {print \$5; exit}")
rx0=$(cat /sys/class/net/$IF/statistics/rx_bytes); tx0=$(cat /sys/class/net/$IF/statistics/tx_bytes); t0=$(date +%s)
while :; do
  cpu=$(top -bn2 -d1 | grep "^%Cpu" | tail -1 | sed "s/,/ /g" | awk "{for(i=1;i<NF;i++){if(\$(i+1)==\"us\")u=\$i;if(\$(i+1)==\"sy\")s=\$i;if(\$(i+1)==\"id\")d=\$i;if(\$(i+1)==\"st\")t=\$i}} END{printf \"CPU %5.1f%%  (us %.0f sy %.0f steal %.0f)\", 100-d, u, s, t}")
  rx=$(cat /sys/class/net/$IF/statistics/rx_bytes); tx=$(cat /sys/class/net/$IF/statistics/tx_bytes); t=$(date +%s); dt=$((t-t0)); [ $dt -lt 1 ] && dt=1
  echo "$cpu | load $(cut -d" " -f1 /proc/loadavg)/$(nproc) | RAM $(free -m | awk "/Mem:/ {print \$3\"/\"\$2}")MB | net in $(( (rx-rx0)*8/dt/1000000 )) out $(( (tx-tx0)*8/dt/1000000 )) Mbps"
  rx0=$rx; tx0=$tx; t0=$t
  docker stats --no-stream --format "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}" | sed "s/products-//; s/-1|/|/" | awk -F"|" "{printf \"    %-16s CPU %7s  RAM %s\n\", \$1, \$2, \$3}"
  echo @@
done'

tmp=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$tmp"' EXIT
hosts=()
i=0; for h in $(val APP_SERVERS | tr ',' ' '); do i=$((i+1)); hosts+=("$h|app$i"); done
for h in $(val DB_SERVERS | tr ',' ' '); do
  if [ "$h" = "$primary" ]; then hosts+=("$h|db primary"); else hosts+=("$h|db standby"); fi
done
[ "$(val SEARCH_SERVER)" ] && hosts=("${hosts[@]/$(val SEARCH_SERVER)|db standby/$(val SEARCH_SERVER)|db standby + meilisearch}")
for e in "${hosts[@]}"; do
  "$SSH" -o BatchMode=yes -o ConnectTimeout=10 -o ServerAliveInterval=15 "deploy@${e%%|*}" "$REMOTE" > "$tmp/${e%%|*}" 2>&1 &
done

while sleep 2; do
  out="$(date +%T)  (Ctrl+C để thoát)\n"
  for e in "${hosts[@]}"; do
    ip=${e%%|*}
    last=$(awk '/^@@$/ { if (b != "") last = b; b = ""; next } { b = b $0 "\n" } END { printf "%s", last }' "$tmp/$ip")
    [ -z "$last" ] && last="    (đang kết nối... $(tail -1 "$tmp/$ip" 2>/dev/null | tr -d '\r'))\n"
    out+="\n== ${e#*|}  $ip\n  $last"
  done
  clear; printf "%b" "$out"
done
