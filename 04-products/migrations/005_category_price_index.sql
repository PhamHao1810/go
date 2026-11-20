-- Duyệt danh mục sắp theo giá: mỗi danh mục con lấy N dòng rẻ nhất qua index này rồi trộn (xem buildListQuery).
-- (category_id, created_at, id) cho sắp mới nhất đã có từ 004.
CREATE INDEX IF NOT EXISTS products_category_price_idx ON products (category_id, price, id);
