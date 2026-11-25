#!/bin/sh
# Chạy trên mỗi máy trong ~/app (deploy.ps1 gọi). Vai trò lấy từ ROLE trong .env: app | primary | standby.
#   app: nạp image mới, thay LẦN LƯỢT api1 rồi api2 (instance kia phục vụ trong lúc chờ) → không gián đoạn.
#        "sh update.sh rollback": quay lại image API trước đó (giữ sẵn trên máy), không cần build.
#   primary/standby: dựng/cập nhật DB (Patroni tự chọn primary), kho ảnh theo ROLE.
set -e
cd "$(dirname "$0")"
chmod 600 .env
val() { sed -n "s/^$1=//p" .env; }
# render_nginx: nginx.conf.tmpl + giá trị trong .env → stdout. Mỗi máy app khác thêm 2 dòng server (api1, api2).
render_nginx() {
  peers=""
  for ip in $(val PEER_IPS | tr ',' ' '); do
    peers="$peers        server $ip:8081 max_fails=1 fail_timeout=5s;\n        server $ip:8082 max_fails=1 fail_timeout=5s;\n"
  done
  sb=""
  [ -n "$(val STORAGE_BACKUP_IP)" ] && sb="        server $(val STORAGE_BACKUP_IP):9000 backup;\n"
  sed -e "s/__SELF_IP__/$(val SELF_IP)/g" -e "s/__DB_IP__/$(val DB_IP)/g" -e "s/__DOMAIN__/$(val DOMAIN)/g" \
    -e "s|__RATE_API__|$(val RATE_API)|g" -e "s|__RATE_AUTH__|$(val RATE_AUTH)|g" nginx.conf.tmpl |
    awk -v peers="$peers" -v sb="$sb" '/__PEER_SERVERS__/ { printf "%s", peers; next } /__STORAGE_BACKUP__/ { printf "%s", sb; next } { print }'
}
ROLE=$(val ROLE)

# etcd (máy được deploy.ps1 chọn bỏ phiếu): lên trước Postgres để Patroni có chỗ bầu primary.
if [ -n "$(val ETCD_NAME)" ]; then docker compose up -d etcd; fi

if [ "$ROLE" = app ]; then
  if [ "$1" = rollback ]; then
    prev=$(cat .api-prev 2>/dev/null) || { echo "không có bản trước để rollback"; exit 1; }
    sed -i "s/^API_TAG=.*/API_TAG=$prev/" .env
    echo "== rollback về $prev"
  fi
  tag=$(val API_TAG)
  if [ -f api.tar ]; then docker load -i api.tar && rm api.tar; fi
  chmod 600 origin.key
  render_nginx > nginx.conf # ghi ĐÈ nội dung: container mount đúng file này, file mới (sed -i) nó không thấy

  for s in api1 api2; do
    echo "== cập nhật $s ($tag)"
    docker compose up -d --wait --wait-timeout 120 "$s" || { docker compose logs --tail 50 "$s"; exit 1; }
    sleep 10 # > fail_timeout của Nginx: chờ Nginx dùng lại instance vừa lên rồi mới tắt instance kia
  done
  docker compose up -d nginx
  docker compose exec -T nginx nginx -t -q # cấu hình sai → dừng deploy, Nginx giữ cấu hình cũ vẫn chạy
  docker compose exec -T nginx nginx -s reload # đổi cấu hình không ngắt kết nối

  # Nhớ bản đang chạy + bản trước (để rollback); xoá mọi image API khác.
  cur=$(cat .api-current 2>/dev/null || true)
  if [ "$cur" != "$tag" ]; then
    [ -n "$cur" ] && echo "$cur" > .api-prev
    echo "$tag" > .api-current
  fi
  echo "$(date '+%F %T') $tag" >> deploy.log
  prev=$(cat .api-prev 2>/dev/null || true)
  docker images products-api --format '{{.Tag}}' | grep -vx -e "$tag" -e "${prev:-$tag}" |
    while read -r t; do docker rmi "products-api:$t" >/dev/null || true; done
else
  chmod 644 s3.json # SeaweedFS chạy bằng user thường trong container; thư mục ~/app đã là 700
  # Máy này giờ là primary kho ảnh: dừng việc chép ảnh từ máy kia.
  [ "$ROLE" = primary ] && { docker compose --profile standby rm -sf storage-sync >/dev/null 2>&1 || true; }
  echo "== DB ($ROLE)"
  docker compose up -d postgres redis storage
  # Không dùng --wait: Postgres đang chép lại từ primary (sau failover, vài phút) báo "unhealthy" → compose bỏ cuộc ngay.
  i=0
  until docker compose ps postgres --format '{{.Health}}' | grep -qx healthy; do
    i=$((i + 1))
    [ "$i" -gt 720 ] && { docker compose logs --tail 50 postgres storage; exit 1; } # 1 giờ
    sleep 5
  done
  if [ "$(val SEARCH)" = 1 ]; then
    docker compose --profile "$ROLE" --profile search up -d
  else
    docker compose --profile search rm -sf meilisearch >/dev/null 2>&1 || true # máy này thôi chạy search
    docker compose --profile "$ROLE" up -d
  fi
fi
docker image prune -f >/dev/null # xoá image cũ, tránh đầy ổ sau nhiều lần deploy

# Cron: kiểm tra sức khoẻ 5 phút/lần; backup 3h sáng ở standby (không có standby thì ở primary).
[ -f .first-deploy ] || touch .first-deploy
{
  # || true: crontab rỗng → grep trả 1 → set -e thoát khỏi nhóm { } trước khi echo → cài crontab RỖNG (lỗi cũ: chưa máy nào có cron)
  crontab -l 2>/dev/null | grep -v -e monitor.sh -e backup.sh || true
  echo "*/5 * * * * sh $PWD/monitor.sh"
  if [ "$ROLE" = standby ] || { [ "$ROLE" = primary ] && [ -z "$(val STANDBY_IP)" ]; }; then
    echo "0 3 * * * sh $PWD/backup.sh >> $HOME/backups/backup.log 2>&1"
  fi
} | crontab -
echo "Xong ($ROLE)."
