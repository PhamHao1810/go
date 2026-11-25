#!/bin/sh
# Đo gián đoạn khi diễn tập hỏng máy: mỗi 0,5 giây 1 lượt ĐỌC (danh sách sản phẩm) + 1 lượt GHI (đăng ký khách mới),
# in "giây  mã-đọc  giây-đọc  mã-ghi". Bắn thẳng origin: FAKE_IPS=1 giả mỗi lượt 1 IP khách (CF-Connecting-IP) để rate
# limit không chặn. Đi qua Cloudflare: KHÔNG đặt FAKE_IPS (Cloudflare trả 403 cho request có header đó) và đặt
# NO_WRITE=1 (rate limit đăng ký theo IP thật sẽ chặn).
# Dùng: BASE_URL=https://shop.lab [INSECURE=1] [FAKE_IPS=1] [NO_WRITE=1] [SECONDS_TOTAL=180] sh probe.sh > probe.log
# Tóm tắt: awk '$2 != 200 {r++} $4 != 201 && $4 != "-" {w++} END {print NR " lượt, lỗi đọc " r ", lỗi ghi " w}' probe.log
B=${BASE_URL:?}/api/v1
K=""; [ -n "$INSECURE" ] && K=-k
end=$(( $(date +%s) + ${SECONDS_TOTAL:-180} ))
i=0
while [ "$(date +%s)" -lt "$end" ]; do
  i=$((i + 1))
  h="X-Probe: $i"
  [ -n "$FAKE_IPS" ] && h="CF-Connecting-IP: 10.$((i / 65536 % 256)).$((i / 256 % 256)).$((i % 256))"
  r=$(curl -s $K -m 20 -o /dev/null -w '%{http_code} %{time_total}' -H "$h" "$B/products?limit=1")
  w=-
  [ -z "$NO_WRITE" ] && w=$(curl -s $K -m 5 -o /dev/null -w '%{http_code}' -H "$h" -H 'Content-Type: application/json' \
    -d "{\"email\":\"probe$(date +%s)$i@test.vn\",\"password\":\"password123\",\"name\":\"probe\"}" "$B/auth/register")
  echo "$(date +%s) $r $w"
  sleep 0.5
done
