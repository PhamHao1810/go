// Load test các tính năng tài khoản/ghi mà k6-catalog.js không có. Chạy song song với k6-catalog.js khi cần tải duyệt nền.
// MODE (mỗi chế độ tăng dần số lượt/giây theo RATES, mỗi mức giữ HOLD giây):
//   auth   — đăng nhập (80%) / đăng ký (20%), sau đó làm mới token + xem tài khoản. bcrypt nằm ở đây.
//   upload — xin link upload → PUT ảnh SIZE byte vào kho ảnh → đặt làm avatar.
//   admin  — admin đọc 1 sản phẩm rồi ghi lại biến thể (giữ nguyên giá/kho, chỉ tăng version) → xoá cache + đẩy Meilisearch.
//   review — đặt hàng → admin đẩy đơn tới delivered → khách viết đánh giá (chỉ khách đã nhận hàng được đánh giá).
//   mix    — tốc độ cố định cho cả nhóm (RATE lượt/giây, chia theo tỉ lệ bên dưới), chạy DURATION giây.
// Chung: BASE_URL, RESOLVE, INSECURE, FAKE_IPS (giống k6-catalog.js), PRODUCT_MIN/MAX, ADMIN_EMAIL/ADMIN_PASSWORD (admin, mix).
// Mỗi lượt giả 1 IP khách khác nhau (FAKE_IPS) — đúng với nhiều người đăng nhập cùng lúc, không dính rate limit theo IP.
import http from 'k6/http';
import { check, fail } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const API = `${BASE}/api/v1`;
const MODE = __ENV.MODE || 'auth';
const RATES = (__ENV.RATES || '4,8,16').split(',').map(Number);
const HOLD = Number(__ENV.HOLD || 60);
const PMIN = Number(__ENV.PRODUCT_MIN || 1);
const PMAX = Number(__ENV.PRODUCT_MAX || 1000000);
const SIZE = Number(__ENV.SIZE || 100000);
const USERS = Number(__ENV.USERS || 100);
const [RESOLVE_HOST, RESOLVE_IP] = (__ENV.RESOLVE || '').split('=');

function stages() {
  const s = [];
  for (const r of RATES) s.push({ duration: '15s', target: r }, { duration: `${HOLD}s`, target: r });
  s.push({ duration: '5s', target: 0 });
  return s;
}
const ramp = (exec) => ({ executor: 'ramping-arrival-rate', exec, startRate: 0, timeUnit: '1s', preAllocatedVUs: 50, maxVUs: 400, stages: stages() });
const fixed = (exec, rate) => ({ executor: 'constant-arrival-rate', exec, rate: Math.max(1, Math.round(rate * 60)), timeUnit: '1m',
  duration: `${__ENV.DURATION || 300}s`, preAllocatedVUs: 20, maxVUs: 200 });

// mix: phần của RATE cho từng tính năng (ước lượng 1 shop thật: xem tài khoản/làm mới token nhiều, ghi ít).
const MIX = { auth: 0.25, session: 0.30, myOrders: 0.25, cancel: 0.05, review: 0.08, upload: 0.03, admin: 0.04 };
const RATE = Number(__ENV.RATE || 10);

export const options = {
  insecureSkipTLSVerify: !!__ENV.INSECURE,
  hosts: RESOLVE_IP ? { [RESOLVE_HOST]: RESOLVE_IP } : {},
  setupTimeout: '5m',
  scenarios: MODE === 'mix'
    ? Object.fromEntries(Object.entries(MIX).map(([k, v]) => [k, fixed(k, RATE * v)]))
    : { [MODE]: ramp(MODE) },
};

