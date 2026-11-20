-- Seed catalog lớn để load test: N sản phẩm (mặc định 1.000.000) kèm đủ quan hệ:
--   ~372 danh mục 3 cấp, 500 hãng, biến thể màu/size (1-5/sản phẩm), tag, thuộc tính,
--   20.000 khách, đánh giá (~3/sản phẩm, lệch: vài sản phẩm rất nhiều), ~N đơn hàng (1-4 món/đơn).
-- Mọi dòng seed nhận diện được để xoá: sku 'SD-%', email '%@seed.vn', slug 'sd-%' (xem unseed-catalog.sql).
--
-- Chạy trên máy DB primary (~20-40 phút cho 1 triệu trên VPS 2 CPU):
--   docker compose exec -T postgres psql -U app -d products -v n=1000000 < seed-catalog.sql
-- Sau đó: dựng lại index search (README: "Seed dữ liệu lớn").
--
-- Tắt trigger trong lúc seed (session_replication_role = replica): không tính tổng kho từng dòng, không ghi
-- 10 triệu dòng outbox. Tổng hợp giá/kho/điểm/đã bán làm 1 lần ở cuối, bằng câu UPDATE theo lô.
\set ON_ERROR_STOP on
\if :{?n}
\else
  \set n 1000000
\endif
\timing on
SET session_replication_role = replica;

