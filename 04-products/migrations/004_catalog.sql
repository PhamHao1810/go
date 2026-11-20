-- Catalog: danh mục cây, hãng, biến thể, thuộc tính, tag, đánh giá, đơn hàng + outbox đồng bộ search.
-- Chỉ THÊM bảng/cột (không đổi/xoá cột cũ) → instance code cũ vẫn chạy được trong lúc rolling deploy.

-- ----- Danh mục cây nhiều cấp -----
-- path = chuỗi id từ gốc xuống, vd "1/5/23": lấy cả cây con bằng 1 điều kiện LIKE '1/5/%' (có index).
CREATE TABLE IF NOT EXISTS categories (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    parent_id  BIGINT REFERENCES categories (id), -- RESTRICT: không xoá được danh mục còn danh mục con
    name       VARCHAR(100) NOT NULL,
    slug       VARCHAR(120) NOT NULL UNIQUE,
    path       TEXT         NOT NULL DEFAULT '',
    depth      INT          NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS categories_path_idx ON categories (path text_pattern_ops);
CREATE INDEX IF NOT EXISTS categories_parent_idx ON categories (parent_id);

-- Tính path/depth lúc tạo (id đã có trong BEFORE INSERT). Không cho đổi cha: cây con sẽ sai path.
CREATE OR REPLACE FUNCTION categories_set_path() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent_path TEXT;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.parent_id IS DISTINCT FROM OLD.parent_id THEN
            RAISE EXCEPTION 'category parent cannot be changed' USING ERRCODE = '22023';
        END IF;
        NEW.path := OLD.path;
        NEW.depth := OLD.depth;
        RETURN NEW;
    END IF;
    IF NEW.parent_id IS NULL THEN
        NEW.path := NEW.id::text;
        NEW.depth := 0;
    ELSE
        SELECT path INTO parent_path FROM categories WHERE id = NEW.parent_id;
        IF parent_path IS NULL THEN
            RAISE EXCEPTION 'parent category % not found', NEW.parent_id USING ERRCODE = '23503';
        END IF;
        NEW.path := parent_path || '/' || NEW.id;
        NEW.depth := array_length(string_to_array(NEW.path, '/'), 1) - 1;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS categories_set_path ON categories;
CREATE TRIGGER categories_set_path BEFORE INSERT OR UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION categories_set_path();

CREATE TABLE IF NOT EXISTS brands (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       VARCHAR(100) NOT NULL UNIQUE,
    slug       VARCHAR(120) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- ----- Cột mới của products -----
-- price/stock giữ nguyên tên nhưng từ nay là số TỔNG HỢP từ biến thể (giá thấp nhất, tổng tồn kho), do trigger cập nhật.
ALTER TABLE products
    ADD COLUMN IF NOT EXISTS category_id  BIGINT REFERENCES categories (id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS brand_id     BIGINT REFERENCES brands (id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS rating_sum   BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS rating_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sold_count   BIGINT NOT NULL DEFAULT 0;
-- Duyệt 1 danh mục mới nhất trước (keyset): WHERE category_id = ANY(...) ORDER BY created_at DESC, id DESC
CREATE INDEX IF NOT EXISTS products_category_created_idx ON products (category_id, created_at, id);
CREATE INDEX IF NOT EXISTS products_brand_idx ON products (brand_id);

-- ----- Biến thể: tồn kho và giá thật nằm ở đây -----
CREATE TABLE IF NOT EXISTS product_variants (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    product_id BIGINT       NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    sku        VARCHAR(64)  NOT NULL UNIQUE,
    color      VARCHAR(40)  NOT NULL DEFAULT '',
    size       VARCHAR(20)  NOT NULL DEFAULT '',
    price      BIGINT       NOT NULL CHECK (price >= 0),
    stock      BIGINT       NOT NULL CHECK (stock >= 0), -- lớp chặn cuối: không bao giờ âm kho
    version    BIGINT       NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS product_variants_product_idx ON product_variants (product_id, id);

-- Mỗi sản phẩm cũ có 1 biến thể mặc định mang đúng sku/giá/kho của nó.
INSERT INTO product_variants (product_id, sku, price, stock)
SELECT p.id, p.sku, p.price, p.stock FROM products p
WHERE NOT EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = p.id);

-- products.price = giá thấp nhất, products.stock = tổng kho của các biến thể.
-- Khoá dòng products TRƯỚC rồi mới tính tổng: 2 transaction đổi 2 biến thể khác nhau của cùng 1 sản phẩm
-- không ghi đè tổng của nhau (câu SELECT chạy sau khi có khoá nên thấy dữ liệu mới nhất).
-- Thứ tự khoá trong toàn hệ thống: mọi biến thể (id tăng dần) → rồi sản phẩm (xem OrderRepo.Place) để không deadlock.
CREATE OR REPLACE FUNCTION products_sync_from_variants() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    pid BIGINT := COALESCE(NEW.product_id, OLD.product_id);
    min_price BIGINT;
    total BIGINT;
BEGIN
    PERFORM 1 FROM products WHERE id = pid FOR UPDATE;
    SELECT COALESCE(MIN(price), 0), COALESCE(SUM(stock), 0) INTO min_price, total
    FROM product_variants WHERE product_id = pid;
    UPDATE products SET price = min_price, stock = total, version = version + 1, updated_at = now()
    WHERE id = pid AND (price, stock) IS DISTINCT FROM (min_price, total);
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS product_variants_sync ON product_variants;
CREATE TRIGGER product_variants_sync AFTER INSERT OR DELETE OR UPDATE OF price, stock ON product_variants
    FOR EACH ROW EXECUTE FUNCTION products_sync_from_variants();

-- ----- Thuộc tính (Chất liệu: Cotton, Xuất xứ: Việt Nam...) và tag: quan hệ nhiều-nhiều -----
CREATE TABLE IF NOT EXISTS attributes (
    id   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name VARCHAR(60) NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS attribute_values (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    attribute_id BIGINT       NOT NULL REFERENCES attributes (id) ON DELETE CASCADE,
    value        VARCHAR(100) NOT NULL,
    UNIQUE (attribute_id, value)
);
CREATE TABLE IF NOT EXISTS product_attribute_values (
    product_id BIGINT NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    value_id   BIGINT NOT NULL REFERENCES attribute_values (id) ON DELETE CASCADE,
    PRIMARY KEY (product_id, value_id)
);
CREATE INDEX IF NOT EXISTS product_attribute_values_value_idx ON product_attribute_values (value_id);

CREATE TABLE IF NOT EXISTS tags (
    id   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name VARCHAR(50) NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS product_tags (
    product_id BIGINT NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    tag_id     BIGINT NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (product_id, tag_id)
);
CREATE INDEX IF NOT EXISTS product_tags_tag_idx ON product_tags (tag_id);

-- ----- Đơn hàng -----
CREATE TABLE IF NOT EXISTS orders (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (id),
    status     TEXT        NOT NULL DEFAULT 'pending'
               CHECK (status IN ('pending', 'paid', 'shipped', 'delivered', 'cancelled')),
    total      BIGINT      NOT NULL CHECK (total >= 0),
    version    BIGINT      NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS orders_user_idx ON orders (user_id, id);

-- Giá + tên chốt lúc mua: admin đổi giá/xoá sản phẩm sau đó không làm sai lịch sử đơn.
CREATE TABLE IF NOT EXISTS order_items (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id   BIGINT       NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    variant_id BIGINT       REFERENCES product_variants (id) ON DELETE SET NULL,
    product_id BIGINT       REFERENCES products (id) ON DELETE SET NULL,
    name       VARCHAR(260) NOT NULL,
    qty        BIGINT       NOT NULL CHECK (qty > 0),
    unit_price BIGINT       NOT NULL CHECK (unit_price >= 0)
);
CREATE INDEX IF NOT EXISTS order_items_order_idx ON order_items (order_id);
CREATE INDEX IF NOT EXISTS order_items_product_idx ON order_items (product_id);
CREATE INDEX IF NOT EXISTS order_items_variant_idx ON order_items (variant_id);

-- ----- Đánh giá: mỗi người 1 lần / sản phẩm, chỉ người đã nhận hàng -----
CREATE TABLE IF NOT EXISTS reviews (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    product_id BIGINT      NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    rating     SMALLINT    NOT NULL CHECK (rating BETWEEN 1 AND 5),
    content    TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (product_id, user_id)
);
CREATE INDEX IF NOT EXISTS reviews_product_idx ON reviews (product_id, id);

-- ----- Outbox: sản phẩm nào cần đẩy lại lên Meilisearch -----
-- Trigger ghi vào đây trong CÙNG transaction với thay đổi → không bao giờ "quên" cập nhật index.
-- Job search-sync đọc, ghép document, đẩy lên Meilisearch rồi xoá dòng (xem service/search_sync.go).
CREATE TABLE IF NOT EXISTS search_outbox (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    product_id BIGINT      NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION search_outbox_product() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO search_outbox (product_id) VALUES (COALESCE(NEW.product_id, OLD.product_id));
    RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION search_outbox_products_row() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO search_outbox (product_id) VALUES (COALESCE(NEW.id, OLD.id));
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS products_search_outbox ON products;
CREATE TRIGGER products_search_outbox AFTER INSERT OR DELETE OR UPDATE ON products
    FOR EACH ROW EXECUTE FUNCTION search_outbox_products_row();

-- Biến thể (màu/size), tag, thuộc tính, ảnh đều nằm trong document search.
-- Giá/kho của biến thể đổi thì trigger ở trên đã cập nhật products → products tự ghi outbox.
DROP TRIGGER IF EXISTS product_variants_search_outbox ON product_variants;
CREATE TRIGGER product_variants_search_outbox AFTER INSERT OR DELETE OR UPDATE OF color, size ON product_variants
    FOR EACH ROW EXECUTE FUNCTION search_outbox_product();
DROP TRIGGER IF EXISTS product_tags_search_outbox ON product_tags;
CREATE TRIGGER product_tags_search_outbox AFTER INSERT OR DELETE ON product_tags
    FOR EACH ROW EXECUTE FUNCTION search_outbox_product();
DROP TRIGGER IF EXISTS product_attribute_values_search_outbox ON product_attribute_values;
CREATE TRIGGER product_attribute_values_search_outbox AFTER INSERT OR DELETE ON product_attribute_values
    FOR EACH ROW EXECUTE FUNCTION search_outbox_product();
DROP TRIGGER IF EXISTS product_images_search_outbox ON product_images;
CREATE TRIGGER product_images_search_outbox AFTER INSERT OR DELETE ON product_images
    FOR EACH ROW EXECUTE FUNCTION search_outbox_product();

-- Đổi tên hãng/danh mục: mọi sản phẩm liên quan (cả cây con của danh mục) phải đẩy lại.
CREATE OR REPLACE FUNCTION search_outbox_brand() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO search_outbox (product_id) SELECT id FROM products WHERE brand_id = NEW.id;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS brands_search_outbox ON brands;
CREATE TRIGGER brands_search_outbox AFTER UPDATE OF name ON brands
    FOR EACH ROW EXECUTE FUNCTION search_outbox_brand();

CREATE OR REPLACE FUNCTION search_outbox_category() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO search_outbox (product_id)
    SELECT p.id FROM products p JOIN categories c ON c.id = p.category_id
    WHERE c.path = NEW.path OR c.path LIKE NEW.path || '/%';
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS categories_search_outbox ON categories;
CREATE TRIGGER categories_search_outbox AFTER UPDATE OF name ON categories
    FOR EACH ROW EXECUTE FUNCTION search_outbox_category();
