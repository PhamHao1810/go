#!/bin/sh
# Chạy trên 1 VPS trong lúc load test: mỗi 5 giây ghi 1 dòng "giờ-unix cpu% steal% tải RAM" + CPU/RAM từng container.
# Dùng: scp sample.sh deploy@<ip>: && ssh deploy@<ip> "nohup sh sample.sh 900 > sample.log 2>&1 < /dev/null &"   (900 = số giây đo)
end=$(( $(date +%s) + ${1:-600} ))
prev=$(head -1 /proc/stat)
while [ "$(date +%s)" -lt "$end" ]; do
  sleep 5
  cur=$(head -1 /proc/stat)
  # cột /proc/stat: user nice system idle iowait irq softirq steal
  echo "$prev
$cur" | awk -v now="$(date +%s)" 'NR == 1 { for (i = 2; i <= 9; i++) a[i] = $i } NR == 2 { for (i = 2; i <= 9; i++) { d[i] = $i - a[i]; t += d[i] }
    printf "%d cpu=%.0f steal=%.0f ", now, 100 * (t - d[5] - d[6]) / t, 100 * d[9] / t }'
  printf 'load=%s memused=%sMiB ' "$(cut -d' ' -f1 /proc/loadavg)" "$(free -m | awk '/Mem:/ {print $3}')"
  # mỗi container: tên=CPU% (100% = 1 core) và tên:mem=RAM đang dùng
  docker stats --no-stream --format '{{.Name}}={{.CPUPerc}} {{.Name}}:mem={{.MemUsage}}' |
    sed 's/products-//g; s/-1=/=/; s/-1:mem/:mem/; s| / [^ ]*||' | tr '\n' ' '
  echo
  prev=$cur
done
