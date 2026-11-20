// Load test trên catalog lớn (scripts/seed-catalog.sql), mô phỏng khách thật:
//   40% tìm kiếm (có dấu / không dấu / sai chính tả, kèm lọc + sắp xếp + lật trang) — Meilisearch
//   25% xem chi tiết sản phẩm (đủ biến thể, thuộc tính, tag, danh mục) — Redis cache + Postgres
//   15% duyệt danh mục (danh mục cha gồm cả con, keyset) — Postgres (đọc standby nếu theo kịp)
//   12% xem đánh giá — Postgres
//    8% đặt hàng 1-3 món (xem chi tiết rồi đặt biến thể của nó) — transaction trừ kho
// Mỗi khách nghỉ 1-3 giây giữa 2 thao tác (người thật không bấm liên tục).
//
// Biến môi trường: BASE_URL, VUS, PRODUCT_MIN, PRODUCT_MAX (khoảng id sản phẩm seed), ADMIN không cần.
// NO_SEARCH=1: bỏ lượt tìm kiếm (đo sức chịu của API + Postgres + Redis khi Meilisearch không phải nút thắt),
//   các thao tác còn lại giữ nguyên tỉ lệ với nhau.
// Gọi thẳng Nginx bỏ qua Cloudflare: RESOLVE=albert.io.vn=<ip> INSECURE=1 FAKE_IPS=1 (giống loadtest/k6.js).
import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const VUS = Number(__ENV.VUS || 500);
const PMIN = Number(__ENV.PRODUCT_MIN || 1);
const PMAX = Number(__ENV.PRODUCT_MAX || 1000000);
const FAKE_IPS = !!__ENV.FAKE_IPS;
const SKIP = __ENV.NO_SEARCH ? 0.40 : 0; // 0.40 = phần của search bên dưới
const [RESOLVE_HOST, RESOLVE_IP] = (__ENV.RESOLVE || '').split('=');

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
    'http_req_duration{name:search}': ['p(95)<300'],
    'http_req_duration{name:detail}': ['p(95)<200'],
    'http_req_duration{name:category}': ['p(95)<300'],
    'http_req_duration{name:reviews}': ['p(95)<200'],
    'http_req_duration{name:order}': ['p(95)<500'],
  },
};

// Từ khoá như người thật gõ: có dấu, không dấu, sai chính tả, ghép hãng/màu.
const QUERIES = [
  'áo thun nam', 'ao thun nam', 'ao thun', 'áo khoác', 'ao khoac nu', 'quần jean', 'quan jean nu', 'váy maxi', 'dam cong so',
  'giày chạy bộ', 'giay chay bo', 'giay the thao nam', 'dép', 'túi xách', 'tui xach nu', 'balo du lich',
  'điện thoại', 'dien thoai', 'dien thaoi', 'điện thoại thông minh', 'tai nghe bluetooth', 'tai nge', 'loa bluetooth',
  'sạc dự phòng', 'sac du phong', 'ốp lưng', 'laptop gaming', 'lap top', 'laptop van phong', 'bàn phím cơ', 'ban phim co',
  'chuột không dây', 'man hinh may tinh', 'router wifi', 'smart tivi', 'tivi 4k', 'tu lanh', 'máy giặt', 'dieu hoa',
  'nồi chiên không dầu', 'noi chien khong dau', 'noi com dien', 'robot hút bụi', 'robot hut bui', 'chăn ga gối',
  'kem chống nắng', 'kem chong nang', 'son môi', 'son moi', 'serum', 'nuoc hoa nu', 'sữa rửa mặt', 'tã bỉm', 'ta bim',
  'đồ chơi lắp ráp', 'xe day em be', 'thảm yoga', 'tham yoga', 'lều cắm trại', 'vali keo', 'sách kinh tế', 'sach ky nang',
  'mũ bảo hiểm', 'mu bao hiem', 'camera hành trình', 'cà phê', 'ca phe', 'banh quy', 'gạo', 'khau trang',
  'chính hãng', 'freeship', 'giảm giá', 'cao cấp', 'đen', 'den', 'xanh duong', 'cotton', 'việt nam', 'da that',
  'SaoMai', 'VietTech', 'AnHome', 'MinhStar', 'vinatex', 'phat smart',
];
const SORTS = ['', '', '', 'price', '-price', '-sold', '-rating', '-created'];
const COLORS = ['Đen', 'Trắng', 'Đỏ', 'Xanh dương', 'Hồng', 'Xám'];
const SIZES = ['S', 'M', 'L', 'XL'];

