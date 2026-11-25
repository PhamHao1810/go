#!/bin/sh
# Backup hằng đêm (cron 3h, update.sh cài) — chạy trên máy standby để backup KHÁC máy với DB chính.
# DB: dump từ primary, giữ 7 bản (với 1 triệu sản phẩm mỗi bản vài GB; ổ máy DB có hạn). Ảnh: chép thêm file mới, không xoá (lỡ tay xoá ảnh vẫn còn bản ở đây).
# Standby đã là bản sao trực tiếp, nhưng nó chép cả lỗi (DELETE nhầm, dữ liệu hỏng) → vẫn cần backup theo ngày.
# HC_BACKUP_URL (tuỳ chọn, healthchecks.io): báo xong/lỗi; quá 1 ngày không có tin (máy chết, cron hỏng) → dịch vụ đó báo bạn.
set -e
cd "$(dirname "$0")"
val() { sed -n "s/^$1=//p" .env; }
hc=$(val HC_BACKUP_URL)
trap 'rc=$?; [ -n "$hc" ] && curl -fsS -m 10 -o /dev/null "$hc$([ $rc -ne 0 ] && echo /fail)"; exit $rc' EXIT
mkdir -p ~/backups/media

file=~/backups/db-$(date +%F).dump
# Nối "máy nào đang là primary" trong mọi máy DB (Patroni có thể đã tự failover).
docker compose exec -T -e DB_HOSTS="$(val DB_HOSTS)" postgres \
  sh -c 'PGPASSWORD=$POSTGRES_PASSWORD pg_dump -Fc -d "postgres://app@$DB_HOSTS/products?target_session_attrs=read-write"' > "$file.tmp"
mv "$file.tmp" "$file" # chỉ giữ file khi dump xong trọn vẹn
ls -1t ~/backups/db-*.dump | tail -n +8 | xargs -r rm --

# Chép qua S3 API (từng file trọn vẹn), từ kho ảnh trên chính máy này (đã đồng bộ từ primary).
export AWS_ACCESS_KEY_ID="$(val STORAGE_ACCESS_KEY)" AWS_SECRET_ACCESS_KEY="$(val STORAGE_SECRET_KEY)"
docker run --rm --network host -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY -e AWS_DEFAULT_REGION=us-east-1 \
  -v "$HOME/backups/media:/backup" amazon/aws-cli:2.27.0 \
  --endpoint-url "http://$(val SELF_IP):9000" s3 sync s3://media /backup --only-show-errors
echo "$(date '+%F %T') backup ok"
