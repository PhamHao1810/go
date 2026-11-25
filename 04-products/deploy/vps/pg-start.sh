#!/bin/sh
# Entrypoint Postgres = Patroni. Patroni giữ "khoá primary" trong etcd (3 máy bỏ phiếu, chịu được 1 máy chết):
#   - primary chết/mất mạng > ~30s: khoá hết hạn → standby tự lên primary. API nối "máy nào đang ghi được"
#     (target_session_attrs=read-write) nên tự theo, không cần deploy.
#   - primary cũ sống lại / hết bị cô lập: không còn khoá → tự thành standby (pg_rewind; hỏng thì chép lại từ đầu).
#     Máy primary mất liên lạc với etcd cũng tự hạ xuống read-only → không bao giờ 2 máy cùng nhận ghi.
# Lần đầu chuyển sang Patroni: máy có dữ liệu primary cũ được Patroni nhận làm primary (deploy.ps1 chạy máy đó trước).
set -e
D=/var/lib/postgresql/data/pgdata
NAME="pg_$(echo "$SELF_IP" | tr . _)"

if [ -f "$D/postgresql.auto.conf" ]; then
  # Standby dựng bằng pg_basebackup -R (trước Patroni) ghi primary_conninfo/slot vào đây; file này đè lên cấu hình
  # Patroni → bỏ đi để Patroni tự quản việc nhân bản.
  sed -i '/^primary_conninfo\|^primary_slot_name/d' "$D/postgresql.auto.conf"
fi

rm -f /tmp/post-init.sh /tmp/patroni.yml # khởi động lại: file cũ thuộc user postgres, root không ghi đè được trong /tmp (protected_regular)
cat > /tmp/post-init.sh <<'EOF'
#!/bin/sh
# Patroni gọi đúng 1 lần khi tạo cụm mới (initdb): tạo database cho app.
psql "$1" -c "CREATE DATABASE products"
EOF
chmod 755 /tmp/post-init.sh

umask 077
cat > /tmp/patroni.yml <<EOF
scope: products
name: $NAME
restapi:
  listen: $SELF_IP:8008
  connect_address: $SELF_IP:8008
  authentication: { username: patroni, password: '$POSTGRES_PASSWORD' }
etcd3:
  hosts: $ETCD_HOSTS
bootstrap:
  dcs:
    ttl: 30
    loop_wait: 10
    retry_timeout: 10
    maximum_lag_on_failover: 16777216 # standby chậm > 16MB WAL thì không tự lên primary (tránh mất nhiều dữ liệu)
    failsafe_mode: true # etcd mất quorum nhưng 2 máy Postgres vẫn thấy nhau → primary giữ nguyên, không tự hạ
    postgresql:
      use_pg_rewind: true
      use_slots: true
      parameters:
        max_connections: 200
        wal_log_hints: "on" # pg_rewind cần
        hot_standby_feedback: "on"
        max_slot_wal_keep_size: 10GB # standby chết lâu: primary giữ tối đa 10GB WAL rồi bỏ slot, không đầy ổ
        # pg_rewind (primary cũ quay lại làm standby) cần WAL của chính nó từ checkpoint trước lúc tách. Đo thật: ghi
        # nặng → WAL đó đã xoay vòng → rewind hỏng → chép lại cả DB. Giữ 1GB: rewind vài giây thay vì chép vài phút.
        # Cụm đã chạy: patronictl -c /tmp/patroni.yml edit-config -s postgresql.parameters.wal_keep_size=1GB --force
        wal_keep_size: 1GB
  initdb: [{ encoding: UTF8 }, data-checksums]
  post_bootstrap: /tmp/post-init.sh
postgresql:
  listen: $SELF_IP,127.0.0.1:5432
  connect_address: $SELF_IP:5432
  data_dir: $D
  remove_data_directory_on_rewind_failure: true
  remove_data_directory_on_diverged_timelines: true
  authentication:
    superuser: { username: app, password: '$POSTGRES_PASSWORD' }
    replication: { username: app, password: '$POSTGRES_PASSWORD' }
  parameters:
    shared_buffers: ${PG_SHARED_BUFFERS:-2GB}
    effective_cache_size: ${PG_EFFECTIVE_CACHE_SIZE:-6GB}
    work_mem: 8MB
  pg_hba: # chỉ localhost + mạng WireGuard, luôn cần mật khẩu
    - local all all trust
    - host all all 127.0.0.1/32 scram-sha-256
    - host all all 10.10.0.0/24 scram-sha-256
    - host replication app 127.0.0.1/32 scram-sha-256
    - host replication app 10.10.0.0/24 scram-sha-256
EOF
chown postgres /tmp/patroni.yml
mkdir -p "$(dirname "$D")" && chown postgres:postgres "$(dirname "$D")"
exec gosu postgres patroni /tmp/patroni.yml
