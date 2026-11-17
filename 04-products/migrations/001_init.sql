-- Postgres tự chạy file này khi container khởi tạo lần đầu.
CREATE TABLE IF NOT EXISTS products (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sku         VARCHAR(64)  NOT NULL UNIQUE,
    name        VARCHAR(200) NOT NULL,
    description TEXT         NOT NULL DEFAULT '',
    price       BIGINT       NOT NULL CHECK (price >= 0),  -- VND, số nguyên để không sai số như float
    stock       BIGINT       NOT NULL CHECK (stock >= 0),  -- lớp chặn cuối cùng: DB không bao giờ cho tồn kho âm
    version     BIGINT       NOT NULL DEFAULT 1,           -- optimistic locking
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- Full-text search: 'simple' không bỏ stopword/không stem, hợp với tiếng Việt (mỗi âm tiết là 1 từ).
    search      TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple', name || ' ' || sku)) STORED
);

CREATE INDEX IF NOT EXISTS products_search_idx ON products USING gin (search);