const pick = (a) => a[Math.floor(Math.random() * a.length)];
const productId = () => PMIN + Math.floor(Math.pow(Math.random(), 2) * (PMAX - PMIN + 1)); // lệch: hàng "hot" được xem nhiều

function clientIP() {
  const n = __VU > 0 ? __VU : Math.floor(Math.random() * 16000000) + 1;
  return `100.${(n >> 16) & 255}.${(n >> 8) & 255}.${n & 255}`;
}

function params(name, token, extra) {
  const h = { 'Content-Type': 'application/json', Accept: 'application/json', 'Accept-Encoding': 'gzip' };
  if (token) h.Authorization = `Bearer ${token}`;
  if (FAKE_IPS) h['CF-Connecting-IP'] = clientIP();
  return Object.assign({ headers: h, tags: { name } }, extra || {});
}

function must(res, status, what) {
  if (res.status !== status) throw new Error(`setup: ${what} lỗi ${res.status}: ${res.body}`);
  return res;
}

export function setup() {
  const cats = must(http.get(`${BASE}/api/v1/categories`, params('setup')), 200, 'danh mục').json('data');
  const ids = [];
  const walk = (nodes) => nodes.forEach((n) => { ids.push(n.id); walk(n.children || []); });
  walk(cats);
  const email = `k6cat-${Date.now()}-${Math.floor(Math.random() * 1e6)}@test.vn`; // nhiều k6 chạy song song không trùng email
  must(http.post(`${BASE}/api/v1/auth/register`, JSON.stringify({ email, password: 'password123', name: 'k6' }), params('setup')), 201, 'đăng ký');
  const token = must(http.post(`${BASE}/api/v1/auth/login`, JSON.stringify({ email, password: 'password123' }), params('setup')), 200, 'đăng nhập')
    .json('data.access_token');
  return { categories: ids, token };
}

const okOrder = http.expectedStatuses(201, 409); // 409 = hết hàng, đúng nghiệp vụ

export default function (data) {
  const r = SKIP + Math.random() * (1 - SKIP);
  if (r < 0.40) {
    let url = `${BASE}/api/v1/search?q=${encodeURIComponent(pick(QUERIES))}&limit=20`;
    const s = pick(SORTS);
    if (s) url += `&sort=${s}`;
    if (Math.random() < 0.3) url += `&category_id=${pick(data.categories)}`;
    if (Math.random() < 0.2) url += `&color=${encodeURIComponent(pick(COLORS))}`;
    if (Math.random() < 0.1) url += `&size=${pick(SIZES)}`;
    if (Math.random() < 0.2) url += `&max_price=${pick([200000, 500000, 1000000, 5000000])}`;
    if (Math.random() < 0.2) url += '&in_stock=true';
    if (Math.random() < 0.2) url += `&page=${2 + Math.floor(Math.random() * 3)}`;
    else if (Math.random() < 0.5) url += '&facets=true'; // lần đầu mở trang tìm kiếm: hiện bộ lọc kèm số lượng
    const res = http.get(url, params('search'));
    check(res, { 'search 200': (x) => x.status === 200 });
  } else if (r < 0.65) {
    const res = http.get(`${BASE}/api/v1/products/${productId()}`, params('detail'));
    check(res, { 'detail 200': (x) => x.status === 200 });
  } else if (r < 0.80) {
    const res = http.get(`${BASE}/api/v1/products?category_id=${pick(data.categories)}&limit=20${Math.random() < 0.3 ? '&sort=price' : ''}`, params('category'));
    check(res, { 'category 200': (x) => x.status === 200 });
  } else if (r < 0.92) {
    const res = http.get(`${BASE}/api/v1/products/${productId()}/reviews?limit=10`, params('reviews'));
    check(res, { 'reviews 200': (x) => x.status === 200 });
  } else {
    const p = http.get(`${BASE}/api/v1/products/${productId()}`, params('detail'));
    if (p.status === 200) {
      const variants = p.json('data.variants') || [];
      const items = [];
      for (let i = 0, n = 1 + Math.floor(Math.random() * Math.min(3, variants.length)); i < n; i++) {
        items.push({ variant_id: variants[i].id, quantity: 1 });
      }
      const res = http.post(`${BASE}/api/v1/orders`, JSON.stringify({ items }), params('order', data.token, { responseCallback: okOrder }));
      check(res, { 'order 201/409': (x) => x.status === 201 || x.status === 409 });
    }
  }
  sleep(1 + Math.random() * 2);
}