-- ===== Danh mục: 12 gốc → 5 nhóm → 5 loại sản phẩm (lá) =====
CREATE TEMP TABLE seed_tree (root TEXT, mid TEXT, leaves TEXT[], price_min INT, price_max INT, sized BOOL);
INSERT INTO seed_tree VALUES
('Thời trang nam','Áo nam',ARRAY['Áo thun nam','Áo sơ mi nam','Áo polo nam','Áo khoác nam','Áo hoodie nam'],99000,1200000,true),
('Thời trang nam','Quần nam',ARRAY['Quần jean nam','Quần kaki nam','Quần short nam','Quần tây nam','Quần jogger nam'],149000,900000,true),
('Thời trang nam','Giày nam',ARRAY['Giày thể thao nam','Giày tây nam','Giày lười nam','Dép quai hậu nam','Dép lê nam'],99000,2500000,true),
('Thời trang nam','Phụ kiện nam',ARRAY['Thắt lưng nam','Ví da nam','Mũ lưỡi trai','Kính mát nam','Cà vạt'],59000,900000,false),
('Thời trang nam','Đồ lót nam',ARRAY['Quần lót nam','Áo ba lỗ nam','Tất nam','Đồ ngủ nam','Áo giữ nhiệt nam'],39000,400000,true),
('Thời trang nữ','Áo nữ',ARRAY['Áo thun nữ','Áo sơ mi nữ','Áo kiểu nữ','Áo khoác nữ','Áo len nữ'],89000,1100000,true),
('Thời trang nữ','Váy đầm',ARRAY['Đầm công sở','Váy maxi','Chân váy','Đầm dự tiệc','Váy babydoll'],129000,1800000,true),
('Thời trang nữ','Quần nữ',ARRAY['Quần jean nữ','Quần ống rộng','Quần legging','Quần short nữ','Quần tây nữ'],119000,800000,true),
('Thời trang nữ','Túi ví nữ',ARRAY['Túi xách nữ','Túi đeo chéo','Ví nữ','Balo nữ','Túi tote'],99000,3500000,false),
('Thời trang nữ','Giày nữ',ARRAY['Giày cao gót','Giày búp bê','Sandal nữ','Giày sneaker nữ','Dép nữ'],99000,1500000,true),
('Điện thoại & Máy tính bảng','Điện thoại',ARRAY['Điện thoại thông minh','Điện thoại phổ thông','Điện thoại gập','Điện thoại chơi game','Điện thoại người già'],390000,35000000,false),
('Điện thoại & Máy tính bảng','Máy tính bảng',ARRAY['Máy tính bảng Android','Máy tính bảng học tập','Máy đọc sách','Máy tính bảng trẻ em','Bút cảm ứng'],290000,25000000,false),
('Điện thoại & Máy tính bảng','Phụ kiện điện thoại',ARRAY['Ốp lưng điện thoại','Cáp sạc','Củ sạc nhanh','Sạc dự phòng','Kính cường lực'],29000,900000,false),
('Điện thoại & Máy tính bảng','Âm thanh di động',ARRAY['Tai nghe bluetooth','Tai nghe có dây','Loa bluetooth','Tai nghe chụp tai','Micro thu âm'],79000,6000000,false),
('Điện thoại & Máy tính bảng','Thiết bị đeo',ARRAY['Đồng hồ thông minh','Vòng đeo tay thông minh','Dây đeo đồng hồ','Kính thực tế ảo','Nhẫn thông minh'],199000,12000000,false),
('Máy tính & Laptop','Laptop',ARRAY['Laptop văn phòng','Laptop gaming','Laptop mỏng nhẹ','Laptop đồ hoạ','Laptop 2 trong 1'],5900000,60000000,false),
('Máy tính & Laptop','Linh kiện',ARRAY['Ổ cứng SSD','RAM máy tính','Card màn hình','Bộ nguồn máy tính','Bo mạch chủ'],390000,30000000,false),
('Máy tính & Laptop','Thiết bị ngoại vi',ARRAY['Bàn phím cơ','Chuột không dây','Màn hình máy tính','Webcam','Tai nghe gaming'],99000,15000000,false),
('Máy tính & Laptop','Thiết bị mạng',ARRAY['Router wifi','Bộ kích sóng wifi','Switch mạng','Dây mạng','Camera wifi'],89000,5000000,false),
('Máy tính & Laptop','Thiết bị văn phòng',ARRAY['Máy in','Mực in','Máy chiếu','Ổ cứng di động','USB'],59000,20000000,false),
('Điện tử & Điện lạnh','Tivi',ARRAY['Smart tivi','Tivi OLED','Tivi 4K','Android tivi','Tivi giá rẻ'],2900000,60000000,false),
('Điện tử & Điện lạnh','Âm thanh',ARRAY['Loa soundbar','Dàn karaoke','Loa kéo','Amply','Micro karaoke'],390000,25000000,false),
('Điện tử & Điện lạnh','Điện lạnh',ARRAY['Tủ lạnh','Máy giặt','Điều hoà','Máy sấy quần áo','Tủ đông'],2900000,40000000,false),
('Điện tử & Điện lạnh','Máy ảnh',ARRAY['Máy ảnh mirrorless','Ống kính máy ảnh','Camera hành trình','Máy quay phim','Tripod'],390000,50000000,false),
('Điện tử & Điện lạnh','Gia dụng điện',ARRAY['Nồi cơm điện','Nồi chiên không dầu','Máy xay sinh tố','Lò vi sóng','Ấm siêu tốc'],190000,8000000,false),
('Nhà cửa & Đời sống','Nhà bếp',ARRAY['Bộ nồi inox','Chảo chống dính','Dao nhà bếp','Hộp đựng thực phẩm','Bình giữ nhiệt'],49000,3000000,false),
('Nhà cửa & Đời sống','Phòng ngủ',ARRAY['Chăn ga gối','Nệm cao su','Gối tựa','Rèm cửa','Đèn ngủ'],99000,12000000,false),
('Nhà cửa & Đời sống','Nội thất',ARRAY['Bàn làm việc','Ghế công thái học','Kệ sách','Tủ quần áo','Sofa'],290000,25000000,false),
('Nhà cửa & Đời sống','Dọn dẹp',ARRAY['Robot hút bụi','Máy hút bụi','Cây lau nhà','Nước giặt','Nước rửa chén'],29000,15000000,false),
('Nhà cửa & Đời sống','Trang trí',ARRAY['Tranh treo tường','Đồng hồ treo tường','Cây giả','Nến thơm','Thảm trải sàn'],49000,3000000,false),
('Sắc đẹp','Chăm sóc da',ARRAY['Sữa rửa mặt','Kem chống nắng','Serum dưỡng da','Toner','Kem dưỡng ẩm'],59000,2500000,false),
('Sắc đẹp','Trang điểm',ARRAY['Son môi','Kem nền','Phấn phủ','Mascara','Kẻ mắt'],49000,1500000,false),
('Sắc đẹp','Chăm sóc tóc',ARRAY['Dầu gội','Dầu xả','Máy sấy tóc','Máy uốn tóc','Thuốc nhuộm tóc'],49000,3000000,false),
('Sắc đẹp','Nước hoa',ARRAY['Nước hoa nam','Nước hoa nữ','Xịt thơm toàn thân','Nước hoa mini','Lăn khử mùi'],59000,5000000,false),
('Sắc đẹp','Chăm sóc cơ thể',ARRAY['Sữa tắm','Tẩy tế bào chết','Dưỡng thể','Kem đánh răng','Bàn chải điện'],39000,2500000,false),
('Mẹ & Bé','Đồ cho bé',ARRAY['Tã bỉm','Sữa bột','Bình sữa','Xe đẩy em bé','Ghế ăn dặm'],59000,9000000,false),
('Mẹ & Bé','Thời trang trẻ em',ARRAY['Quần áo sơ sinh','Váy bé gái','Áo thun bé trai','Giày trẻ em','Mũ trẻ em'],49000,600000,true),
('Mẹ & Bé','Đồ chơi',ARRAY['Đồ chơi lắp ráp','Búp bê','Xe điều khiển','Đồ chơi giáo dục','Bộ xếp hình'],49000,3000000,false),
('Mẹ & Bé','Đồ cho mẹ',ARRAY['Máy hút sữa','Đồ bầu','Gối bầu','Túi bỉm sữa','Áo choàng cho con bú'],99000,6000000,false),
('Mẹ & Bé','An toàn cho bé',ARRAY['Nhiệt kế điện tử','Máy hút mũi','Camera trông trẻ','Chặn cửa','Địu em bé'],59000,3000000,false),
('Thể thao & Du lịch','Đồ thể thao',ARRAY['Giày chạy bộ','Quần áo tập gym','Đồ bơi','Áo bóng đá','Tất thể thao'],79000,4000000,true),
('Thể thao & Du lịch','Dụng cụ tập',ARRAY['Tạ tay','Thảm yoga','Dây kháng lực','Máy chạy bộ','Xe đạp tập'],59000,20000000,false),
('Thể thao & Du lịch','Dã ngoại',ARRAY['Lều cắm trại','Túi ngủ','Bếp dã ngoại','Đèn pin','Ghế xếp'],79000,5000000,false),
('Thể thao & Du lịch','Vali túi xách',ARRAY['Vali kéo','Balo du lịch','Túi trống','Túi đựng mỹ phẩm','Gối cổ'],59000,6000000,false),
('Thể thao & Du lịch','Xe đạp',ARRAY['Xe đạp địa hình','Xe đạp trẻ em','Xe đạp gấp','Mũ bảo hiểm xe đạp','Đèn xe đạp'],99000,15000000,false),
('Sách & Văn phòng phẩm','Sách',ARRAY['Sách kinh tế','Sách kỹ năng sống','Tiểu thuyết','Sách thiếu nhi','Sách ngoại ngữ'],39000,600000,false),
('Sách & Văn phòng phẩm','Văn phòng phẩm',ARRAY['Bút bi','Sổ tay','Giấy in','Bìa hồ sơ','Máy tính cầm tay'],5000,900000,false),
('Sách & Văn phòng phẩm','Dụng cụ học sinh',ARRAY['Cặp sách','Hộp bút','Bút chì màu','Thước kẻ','Đèn học'],9000,800000,false),
('Sách & Văn phòng phẩm','Mỹ thuật',ARRAY['Màu nước','Giá vẽ','Cọ vẽ','Giấy vẽ','Bút dạ quang'],15000,1500000,false),
('Sách & Văn phòng phẩm','Quà tặng',ARRAY['Hộp quà','Thiệp chúc mừng','Gấu bông','Sổ da','Bút ký cao cấp'],19000,2000000,false),
('Ô tô & Xe máy','Phụ kiện xe máy',ARRAY['Mũ bảo hiểm','Áo mưa','Găng tay xe máy','Khoá chống trộm','Gương xe máy'],39000,2500000,false),
('Ô tô & Xe máy','Phụ kiện ô tô',ARRAY['Camera hành trình ô tô','Thảm lót sàn ô tô','Nước hoa ô tô','Bơm lốp ô tô','Giá đỡ điện thoại'],59000,6000000,false),
('Ô tô & Xe máy','Dầu nhớt',ARRAY['Dầu nhớt xe máy','Dầu nhớt ô tô','Nước làm mát','Dung dịch vệ sinh','Mỡ bò'],39000,900000,false),
('Ô tô & Xe máy','Lốp xe',ARRAY['Lốp xe máy','Lốp ô tô','Săm xe máy','Keo vá lốp','Van lốp'],29000,4000000,false),
('Ô tô & Xe máy','Đồ bảo hộ',ARRAY['Áo giáp moto','Giày moto','Kính chắn gió','Bịt mặt','Đầu gối bảo hộ'],49000,5000000,false),
('Bách hoá','Đồ uống',ARRAY['Cà phê','Trà','Nước ngọt','Sữa tươi','Nước khoáng'],9000,600000,false),
('Bách hoá','Đồ ăn vặt',ARRAY['Bánh quy','Kẹo','Snack','Hạt dinh dưỡng','Trái cây sấy'],9000,500000,false),
('Bách hoá','Gia vị',ARRAY['Nước mắm','Dầu ăn','Hạt nêm','Tương ớt','Muối'],9000,300000,false),
('Bách hoá','Thực phẩm khô',ARRAY['Gạo','Mì gói','Bún khô','Đậu các loại','Ngũ cốc'],9000,800000,false),
('Bách hoá','Đồ dùng cá nhân',ARRAY['Giấy vệ sinh','Khăn ướt','Băng vệ sinh','Dao cạo râu','Khẩu trang'],9000,400000,false);

