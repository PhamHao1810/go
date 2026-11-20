-- Chỉ đẩy sản phẩm sang Meilisearch khi trường CÓ TRONG INDEX thật sự đổi.
-- Trước đây mọi UPDATE products đều ghi outbox: mỗi đơn hàng 2 lần (stock, sold_count), mỗi lần = 1 task
-- Meilisearch ~250ms CPU trên index 1 triệu → load test 1000 VU search timeout vì Meilisearch bận index.
--   stock      → chỉ khi còn hàng/hết hàng đổi (in_stock)
--   rating     → chỉ khi đổi bậc 0,5 sao (lọc "từ 4 sao" vẫn đúng; số lẻ trên kết quả search có thể trễ)
--   sold_count → không đồng bộ theo từng đơn: đánh dấu vào search_sold_dirty, job search-sold-sync đẩy theo lịch
--   description, version, updated_at → không có trong index, bỏ qua
DROP TRIGGER IF EXISTS products_search_outbox ON products;
CREATE TRIGGER products_search_outbox AFTER INSERT OR DELETE ON products
    FOR EACH ROW EXECUTE FUNCTION search_outbox_products_row();

DROP TRIGGER IF EXISTS products_search_outbox_update ON products;
CREATE TRIGGER products_search_outbox_update AFTER UPDATE ON products
    FOR EACH ROW
    WHEN ((OLD.name, OLD.sku, OLD.price, OLD.category_id, OLD.brand_id, OLD.stock > 0,
           round(OLD.rating_sum::numeric * 2 / NULLIF(OLD.rating_count, 0)))
          IS DISTINCT FROM
          (NEW.name, NEW.sku, NEW.price, NEW.category_id, NEW.brand_id, NEW.stock > 0,
           round(NEW.rating_sum::numeric * 2 / NULLIF(NEW.rating_count, 0))))
    EXECUTE FUNCTION search_outbox_products_row();

-- Sản phẩm có sold_count đổi từ lần đồng bộ trước. Khoá chính = 1 dòng/sản phẩm dù bán 1000 đơn.
-- Không FK: sản phẩm đã xoá thì sync thấy không còn document → xoá khỏi index (vô hại).
CREATE TABLE IF NOT EXISTS search_sold_dirty (
    product_id BIGINT PRIMARY KEY
);

CREATE OR REPLACE FUNCTION search_sold_dirty_mark() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO search_sold_dirty (product_id) VALUES (NEW.id) ON CONFLICT DO NOTHING;
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS products_sold_dirty ON products;
CREATE TRIGGER products_sold_dirty AFTER UPDATE OF sold_count ON products
    FOR EACH ROW WHEN (OLD.sold_count IS DISTINCT FROM NEW.sold_count)
    EXECUTE FUNCTION search_sold_dirty_mark();