let ipn = Math.floor(Math.random() * 1e6);
function params(name, token, extra) {
  const h = { 'Content-Type': 'application/json', Accept: 'application/json', 'Accept-Encoding': 'gzip' };
  if (token) h.Authorization = `Bearer ${token}`;
  if (__ENV.FAKE_IPS) { ipn++; h['CF-Connecting-IP'] = `100.${64 + (__VU % 64)}.${(ipn >> 8) & 255}.${ipn & 255}`; }
  return Object.assign({ headers: h, tags: { name } }, extra || {});
}
const pick = (a) => a[Math.floor(Math.random() * a.length)];
const productId = () => PMIN + Math.floor(Math.pow(Math.random(), 2) * (PMAX - PMIN + 1));
const json = (o) => JSON.stringify(o);

function must(res, status, what) {
  if (res.status !== status) fail(`setup: ${what} lỗi ${res.status}: ${res.body}`);
  return res;
}
function login(email, password, name) {
  const r = http.post(`${API}/auth/login`, json({ email, password }), params(name || 'login'));
  return r.status === 200 ? r.json('data') : null;
}

export function setup() {
  // Tạo sẵn USERS tài khoản (đăng nhập lại ở auth; có token cho upload/đơn/đánh giá). Mật khẩu thật, bcrypt thật.
  const tag = `${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
  const users = [];
  for (let i = 0; i < USERS; i++) {
    const email = `k6acc-${tag}-${i}@test.vn`;
    must(http.post(`${API}/auth/register`, json({ email, password: 'password123', name: `k6 ${i}` }), params('setup')), 201, 'đăng ký');
    const t = login(email, 'password123', 'setup');
    if (!t) fail('setup: đăng nhập lỗi');
    users.push({ email, access: t.access_token, refresh: t.refresh_token });
  }
  let admin = null;
  if (MODE === 'admin' || MODE === 'review' || MODE === 'mix') {
    const t = login(__ENV.ADMIN_EMAIL, __ENV.ADMIN_PASSWORD, 'setup');
    if (!t) fail('setup: admin đăng nhập lỗi (ADMIN_EMAIL/ADMIN_PASSWORD)');
    admin = t.access_token;
  }
  return { users, admin, tag };
}

// Đăng nhập (hoặc đăng ký mới) → làm mới token → xem tài khoản: đúng 1 phiên mở app.
export function auth(d) {
  let t;
  if (Math.random() < 0.2) {
    const email = `k6acc-${d.tag}-n${__VU}-${__ITER}-${Math.floor(Math.random() * 1e9)}@test.vn`;
    const r = http.post(`${API}/auth/register`, json({ email, password: 'password123', name: 'k6 new' }), params('register'));
    check(r, { 'register 201': (x) => x.status === 201 });
    if (r.status !== 201) return;
    t = login(email, 'password123');
  } else {
    t = login(pick(d.users).email, 'password123');
  }
  check(t, { 'login 200': (x) => !!x });
  if (!t) return;
  const r = http.post(`${API}/auth/refresh`, json({ refresh_token: t.refresh_token }), params('refresh'));
  check(r, { 'refresh 200': (x) => x.status === 200 });
  check(http.get(`${API}/me`, params('me', t.access_token)), { 'me 200': (x) => x.status === 200 });
}

// Khách đã đăng nhập: xem tài khoản (token sẵn có). Làm mới token cũng ở auth().
export function session(d) {
  check(http.get(`${API}/me`, params('me', pick(d.users).access)), { 'me 200': (x) => x.status === 200 });
}

export function myOrders(d) {
  const u = pick(d.users);
  const r = http.get(`${API}/orders?limit=20`, params('my_orders', u.access));
  check(r, { 'orders 200': (x) => x.status === 200 });
  const items = r.status === 200 ? r.json('data') || [] : [];
  if (items.length) check(http.get(`${API}/orders/${pick(items).id}`, params('order_get', u.access)), { 'order 200': (x) => x.status === 200 });
}

// Đặt rồi huỷ (trả kho) — không để lại tồn kho bị giữ.
export function cancel(d) {
  const u = pick(d.users);
  const p = http.get(`${API}/products/${productId()}`, params('detail'));
  if (p.status !== 200) return;
  const v = (p.json('data.variants') || [])[0];
  if (!v) return;
  const o = http.post(`${API}/orders`, json({ items: [{ variant_id: v.id, quantity: 1 }] }), params('order', u.access,
    { responseCallback: http.expectedStatuses(201, 409) }));
  if (o.status !== 201) return;
  check(http.post(`${API}/orders/${o.json('data.id')}/cancel`, null, params('order_cancel', u.access)), { 'cancel 200': (x) => x.status === 200 });
}

// Chỉ khách đã NHẬN hàng mới được đánh giá: đặt hàng → admin đẩy paid → shipped → delivered → khách viết đánh giá.
export function review(d) {
  const u = pick(d.users);
  const id = productId();
  const p = http.get(`${API}/products/${id}`, params('detail'));
  const v = p.status === 200 ? (p.json('data.variants') || [])[0] : null;
  if (!v) return;
  const o = http.post(`${API}/orders`, json({ items: [{ variant_id: v.id, quantity: 1 }] }), params('order', u.access,
    { responseCallback: http.expectedStatuses(201, 409) }));
  if (o.status !== 201) return;
  for (const s of ['paid', 'shipped', 'delivered']) {
    const r = http.put(`${API}/orders/${o.json('data.id')}/status`, json({ status: s }), params('order_status', d.admin));
    if (!check(r, { 'status 200': (x) => x.status === 200 })) return;
  }
  const r = http.post(`${API}/products/${id}/reviews`, json({ rating: 1 + Math.floor(Math.random() * 5), content: 'k6: hàng ổn, giao nhanh' }),
    params('review_write', u.access, { responseCallback: http.expectedStatuses(201, 409) })); // 409: đã đánh giá sản phẩm này
  check(r, { 'review 201/409': (x) => x.status === 201 || x.status === 409 });
}

// Ảnh PNG giả SIZE byte: đúng chữ ký PNG ở đầu (server kiểm magic bytes), phần sau là byte rác.
const png = (() => {
  const b = new Uint8Array(SIZE);
  b.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 0x49, 0x48, 0x44, 0x52]);
  for (let i = 16; i < SIZE; i++) b[i] = (i * 7919) & 255;
  return b.buffer;
})();

export function upload(d) {
  const u = pick(d.users);
  const r = http.post(`${API}/assets`, json({ content_type: 'image/png', size: SIZE }), params('upload_create', u.access));
  check(r, { 'asset 201': (x) => x.status === 201 });
  if (r.status !== 201) return;
  const a = r.json('data');
  const h = Object.assign({}, a.headers, { 'Content-Length': String(SIZE) });
  const put = http.put(a.upload_url, png, { headers: h, tags: { name: 'upload_put' } });
  check(put, { 'put 200': (x) => x.status === 200 });
  if (put.status !== 200) return;
  check(http.put(`${API}/me/avatar`, json({ asset_id: a.asset_id }), params('avatar_set', u.access)), { 'avatar 200': (x) => x.status === 200 });
}

// Admin "sửa" 1 biến thể: ghi lại đúng giá/kho đang có (không đổi dữ liệu thật) — vẫn là 1 lần ghi + xoá cache + đẩy Meilisearch.
export function admin(d) {
  const id = productId();
  const p = http.get(`${API}/products/${id}`, params('admin_read', d.admin));
  if (p.status !== 200) return;
  const v = (p.json('data.variants') || [])[0];
  if (!v) return;
  const r = http.put(`${API}/products/${id}/variants/${v.id}`,
    json({ sku: v.sku, color: v.color, size: v.size, price: v.price, stock: v.stock, version: v.version }),
    params('admin_edit', d.admin, { responseCallback: http.expectedStatuses(200, 409) })); // 409: khách vừa mua, version đổi
  check(r, { 'edit 200/409': (x) => x.status === 200 || x.status === 409 });
}