INSERT INTO categories (name, slug)
SELECT root, 'sd-' || md5(root) FROM (SELECT DISTINCT root FROM seed_tree) r ON CONFLICT (slug) DO NOTHING;
INSERT INTO categories (parent_id, name, slug)
SELECT c.id, t.mid, 'sd-' || md5(t.root || '/' || t.mid) FROM seed_tree t JOIN categories c ON c.slug = 'sd-' || md5(t.root)
ON CONFLICT (slug) DO NOTHING;
-- trigger tính path bị tắt (replica) → tự tính path/depth cho 2 cấp vừa tạo
UPDATE categories SET path = id::text, depth = 0 WHERE slug LIKE 'sd-%' AND parent_id IS NULL AND path = '';
UPDATE categories c SET path = p.path || '/' || c.id, depth = 1 FROM categories p
WHERE c.parent_id = p.id AND c.slug LIKE 'sd-%' AND c.path = '' AND p.depth = 0;
INSERT INTO categories (parent_id, name, slug)
SELECT m.id, leaf, 'sd-' || md5(t.root || '/' || t.mid || '/' || leaf)
FROM seed_tree t JOIN categories m ON m.slug = 'sd-' || md5(t.root || '/' || t.mid), unnest(t.leaves) leaf
ON CONFLICT (slug) DO NOTHING;
UPDATE categories c SET path = p.path || '/' || c.id, depth = 2 FROM categories p
WHERE c.parent_id = p.id AND c.slug LIKE 'sd-%' AND c.path = '' AND p.depth = 1;

