// Mô phỏng người dùng thật: 80% xem sản phẩm, 15% search, 5% mua. Mỗi user nghỉ 1s giữa 2 thao tác.
// Chạy (trong mạng Docker): docker run --rm --network 04-products_default -v "${PWD}/04-products/loadtest:/scripts" -e BASE_URL=http://nginx -e VUS=2000 grafana/k6 run /scripts/k6.js
// 50k VU cần máy rất mạnh hoặc k6 phân tán; tăng dần 1k -> 5k -> 20k -> 50k.
// Giả lập trình duyệt gọi qua Nginx (TLS, rate limit, chia tải) mà không qua Cloudflare (deploy/vps/README.md):
//   BASE_URL=https://albert.io.vn RESOLVE=albert.io.vn=10.10.0.1 INSECURE=1 FAKE_IPS=1
//   RESOLVE: trỏ domain thẳng vào Nginx 1 máy app; INSECURE: chứng chỉ Origin CA chỉ Cloudflare tin;
//   FAKE_IPS: mỗi VU gửi CF-Connecting-IP riêng như Cloudflare làm → Nginx coi là nhiều khách, rate limit theo IP như thật.
import http from 'k6/http';
import { check, sleep } from 'k6';

// BASE_URL nhận nhiều địa chỉ cách nhau dấu phẩy: mỗi request chọn ngẫu nhiên 1 cái (bắn thẳng vào mọi instance API).
const BASES = (__ENV.BASE_URL || 'http://localhost:8080').split(',').map((s) => s.trim()).filter(Boolean);
const BASE = BASES[0]; // setup (tạo dữ liệu) chỉ cần 1 địa chỉ
const pick = () => BASES[Math.floor(Math.random() * BASES.length)];
const VUS = Number(__ENV.VUS || 1000);
const ADMIN_EMAIL = __ENV.ADMIN_EMAIL || 'admin@example.com';
const ADMIN_PASSWORD = __ENV.ADMIN_PASSWORD || 'admin12345';
const FAKE_IPS = !!__ENV.FAKE_IPS;
const [RESOLVE_HOST, RESOLVE_IP] = (__ENV.RESOLVE || '').split('=');
const WORDS = ['áo', 'quần', 'giày', 'điện thoại', 'tai nghe', 'bàn phím', 'nam', 'nữ'];

export const options = {
  insecureSkipTLSVerify: !!__ENV.INSECURE,
  hosts: RESOLVE_IP ? { [RESOLVE_HOST]: RESOLVE_IP } : {},
  stages: [
    { duration: '1m', target: VUS },
    { duration: '3m', target: VUS },
    { duration: '30s', target: 0 },
  ],
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<200'],
    // Tách theo endpoint để biết chậm ở đâu.
    'http_req_duration{name:get}': ['p(95)<100'],
    'http_req_duration{name:search}': ['p(95)<200'],
    'http_req_duration{name:purchase}': ['p(95)<200'],
  },
};

// IP giả cố định theo VU (1 VU = 1 khách). Trong setup (__VU = 0) mỗi request 1 IP khác để không tự dính rate limit.
function clientIP() {
  const n = __VU > 0 ? __VU : Math.floor(Math.random() * 16000000) + 1;
  return `100.${(n >> 16) & 255}.${(n >> 8) & 255}.${n & 255}`;
}

// Header giống trình duyệt FE gửi: JSON, nén gzip, token nếu đã đăng nhập.
function headers(token) {
  const h = { 'Content-Type': 'application/json', Accept: 'application/json', 'Accept-Encoding': 'gzip' };
  if (token) h.Authorization = `Bearer ${token}`;
  if (FAKE_IPS) h['CF-Connecting-IP'] = clientIP();
  return { headers: h };
}

// Dừng ngay nếu bước chuẩn bị lỗi, tránh chạy test với dữ liệu rỗng rồi ra số liệu sai.
function must(res, status, what) {
  if (res.status !== status) throw new Error(`setup: ${what} lỗi ${res.status}: ${res.body}`);
  return res;
}

function login(email, password) {
  return must(http.post(`${BASE}/api/v1/auth/login`, JSON.stringify({ email, password }), headers()), 200, 'đăng nhập')
    .json('data.access_token');
}

export function setup() {
  const run = Date.now();
  const admin = login(ADMIN_EMAIL, ADMIN_PASSWORD);
  const ids = [];
  for (let i = 0; i < 200; i++) {
    const res = http.post(`${BASE}/api/v1/products`, JSON.stringify({
      sku: `K6-${run}-${i}`, name: `${WORDS[i % WORDS.length]} k6 ${i}`, price: 100000, stock: 1000000000,
    }), headers(admin));
    ids.push(must(res, 201, 'tạo sản phẩm').json('data.id'));
  }
  // 1 tài khoản khách dùng chung cho mọi VU khi mua (access token sống 15 phút > thời gian test).
  const email = `k6-${run}@test.vn`;
  must(http.post(`${BASE}/api/v1/auth/register`, JSON.stringify({ email, password: 'password123', name: 'k6' }), headers()), 201, 'đăng ký');
  return { ids, user: login(email, 'password123') };
}

// 409 khi mua là kết quả đúng (hết hàng), không tính là lỗi.
const purchaseOK = http.expectedStatuses(200, 409);

export default function (data) {
  const id = data.ids[Math.floor(Math.random() * data.ids.length)];
  const r = Math.random();
  const BASE = pick();
  let res;
  if (r < 0.8) {
    res = http.get(`${BASE}/api/v1/products/${id}`, { ...headers(), tags: { name: 'get' } });
    check(res, { 'get 200': (x) => x.status === 200 });
  } else if (r < 0.95) {
    const q = encodeURIComponent(WORDS[Math.floor(Math.random() * WORDS.length)]);
    res = http.get(`${BASE}/api/v1/products?q=${q}&limit=20`, { ...headers(), tags: { name: 'search' } });
    check(res, { 'search 200': (x) => x.status === 200 });
  } else {
    res = http.post(`${BASE}/api/v1/products/${id}/purchase`, JSON.stringify({ quantity: 1 }),
      { ...headers(data.user), responseCallback: purchaseOK, tags: { name: 'purchase' } });
    check(res, { 'purchase 200/409': (x) => x.status === 200 || x.status === 409 });
  }
  sleep(1);
}
