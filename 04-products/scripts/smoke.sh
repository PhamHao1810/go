#!/bin/sh
# Kiểm tra end-to-end trên cụm thật (docker compose --profile full up -d): đăng nhập, phân quyền,
# upload ảnh lên storage, gắn ảnh, avatar, refresh token. Chạy: sh 04-products/scripts/smoke.sh
# In "PASS"/"FAIL" từng bước; thoát mã 1 nếu có bước sai.
B=${BASE_URL:-http://localhost:8080}/api/v1
ADMIN_EMAIL=${ADMIN_EMAIL:-admin@example.com}
ADMIN_PASSWORD=${ADMIN_PASSWORD:-admin12345}
TMP=$(mktemp -d)
FAILED=0

# field <tên>: lấy giá trị field JSON đầu tiên từ stdin (đủ dùng cho test, không cần jq).
field() { grep -o "\"$1\":\"\{0,1\}[^\",}]*" | head -1 | sed "s/\"$1\":\"\{0,1\}//"; }
check() { # check <tên bước> <mong đợi> <thực tế>
  if [ "$2" = "$3" ]; then echo "PASS  $1"; else echo "FAIL  $1: muốn '$2', nhận '$3'"; FAILED=1; fi
}
req() { # req <method> <path> <token|-> [body] → in body, lưu status vào $TMP/status
  auth=""; [ "$3" != "-" ] && auth="Authorization: Bearer $3"
  curl -s -o "$TMP/body" -w "%{http_code}" -X "$1" "$B$2" -H "Content-Type: application/json" ${auth:+-H "$auth"} ${4:+-d "$4"} > "$TMP/status"
  cat "$TMP/body"
}
status() { cat "$TMP/status"; }
# Thử HTTPS ở lab: INSECURE=1 chấp nhận chứng chỉ tự ký; RESOLVE=shop.lab:443:<IP> gọi domain mà không cần DNS.
curl() { command curl ${INSECURE:+-k} ${RESOLVE:+--resolve "$RESOLVE"} "$@"; }
upload() { # upload <file>: PUT file lên upload_url trong $UP; in HTTP status
  url=$(echo "$UP" | field upload_url | sed 's/\\u0026/\&/g')
  curl -s -o /dev/null -w "%{http_code}" -X PUT "$url" -H "Content-Type: image/png" --data-binary "@$1"
}

# Ảnh PNG 1x1 thật.
echo 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=' | base64 -d > "$TMP/img.png"
SIZE=$(wc -c < "$TMP/img.png" | tr -d ' ')
RUN=$(date +%s)

ADMIN=$(req POST /auth/login - "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" | field access_token)
check "admin đăng nhập" 200 "$(status)"

req POST /auth/register - "{\"email\":\"user$RUN@test.vn\",\"password\":\"password123\",\"name\":\"Khách\"}" >/dev/null
check "khách đăng ký" 201 "$(status)"
LOGIN=$(req POST /auth/login - "{\"email\":\"user$RUN@test.vn\",\"password\":\"password123\"}")
USER=$(echo "$LOGIN" | field access_token); REFRESH=$(echo "$LOGIN" | field refresh_token)

req POST /products - '{"sku":"x","name":"x","price":1,"stock":1}' >/dev/null;      check "chưa đăng nhập không tạo được sản phẩm" 401 "$(status)"
req POST /products "$USER" '{"sku":"x","name":"x","price":1,"stock":1}' >/dev/null; check "khách không tạo được sản phẩm" 403 "$(status)"
PID=$(req POST /products "$ADMIN" "{\"sku\":\"SMOKE-$RUN\",\"name\":\"Giày smoke\",\"price\":100,\"stock\":5}" | field id)
check "admin tạo sản phẩm" 201 "$(status)"

# --- Upload ảnh: xin upload_url → PUT thẳng lên storage → gắn vào sản phẩm ---
UP=$(req POST /assets "$ADMIN" "{\"content_type\":\"image/png\",\"size\":$SIZE}")
check "xin upload_url" 201 "$(status)"
AID=$(echo "$UP" | field asset_id)

req POST "/products/$PID/images" "$ADMIN" "{\"asset_id\":$AID}" >/dev/null
check "gắn ảnh khi chưa upload → 409" 409 "$(status)"

head -c 30 "$TMP/img.png" > "$TMP/small.png"
code=$(upload "$TMP/small.png")
[ "$code" -ge 400 ] && code=rejected; check "upload file cắt cụt bị storage từ chối" rejected "$code"
check "upload đúng lên storage" 200 "$(upload "$TMP/img.png")"

IMG=$(req POST "/products/$PID/images" "$ADMIN" "{\"asset_id\":$AID}" | field url)
check "gắn ảnh vào sản phẩm" 200 "$(status)"
check "GET sản phẩm có URL ảnh" "$IMG" "$(req GET "/products/$PID" - | field url)"
check "ảnh tải công khai được" 200 "$(curl -s -o /dev/null -w "%{http_code}" "$IMG")"

req POST "/products/$PID/images" "$USER" "{\"asset_id\":$AID}" >/dev/null
check "khách không gắn ảnh được" 403 "$(status)"

# --- Avatar: khách upload ảnh của mình; không dùng được ảnh của admin ---
req PUT /me/avatar "$USER" "{\"asset_id\":$AID}" >/dev/null
check "không lấy được ảnh người khác làm avatar" 404 "$(status)"
UP=$(req POST /assets "$USER" "{\"content_type\":\"image/png\",\"size\":$SIZE}")
UAID=$(echo "$UP" | field asset_id); upload "$TMP/img.png" >/dev/null
AVATAR=$(req PUT /me/avatar "$USER" "{\"asset_id\":$UAID}" | field avatar_url)
check "đặt avatar" 200 "$(status)"
check "GET /me có avatar_url" "$AVATAR" "$(req GET /me "$USER" | field avatar_url)"

# --- Mua hàng + refresh token ---
req POST "/products/$PID/purchase" "$USER" '{"quantity":2}' >/dev/null; check "khách mua hàng" 200 "$(status)"
NEW=$(req POST /auth/refresh - "{\"refresh_token\":\"$REFRESH\"}" | field refresh_token)
check "refresh lấy token mới" 200 "$(status)"
req POST /auth/refresh - "{\"refresh_token\":\"$REFRESH\"}" >/dev/null; check "dùng lại refresh token cũ → 401" 401 "$(status)"
req POST /auth/refresh - "{\"refresh_token\":\"$NEW\"}" >/dev/null;     check "phát hiện dùng lại → thu hồi cả token mới" 401 "$(status)"

rm -rf "$TMP"
exit $FAILED