-- Lá đánh số 1..300 để chọn ngẫu nhiên bằng phép JOIN.
CREATE TEMP TABLE seed_leaves AS
SELECT row_number() OVER (ORDER BY c.id) AS idx, c.id, c.name, t.price_min, t.price_max, t.sized
FROM seed_tree t JOIN categories m ON m.slug = 'sd-' || md5(t.root || '/' || t.mid)
JOIN categories c ON c.parent_id = m.id;
CREATE UNIQUE INDEX ON seed_leaves (idx);
SELECT count(*) AS leaves FROM seed_leaves \gset

-- ===== Hãng: 25 x 20 = 500 tên ghép =====
INSERT INTO brands (name, slug)
SELECT a || b, 'sd-' || lower(a || b)
FROM unnest(ARRAY['Sao','Viet','An','Phu','Hoa','Minh','Tan','Kim','Lac','Nam','Bao','Duc','Gia','Hai','Long',
                  'Thien','Phat','Hung','Tam','Quang','Vina','Sai','Ha','Dai','Hong']) a,
     unnest(ARRAY['Mai','Tex','Home','Tech','Star','Life','Care','Plus','Pro','Max','Go','Fit','Mart','Food','Kid',
                  'Style','Smart','Green','Gold','One']) b
ON CONFLICT DO NOTHING;
CREATE TEMP TABLE seed_brands AS SELECT row_number() OVER (ORDER BY id) AS idx, id, name FROM brands WHERE slug LIKE 'sd-%';
CREATE UNIQUE INDEX ON seed_brands (idx);
SELECT count(*) AS nbrands FROM seed_brands \gset

