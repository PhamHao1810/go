#!/bin/sh
# Khoá bảo mật + nối mạng riêng WireGuard cho 1 VPS Ubuntu 22.04/24.04. deploy.ps1 -Setup gọi: lần đầu bằng root,
# những lần sau bằng user deploy + sudo. Chạy lại không sao.
# Dùng: sh setup.sh <app|db> [giờ tự reboot khi có bản vá kernel, vd 03:00]   (cần wg0.conf, cf-ufw.sh cùng thư mục)
set -eu
ROLE=$1
REBOOT_AT=${2:-}
HERE=$(cd "$(dirname "$0")" && pwd)
export DEBIAN_FRONTEND=noninteractive
[ -s /root/.ssh/authorized_keys ] || [ -s /home/deploy/.ssh/authorized_keys ] ||
  { echo "chưa có SSH key: dừng để không tự khoá mình ngoài"; exit 1; }

echo "== 1. Cập nhật hệ điều hành, tự cài bản vá bảo mật mỗi ngày"
apt-get update -q
apt-get upgrade -yq -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold
apt-get install -yq ufw fail2ban unattended-upgrades curl wireguard-tools
printf 'APT::Periodic::Update-Package-Lists "1";\nAPT::Periodic::Unattended-Upgrade "1";\n' > /etc/apt/apt.conf.d/20auto-upgrades
if [ -n "$REBOOT_AT" ]; then
  # Máy app tự reboot khi bản vá kernel cần (mỗi máy 1 giờ khác nhau → luôn còn máy phục vụ). Máy DB: reboot tay.
  printf 'Unattended-Upgrade::Automatic-Reboot "true";\nUnattended-Upgrade::Automatic-Reboot-Time "%s";\n' "$REBOOT_AT" \
    > /etc/apt/apt.conf.d/52auto-reboot
fi

echo "== 2. User deploy (thay cho root)"
id deploy >/dev/null 2>&1 || adduser --disabled-password --gecos "" deploy
# deploy thuộc nhóm docker = tương đương root rồi, nên sudo không mật khẩu không làm yếu thêm.
echo 'deploy ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/deploy && chmod 440 /etc/sudoers.d/deploy
install -d -m 700 -o deploy -g deploy /home/deploy/.ssh /home/deploy/app /home/deploy/backups
[ -s /home/deploy/.ssh/authorized_keys ] ||
  install -m 600 -o deploy -g deploy /root/.ssh/authorized_keys /home/deploy/.ssh/authorized_keys

echo "== 3. Mạng riêng WireGuard 10.10.0.0/24 (mã hoá mọi kết nối giữa các máy)"
install -m 600 /dev/null /etc/wireguard/wg0.conf
sed "/^\[Interface\]/a PrivateKey = $(cat /etc/wireguard/private.key)" "$HERE/wg0.conf" > /etc/wireguard/wg0.conf
systemctl enable wg-quick@wg0 >/dev/null 2>&1
if systemctl is-active -q wg-quick@wg0; then
  wg-quick strip wg0 > /run/wg0.strip && wg syncconf wg0 /run/wg0.strip && rm /run/wg0.strip # thêm/bớt máy không ngắt kết nối
else
  systemctl start wg-quick@wg0
fi

echo "== 4. Docker (khởi động SAU WireGuard vì dịch vụ nghe trên IP 10.10.0.x), giới hạn log"
mkdir -p /etc/docker /etc/systemd/system/docker.service.d
printf '{"log-driver":"json-file","log-opts":{"max-size":"10m","max-file":"5"}}\n' > /etc/docker/daemon.json
printf '[Unit]\nAfter=wg-quick@wg0.service\nWants=wg-quick@wg0.service\n' > /etc/systemd/system/docker.service.d/wireguard.conf
systemctl daemon-reload
command -v docker >/dev/null 2>&1 || curl -fsSL https://get.docker.com | sh
systemctl restart docker
usermod -aG docker deploy

echo "== 5. Swap 2GB"
if [ -z "$(swapon --show)" ]; then
  fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
  echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi

echo "== 6. Firewall"
ufw default deny incoming
ufw default allow outgoing
ufw allow OpenSSH
ufw allow in on wg0 # các máy trong mạng riêng gọi nhau thoải mái
for peer in $(sed -n 's/^Endpoint *= *\(.*\):51820$/\1/p' "$HERE/wg0.conf"); do
  ufw allow proto udp from "$peer" to any port 51820 # WireGuard chỉ nhận từ đúng các máy trong hệ thống
done
if [ "$ROLE" = app ]; then
  install -m 755 "$HERE/cf-ufw.sh" /etc/cron.weekly/cf-ufw # tự cập nhật dải IP Cloudflare mỗi tuần
  /etc/cron.weekly/cf-ufw
fi
ufw --force enable

echo "== 7. fail2ban: khoá IP dò mật khẩu SSH"
systemctl enable --now fail2ban

echo "== 8. SSH: chỉ đăng nhập bằng key, cấm root, cấm mật khẩu"
# File "00-" được đọc TRƯỚC file của nhà cung cấp (vd 50-cloud-init.conf bật mật khẩu); sshd lấy giá trị gặp đầu tiên.
cat > /etc/ssh/sshd_config.d/00-hardening.conf <<'EOF'
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
MaxAuthTries 3
AllowUsers deploy
EOF
sshd -t # cấu hình sai → dừng ở đây, chưa restart, không bị khoá ngoài
systemctl restart ssh 2>/dev/null || systemctl restart sshd
echo "Xong setup ($ROLE)."
