-- Index cho phân trang keyset theo từng kiểu sort: WHERE (cột, id) < ($1, $2) ORDER BY cột, id.
-- ponytail: CREATE INDEX thường khoá ghi trong lúc tạo; bảng hàng triệu dòng thì tách ra chạy
-- CREATE INDEX CONCURRENTLY bằng tay (không chạy được trong transaction của migration).
CREATE INDEX IF NOT EXISTS products_created_at_id_idx ON products (created_at, id);
CREATE INDEX IF NOT EXISTS products_price_id_idx ON products (price, id);