-- ===== Tag + thuộc tính =====
INSERT INTO tags (name)
SELECT unnest(ARRAY['chính hãng','freeship','giảm giá','hàng mới','bán chạy','cao cấp','giá rẻ','quà tặng','trả góp 0%',
  'hàng nhập khẩu','made in vietnam','bảo hành 12 tháng','bảo hành 24 tháng','combo','limited','eco','handmade',
  'flash sale','xả kho','hot trend','unisex','size lớn','chống nước','siêu nhẹ','bền bỉ','tiết kiệm điện','thông minh',
  'không dây','cho bé','cho mẹ','dân văn phòng','sinh viên','du lịch','thể thao','mùa hè','mùa đông','tết','valentine',
  'học online','gaming']) ON CONFLICT DO NOTHING;
CREATE TEMP TABLE seed_tags AS SELECT row_number() OVER (ORDER BY id) AS idx, id FROM tags;
SELECT count(*) AS ntags FROM seed_tags \gset

INSERT INTO attributes (name) SELECT unnest(ARRAY['Chất liệu','Xuất xứ','Bảo hành','Kiểu dáng','Phong cách','Công suất'])
ON CONFLICT DO NOTHING;
INSERT INTO attribute_values (attribute_id, value)
SELECT a.id, v FROM attributes a, LATERAL unnest(CASE a.name
  WHEN 'Chất liệu' THEN ARRAY['Cotton','Polyester','Da thật','Da PU','Nhựa ABS','Inox 304','Nhôm','Gỗ sồi','Thuỷ tinh','Silicone']
  WHEN 'Xuất xứ' THEN ARRAY['Việt Nam','Nhật Bản','Hàn Quốc','Trung Quốc','Thái Lan','Mỹ','Đức','Đài Loan']
  WHEN 'Bảo hành' THEN ARRAY['Không bảo hành','3 tháng','6 tháng','12 tháng','24 tháng','36 tháng']
  WHEN 'Kiểu dáng' THEN ARRAY['Basic','Oversize','Slim fit','Cổ điển','Hiện đại','Mini','Nhỏ gọn']
  WHEN 'Phong cách' THEN ARRAY['Năng động','Công sở','Thanh lịch','Đường phố','Tối giản','Sang trọng']
  ELSE ARRAY['50W','100W','300W','500W','1000W','2000W'] END) v
WHERE a.name IN ('Chất liệu','Xuất xứ','Bảo hành','Kiểu dáng','Phong cách','Công suất')
ON CONFLICT DO NOTHING;
CREATE TEMP TABLE seed_attr_values AS SELECT row_number() OVER (ORDER BY id) AS idx, id FROM attribute_values;
SELECT count(*) AS nvalues FROM seed_attr_values \gset

-- ===== Khách =====
INSERT INTO users (email, password_hash, name, role)
SELECT 'u' || g || '@seed.vn', 'seed-no-login', (ARRAY['Nguyễn','Trần','Lê','Phạm','Hoàng','Huỳnh','Võ','Đặng','Bùi','Đỗ'])[1 + g % 10]
  || ' ' || (ARRAY['Văn','Thị','Minh','Ngọc','Thanh','Quốc','Hữu','Thu'])[1 + g % 8]
  || ' ' || (ARRAY['An','Bình','Châu','Dũng','Giang','Hà','Khoa','Linh','Nam','Phương','Quân','Trang','Tú','Vy'])[1 + g % 14],
  'customer'
