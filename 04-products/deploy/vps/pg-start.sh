#!/bin/sh
# Entrypoint Postgres theo ROLE.
#   primary: chạy bình thường (nếu máy này vốn là standby → bỏ chế độ standby = được nâng lên primary).
#   standby: lần đầu chép toàn bộ DB từ primary, sau đó nhận thay đổi liên tục (streaming replication).
set -e
D="$PGDATA"
slot="standby_$(echo "$SELF_IP" | tr . _)"

if [ "$ROLE" = primary ]; then
  rm -f "$D/standby.signal"
else
  if [ -s "$D/PG_VERSION" ] && [ ! -f "$D/standby.signal" ]; then
    # Máy này từng là primary (vd sau failover vừa quay lại): dữ liệu có thể lệch → cất sang bên (không xoá), chép lại.
    old="$D-old-$(date +%Y%m%d%H%M%S)"
    echo "pg-start: máy này từng là primary, cất dữ liệu cũ vào $old"
    mv "$D" "$old"
  fi
  if [ ! -s "$D/PG_VERSION" ]; then
    export PGPASSWORD="$POSTGRES_PASSWORD"
    # Chép vào thư mục tạm rồi mới đổi tên: bị ngắt giữa chừng thì lần sau chép lại, không chạy với dữ liệu dở dang.
    until rm -rf "$D.tmp" && install -d -m 700 -o postgres -g postgres "$D.tmp" &&
      gosu postgres psql -h "$PRIMARY_IP" -U app -d products -qAtc \
        "SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = '$slot' AND NOT active" >/dev/null &&
      gosu postgres pg_basebackup -h "$PRIMARY_IP" -U app -D "$D.tmp" -R -X stream -C -S "$slot"; do
      echo "pg-start: chưa chép được từ primary $PRIMARY_IP, thử lại sau 5s"
      sleep 5
    done
    mv "$D.tmp" "$D"
  fi
fi
exec docker-entrypoint.sh "$@"
