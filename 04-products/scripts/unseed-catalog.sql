-- Xoá toàn bộ dữ liệu do seed-catalog.sql tạo (sku 'SD-%', email '%@seed.vn', slug 'sd-%'), theo lô.
--   docker compose exec -T postgres psql -U app -d products < unseed-catalog.sql
-- Sau đó dựng lại index search (README: "Seed dữ liệu lớn").
--
-- Tắt trigger (không ghi 1 triệu dòng outbox). Lưu ý: chế độ này cũng tắt ON DELETE CASCADE / SET NULL của khoá
-- ngoại, nên phải tự xoá/gỡ bảng con theo đúng thứ tự dưới đây.
\set ON_ERROR_STOP on
\timing on
SET session_replication_role = replica;

CREATE TEMP TABLE seed_p AS SELECT id FROM products WHERE sku LIKE 'SD-%';
CREATE UNIQUE INDEX ON seed_p (id);
CREATE TEMP TABLE seed_u AS SELECT id FROM users WHERE email LIKE '%@seed.vn';
CREATE UNIQUE INDEX ON seed_u (id);
CREATE TEMP TABLE seed_o AS SELECT o.id FROM orders o JOIN seed_u u ON u.id = o.user_id;
CREATE UNIQUE INDEX ON seed_o (id);
ANALYZE seed_p, seed_u, seed_o;

-- Đơn của khách thật (vd load test) có mua hàng seed: giữ đơn, gỡ liên kết (giống ON DELETE SET NULL).
UPDATE order_items SET product_id = NULL, variant_id = NULL
WHERE product_id IN (SELECT id FROM seed_p) AND order_id NOT IN (SELECT id FROM seed_o);

DO $$
DECLARE n BIGINT;
BEGIN
  LOOP
    DELETE FROM order_items WHERE id IN (SELECT i.id FROM order_items i JOIN seed_o o ON o.id = i.order_id LIMIT 200000);
    GET DIAGNOSTICS n = ROW_COUNT; COMMIT; EXIT WHEN n = 0;
  END LOOP;
  DELETE FROM orders WHERE id IN (SELECT id FROM seed_o); COMMIT;
  LOOP
    DELETE FROM reviews WHERE id IN (SELECT r.id FROM reviews r JOIN seed_p p ON p.id = r.product_id LIMIT 500000);
    GET DIAGNOSTICS n = ROW_COUNT; COMMIT; EXIT WHEN n = 0;
  END LOOP;
  DELETE FROM reviews WHERE user_id IN (SELECT id FROM seed_u); COMMIT;
  DELETE FROM refresh_tokens WHERE user_id IN (SELECT id FROM seed_u); COMMIT;
  DELETE FROM product_tags WHERE product_id IN (SELECT id FROM seed_p); COMMIT;
  DELETE FROM product_attribute_values WHERE product_id IN (SELECT id FROM seed_p); COMMIT;
  DELETE FROM product_images WHERE product_id IN (SELECT id FROM seed_p); COMMIT;
  LOOP
    DELETE FROM product_variants WHERE id IN (SELECT v.id FROM product_variants v JOIN seed_p p ON p.id = v.product_id LIMIT 500000);
    GET DIAGNOSTICS n = ROW_COUNT; COMMIT; EXIT WHEN n = 0;
  END LOOP;
  DELETE FROM products WHERE id IN (SELECT id FROM seed_p); COMMIT;
  DELETE FROM users WHERE id IN (SELECT id FROM seed_u); COMMIT;
  -- Danh mục/hãng seed: sản phẩm thật nào còn dùng thì gỡ ra trước (giống ON DELETE SET NULL).
  UPDATE products SET category_id = NULL WHERE category_id IN (SELECT id FROM categories WHERE slug LIKE 'sd-%');
  UPDATE products SET brand_id = NULL WHERE brand_id IN (SELECT id FROM brands WHERE slug LIKE 'sd-%');
  DELETE FROM categories WHERE slug LIKE 'sd-%' AND depth = 2;
  DELETE FROM categories WHERE slug LIKE 'sd-%' AND depth = 1;
  DELETE FROM categories WHERE slug LIKE 'sd-%';
  DELETE FROM brands WHERE slug LIKE 'sd-%';
  COMMIT;
END $$;

RESET session_replication_role;
VACUUM ANALYZE;
SELECT count(*) AS products_left, pg_size_pretty(pg_database_size('products')) AS db_size FROM products;