FROM generate_series(1, 20000) g ON CONFLICT (email) DO NOTHING;
CREATE TEMP TABLE seed_users AS SELECT row_number() OVER (ORDER BY id) AS idx, id FROM users WHERE email LIKE '%@seed.vn';
CREATE UNIQUE INDEX ON seed_users (idx);
SELECT count(*) AS nusers FROM seed_users \gset

-- ===== Sản phẩm + biến thể + tag + thuộc tính, từng lô 50.000 =====
-- g = số thứ tự sản phẩm (sku 'SD-g'). Số biến thể, kho mỗi biến thể suy ra từ g nên products.price/stock
-- tính đúng ngay lúc INSERT (giá thấp nhất = biến thể đầu tiên, tổng kho = số biến thể x kho mỗi biến thể).
CREATE OR REPLACE PROCEDURE seed_products(lo BIGINT, hi BIGINT, nleaves INT, nbrands INT, ntags INT, nvalues INT)
LANGUAGE plpgsql AS $$
BEGIN
  CREATE TEMP TABLE IF NOT EXISTS seed_new (id BIGINT, g BIGINT, base BIGINT, nvar INT, per BIGINT, sized BOOL);
  TRUNCATE seed_new;
  WITH src AS (
    SELECT g,
      -- lá lệch về đầu danh sách (danh mục phổ biến nhiều hàng hơn)
      1 + floor(power(random(), 1.6) * nleaves)::int AS li,
      1 + floor(random() * nbrands)::int AS bi,
      1 + (g % 5)::int AS nvar,
      CASE WHEN g % 11 = 0 THEN 0 ELSE (g * 37) % 120 + 1 END AS per,
      random() AS r1, random() AS r2
    FROM generate_series(lo, hi) g
  ), ins AS (
    INSERT INTO products (sku, name, description, price, stock, category_id, brand_id, created_at, updated_at)
    SELECT 'SD-' || s.g,
      l.name || ' ' || b.name || ' ' ||
        (ARRAY['cao cấp','chính hãng','giá rẻ','thế hệ mới','phiên bản 2026','siêu bền','nhỏ gọn','sang trọng',
               'thời trang','chống nước','đa năng','tiện lợi','basic','plus','pro','mini','max','lite'])[1 + floor(s.r1 * 18)::int]
        || ' ' || upper(substr(md5(s.g::text), 1, 2)) || (s.g % 1000),
      'Sản phẩm ' || l.name || ' của ' || b.name || '. Hàng mẫu sinh tự động để kiểm tra hiệu năng.',
      (l.price_min + floor(power(s.r2, 2) * (l.price_max - l.price_min)) / 1000 * 1000)::bigint,
      s.nvar * s.per, l.id, b.id,
      now() - (random() * interval '730 days'), now()
    FROM src s JOIN seed_leaves l ON l.idx = s.li JOIN seed_brands b ON b.idx = s.bi
    RETURNING id, sku, price, stock, category_id
  )
  INSERT INTO seed_new
  SELECT ins.id, substr(ins.sku, 4)::bigint, ins.price, 1 + (substr(ins.sku, 4)::bigint % 5)::int,
    CASE WHEN ins.stock = 0 THEN 0 ELSE ins.stock / (1 + (substr(ins.sku, 4)::bigint % 5)) END, l.sized
  FROM ins JOIN seed_leaves l ON l.id = ins.category_id;

  -- Biến thể j = 0..nvar-1: giá tăng 5% mỗi bậc (biến thể 0 = giá thấp nhất = products.price).
  INSERT INTO product_variants (product_id, sku, color, size, price, stock)
  SELECT n.id, 'SD-' || n.g || '-' || j,
    (ARRAY['Đen','Trắng','Đỏ','Xanh dương','Xanh lá','Vàng','Hồng','Xám','Nâu','Be'])[1 + ((n.g + j * 3) % 10)::int],
    CASE WHEN n.sized THEN (ARRAY['S','M','L','XL','XXL'])[1 + j] ELSE '' END,
    n.base + n.base * j * 5 / 100, n.per
  FROM seed_new n, generate_series(0, n.nvar - 1) j;

  -- 0-4 tag, 1-3 thuộc tính mỗi sản phẩm (trùng thì bỏ qua).
  INSERT INTO product_tags (product_id, tag_id)
  SELECT n.id, t.id FROM seed_new n CROSS JOIN LATERAL generate_series(1, (n.g % 5)::int) k
  JOIN seed_tags t ON t.idx = 1 + ((n.g * 7 + k * 13) % ntags)
  ON CONFLICT DO NOTHING;
  INSERT INTO product_attribute_values (product_id, value_id)
  SELECT n.id, v.id FROM seed_new n CROSS JOIN LATERAL generate_series(1, 1 + (n.g % 3)::int) k
  JOIN seed_attr_values v ON v.idx = 1 + ((n.g * 11 + k * 17) % nvalues)
  ON CONFLICT DO NOTHING;
