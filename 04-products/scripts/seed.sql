-- Tạo 200k sản phẩm giả để thử tốc độ search với dữ liệu thật.
-- Chạy: Get-Content 04-products/scripts/seed.sql | docker compose -f 04-products/docker-compose.yml exec -T postgres psql -U app -d products
INSERT INTO products (sku, name, price, stock)
SELECT 'SEED-' || g,
       (ARRAY['Áo thun','Quần jean','Giày chạy bộ','Điện thoại','Tai nghe','Bàn phím','Chuột','Balo'])[1 + g % 8]
         || ' ' || (ARRAY['nam','nữ','trẻ em','cao cấp','giá rẻ'])[1 + g % 5] || ' ' || g,
       10000 + (g % 1000) * 1000,
       1000
FROM generate_series(1, 200000) g
ON CONFLICT (sku) DO NOTHING;
