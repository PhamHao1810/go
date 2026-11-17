#!/bin/sh
# Máy app: chỉ cho IP của Cloudflare vào cổng 443. Ai biết IP máy cũng không gọi thẳng được (phải qua lớp chống DDoS).
# setup.sh cài vào /etc/cron.weekly để tự cập nhật khi Cloudflare thêm/bớt dải IP.
set -eu
new=$(curl -fsS -m 20 https://www.cloudflare.com/ips-v4; echo; curl -fsS -m 20 https://www.cloudflare.com/ips-v6)
# Danh sách lỗi/rỗng → giữ nguyên rule cũ (xoá nhầm = Cloudflare không vào được = sập web).
[ "$(echo "$new" | grep -c /)" -ge 10 ] || { echo "cf-ufw: danh sách IP Cloudflare bất thường, giữ rule cũ"; exit 1; }

for ip in $new; do
  ufw allow proto tcp from "$ip" to any port 443 comment cloudflare >/dev/null
done
# Thêm trước, xoá sau: không lúc nào Cloudflare bị chặn.
for ip in $(ufw status | awk '/# cloudflare/ {for (i = 1; i <= NF; i++) if ($i ~ /^[0-9a-f.:]+\/[0-9]+$/) print $i}'); do
  echo "$new" | grep -qx "$ip" || ufw delete allow proto tcp from "$ip" to any port 443 >/dev/null
done
echo "cf-ufw: $(echo "$new" | grep -c /) dải IP Cloudflare"