END $$;

SELECT set_config('seed.n', :'n', false), set_config('seed.leaves', :'leaves', false),
  set_config('seed.brands', :'nbrands', false), set_config('seed.tags', :'ntags', false),
  set_config('seed.values', :'nvalues', false), set_config('seed.users', :'nusers', false) \g /dev/null

DO $$
DECLARE
  total BIGINT := current_setting('seed.n')::bigint;
  done BIGINT := COALESCE((SELECT max(substr(sku, 4)::bigint) FROM products WHERE sku LIKE 'SD-%'), 0); -- chạy tiếp nếu lần trước dừng giữa chừng
  lo BIGINT;
BEGIN
  lo := done + 1;
  WHILE lo <= total LOOP
    CALL seed_products(lo, LEAST(lo + 49999, total), current_setting('seed.leaves')::int, current_setting('seed.brands')::int,
      current_setting('seed.tags')::int, current_setting('seed.values')::int);
    COMMIT; -- mỗi lô 1 transaction: không giữ khoá/WAL quá lâu, standby theo kịp
    RAISE NOTICE 'products % / %', LEAST(lo + 49999, total), total;
    lo := lo + 50000;
  END LOOP;
END $$;

-- ===== Đánh giá: phần lớn sản phẩm 0-5, vài sản phẩm "hot" tới 25; điểm lệch về 4-5 sao =====
DO $$
DECLARE
  nusers INT := current_setting('seed.users')::int;
  lo BIGINT; hi BIGINT; maxid BIGINT;
BEGIN
  SELECT min(id), max(id) INTO lo, maxid FROM products WHERE sku LIKE 'SD-%';
  WHILE lo <= maxid LOOP
    hi := lo + 99999;
    INSERT INTO reviews (product_id, user_id, rating, content, created_at)
    SELECT p.id, u.id,
      (ARRAY[5,5,5,5,5,4,4,4,3,2,1])[1 + floor(random() * 11)::int],
      (ARRAY['Hàng đẹp, đúng mô tả','Giao hàng nhanh, đóng gói cẩn thận','Chất lượng tốt so với giá','Sẽ ủng hộ shop tiếp',
             'Tạm ổn','Màu hơi khác ảnh','Đóng gói chưa kỹ','Dùng một tuần thấy ổn','Rất hài lòng','Không như mong đợi',
             ''])[1 + floor(random() * 11)::int],
      p.created_at + random() * (now() - p.created_at)
    FROM products p
    CROSS JOIN LATERAL generate_series(1, floor(power(random() + p.id * 0, 6) * 25)::int) j
    JOIN seed_users u ON u.idx = 1 + ((p.id * 7919 + j * 104729) % nusers)
    WHERE p.id BETWEEN lo AND hi AND p.sku LIKE 'SD-%'
    ON CONFLICT DO NOTHING;
    COMMIT;
    RAISE NOTICE 'reviews: products up to id %', LEAST(hi, maxid);
    lo := hi + 1;
  END LOOP;
END $$;

-- ===== Đơn hàng: n đơn, 1-4 món, phân bố trạng thái gần thực tế =====
DO $$
DECLARE
  total BIGINT := current_setting('seed.n')::bigint;
  nusers INT := current_setting('seed.users')::int;
  vmin BIGINT; vmax BIGINT; done BIGINT := 0; batch BIGINT;
