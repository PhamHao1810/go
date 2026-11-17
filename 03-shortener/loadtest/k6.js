// Load test bằng k6 (không cần cài, chạy qua Docker — xem README).
// Bắt đầu nhỏ (200 VU), tìm nút thắt bằng pprof, sửa, rồi tăng dần lên 1k -> 10k -> 100k.
import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';

export const options = {
  stages: [
    { duration: '30s', target: 200 }, // ramp-up
    { duration: '1m', target: 200 },  // giữ tải
    { duration: '15s', target: 0 },   // ramp-down
  ],
  thresholds: {
    http_req_failed: ['rate<0.01'],   // < 1% lỗi
    http_req_duration: ['p(95)<100'], // p95 < 100ms
  },
};

// Chạy 1 lần trước test: tạo 100 link để dùng.
export function setup() {
  const codes = [];
  for (let i = 0; i < 100; i++) {
    const res = http.post(`${BASE}/api/links`, JSON.stringify({ url: `https://example.com/${i}` }), {
      headers: { 'Content-Type': 'application/json' },
    });
    codes.push(res.json('code'));
  }
  return { codes };
}

// Mỗi virtual user (VU) lặp hàm này liên tục.
export default function (data) {
  const code = data.codes[Math.floor(Math.random() * data.codes.length)];
  const res = http.get(`${BASE}/${code}`, { redirects: 0 });
  check(res, { 'status 302': (r) => r.status === 302 });
}
