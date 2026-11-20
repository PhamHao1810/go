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
  # Body đi qua stdin, không qua tham số dòng lệnh: Git Bash trên Windows làm hỏng chữ có dấu trong tham số.
  printf '%s' "$4" | curl -s -o "$TMP/body" -w "%{http_code}" -X "$1" "$B$2" -H "Content-Type: application/json"     ${auth:+-H "$auth"} ${4:+--data-binary @-} > "$TMP/status"
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

# --- Catalog: danh mục cây, hãng, biến thể, tag, đơn hàng, đánh giá, search ---
ROOT=$(req POST /categories "$ADMIN" "{\"name\":\"Thời trang $RUN\",\"slug\":\"tt-$RUN\"}" | field id); check "tạo danh mục gốc" 201 "$(status)"
LEAF=$(req POST /categories "$ADMIN" "{\"parent_id\":$ROOT,\"name\":\"Áo khoác\",\"slug\":\"ak-$RUN\"}" | field id); check "tạo danh mục con" 201 "$(status)"
req POST /categories "$USER" "{\"name\":\"x\",\"slug\":\"x-$RUN\"}" >/dev/null; check "khách không tạo được danh mục" 403 "$(status)"
BRAND=$(req POST /brands "$ADMIN" "{\"name\":\"Hãng $RUN\",\"slug\":\"h-$RUN\"}" | field id); check "tạo hãng" 201 "$(status)"
PID2=$(req POST /products "$ADMIN" "{\"sku\":\"CAT-$RUN\",\"name\":\"Áo khoác gió $RUN\",\"price\":300000,\"stock\":5,\"category_id\":$LEAF,\"brand_id\":$BRAND}" | field id)
check "tạo sản phẩm có danh mục + hãng" 201 "$(status)"
VID=$(req POST "/products/$PID2/variants" "$ADMIN" "{\"sku\":\"CAT-$RUN-RED-L\",\"color\":\"Đỏ\",\"size\":\"L\",\"price\":280000,\"stock\":3}" | field id)
check "thêm biến thể" 201 "$(status)"
check "giá sản phẩm = giá thấp nhất các biến thể" 280000 "$(req GET "/products/$PID2" - | field price)"
check "kho sản phẩm = tổng kho các biến thể" 8 "$(req GET "/products/$PID2" - | field stock)"
req PUT "/products/$PID2/tags" "$ADMIN" '{"tags":["Hot","khuyến mãi","hot"]}' >/dev/null; check "gắn tag" 200 "$(status)"
req PUT "/products/$PID2/attributes" "$ADMIN" '{"attributes":[{"name":"Chất liệu","value":"Polyester"}]}' >/dev/null; check "gắn thuộc tính" 200 "$(status)"
req POST "/products/$PID2/purchase" "$USER" '{"quantity":1}' >/dev/null; check "mua nhanh sản phẩm nhiều biến thể → 400" 400 "$(status)"

OID=$(req POST /orders "$USER" "{\"items\":[{\"variant_id\":$VID,\"quantity\":2}]}" | field id); check "đặt hàng" 201 "$(status)"
check "tổng tiền chốt theo giá biến thể" 560000 "$(req GET "/orders/$OID" "$USER" | field total)"
req POST /orders "$USER" "{\"items\":[{\"variant_id\":$VID,\"quantity\":5}]}" >/dev/null; check "đặt quá tồn kho → 409" 409 "$(status)"
req POST "/products/$PID2/reviews" "$USER" '{"rating":5,"content":"Đẹp"}' >/dev/null; check "chưa nhận hàng không đánh giá được" 403 "$(status)"
req PUT "/orders/$OID/status" "$USER" '{"status":"paid"}' >/dev/null; check "khách không đổi trạng thái đơn được" 403 "$(status)"
req PUT "/orders/$OID/status" "$ADMIN" '{"status":"shipped"}' >/dev/null; check "không nhảy cóc pending → shipped" 409 "$(status)"
for st in paid shipped delivered; do req PUT "/orders/$OID/status" "$ADMIN" "{\"status\":\"$st\"}" >/dev/null; check "đơn → $st" 200 "$(status)"; done
req POST "/products/$PID2/reviews" "$USER" '{"rating":4,"content":"Áo đẹp, giao nhanh"}' >/dev/null; check "đánh giá sau khi nhận hàng" 201 "$(status)"
req POST "/products/$PID2/reviews" "$USER" '{"rating":1}' >/dev/null; check "đánh giá 2 lần → 409" 409 "$(status)"
check "điểm đánh giá trung bình" 4 "$(req GET "/products/$PID2" - | field average)"

OID2=$(req POST /orders "$USER" "{\"items\":[{\"variant_id\":$VID,\"quantity\":1}]}" | field id)
req POST "/orders/$OID2/cancel" "$USER" >/dev/null; check "huỷ đơn" 200 "$(status)"
check "huỷ đơn trả lại kho" 6 "$(req GET "/products/$PID2" - | field stock)"
check "lọc theo danh mục cha ra sản phẩm ở danh mục con" "$PID2" "$(req GET "/products?category_id=$ROOT" - | field id)"

# Search: outbox → Meilisearch trễ ~1-2 giây. Gõ không dấu + sai chính tả vẫn phải ra.
found=""
for i in 1 2 3 4 5 6 7 8 9 10; do
  if req GET "/search?q=ao+khoac+gioo+$RUN" - | grep -q "\"id\":$PID2,"; then found=yes; break; fi
  sleep 1
done
check "search không dấu + sai chính tả thấy sản phẩm mới (engine: $(req GET "/search?q=x" - | field engine))" yes "$found"

# --- Mua hàng + refresh token ---
req POST "/products/$PID/purchase" "$USER" '{"quantity":2}' >/dev/null; check "khách mua hàng" 200 "$(status)"
NEW=$(req POST /auth/refresh - "{\"refresh_token\":\"$REFRESH\"}" | field refresh_token)
check "refresh lấy token mới" 200 "$(status)"
req POST /auth/refresh - "{\"refresh_token\":\"$REFRESH\"}" >/dev/null; check "dùng lại refresh token cũ → 401" 401 "$(status)"
req POST /auth/refresh - "{\"refresh_token\":\"$NEW\"}" >/dev/null;     check "phát hiện dùng lại → thu hồi cả token mới" 401 "$(status)"

rm -rf "$TMP"
exit $FAILED