BEGIN
  SELECT min(id), max(id) INTO vmin, vmax FROM product_variants WHERE sku LIKE 'SD-%';
  SELECT count(*) INTO done FROM orders o JOIN users u ON u.id = o.user_id WHERE u.email LIKE '%@seed.vn';
  WHILE done < total LOOP
    batch := LEAST(100000, total - done);
    CREATE TEMP TABLE IF NOT EXISTS seed_orders (id BIGINT);
    TRUNCATE seed_orders;
    WITH ins AS (
      INSERT INTO orders (user_id, status, total, created_at, updated_at)
      SELECT u.id,
        (ARRAY['delivered','delivered','delivered','delivered','delivered','delivered','delivered',
               'shipped','paid','cancelled'])[1 + floor(random() * 10)::int],
        0, ts, ts
      FROM (SELECT now() - random() * interval '700 days' AS ts, 1 + floor(random() * nusers)::int AS ui
            FROM generate_series(1, batch)) g
      JOIN seed_users u ON u.idx = g.ui -- id ngẫu nhiên tính sẵn thành cột: JOIN theo cột mới dùng được index
      RETURNING id
    )
    INSERT INTO seed_orders SELECT id FROM ins;

    INSERT INTO order_items (order_id, variant_id, product_id, name, qty, unit_price)
    SELECT o.id, v.id, v.product_id, p.name, 1 + floor(power(random(), 3) * 3)::int, v.price
    FROM seed_orders o
    CROSS JOIN LATERAL (SELECT vmin + floor(power(random(), 1.5) * (vmax - vmin))::bigint AS vid -- lệch: vài sản phẩm bán rất chạy
                        FROM generate_series(1, 1 + floor(random() * 4 + o.id * 0)::int)) k
    JOIN product_variants v ON v.id = k.vid
    JOIN products p ON p.id = v.product_id;

    UPDATE orders o SET total = t.total
    FROM (SELECT order_id, sum(qty * unit_price) AS total FROM order_items WHERE order_id IN (SELECT id FROM seed_orders)
          GROUP BY order_id) t
    WHERE o.id = t.order_id;
    COMMIT;
    done := done + batch;
    RAISE NOTICE 'orders % / %', done, total;
  END LOOP;
END $$;

-- ===== Tổng hợp điểm đánh giá + số đã bán vào products (thay cho trigger đã tắt), theo lô =====
DO $$
DECLARE lo BIGINT; hi BIGINT; maxid BIGINT;
BEGIN
  SELECT min(id), max(id) INTO lo, maxid FROM products WHERE sku LIKE 'SD-%';
  WHILE lo <= maxid LOOP
    hi := lo + 99999;
    UPDATE products p SET rating_sum = COALESCE(r.s, 0), rating_count = COALESCE(r.c, 0), sold_count = COALESCE(o.q, 0)
    FROM (SELECT id FROM products WHERE id BETWEEN lo AND hi AND sku LIKE 'SD-%') x
    LEFT JOIN (SELECT product_id, sum(rating) AS s, count(*) AS c FROM reviews
               WHERE product_id BETWEEN lo AND hi GROUP BY product_id) r ON r.product_id = x.id
    LEFT JOIN (SELECT i.product_id, sum(i.qty) AS q FROM order_items i JOIN orders od ON od.id = i.order_id
               WHERE i.product_id BETWEEN lo AND hi AND od.status <> 'cancelled' GROUP BY i.product_id) o ON o.product_id = x.id
    WHERE p.id = x.id;
    COMMIT;
    RAISE NOTICE 'aggregates: products up to id %', LEAST(hi, maxid);
    lo := hi + 1;
  END LOOP;
END $$;

RESET session_replication_role;
DROP PROCEDURE seed_products;
ANALYZE;

SELECT (SELECT count(*) FROM products WHERE sku LIKE 'SD-%') AS products,
       (SELECT count(*) FROM product_variants WHERE sku LIKE 'SD-%') AS variants,
       (SELECT count(*) FROM reviews) AS reviews,
       (SELECT count(*) FROM orders) AS orders,
       (SELECT count(*) FROM order_items) AS order_items,
       pg_size_pretty(pg_database_size('products')) AS db_size;
