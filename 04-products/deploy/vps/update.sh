#!/bin/sh
# Chạy trên mỗi máy trong ~/app (deploy.ps1 gọi). Vai trò lấy từ ROLE trong .env: app | primary | standby.
#   app: nạp image mới, thay LẦN LƯỢT api1 rồi api2 (instance kia phục vụ trong lúc chờ) → không gián đoạn.
#   primary/standby: dựng/cập nhật DB, kho ảnh; đổi vai trò khi failover.
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
  sed -e "s/__SELF_IP__/$(val SELF_IP)/g" -e "s/__DB_IP__/$(val DB_IP)/g" -e "s/__DOMAIN__/$(val DOMAIN)/g" \
    -e "s|__RATE_API__|$(val RATE_API)|g" -e "s|__RATE_AUTH__|$(val RATE_AUTH)|g" nginx.conf.tmpl |
    awk -v peers="$peers" '/__PEER_SERVERS__/ { printf "%s", peers; next } { print }'
}
ROLE=$(val ROLE)

if [ "$ROLE" = app ]; then
  if [ -f api.tar ]; then docker load -i api.tar && rm api.tar; fi
  chmod 600 origin.key
  render_nginx > nginx.conf # ghi ĐÈ nội dung: container mount đúng file này, file mới (sed -i) nó không thấy

  for s in api1 api2; do
    echo "== cập nhật $s"
    docker compose up -d --wait --wait-timeout 120 "$s" || { docker compose logs --tail 50 "$s"; exit 1; }
    sleep 10 # > fail_timeout của Nginx: chờ Nginx dùng lại instance vừa lên rồi mới tắt instance kia
  done
  docker compose up -d nginx
  docker compose exec -T nginx nginx -t -q # cấu hình sai → dừng deploy, Nginx giữ cấu hình cũ vẫn chạy
  docker compose exec -T nginx nginx -s reload # đổi cấu hình không ngắt kết nối
else
  chmod 644 s3.json # SeaweedFS chạy bằng user thường trong container; thư mục ~/app đã là 700
  # Standby được nâng lên primary: dừng việc chép ảnh từ primary cũ.
  [ "$ROLE" = primary ] && { docker compose --profile standby rm -sf storage-sync >/dev/null 2>&1 || true; }
  echo "== DB ($ROLE)"
  docker compose up -d --wait --wait-timeout 3600 postgres redis storage ||
    { docker compose logs --tail 50 postgres storage; exit 1; }
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
