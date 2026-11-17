-- Postgres tự chạy file này khi container khởi tạo lần đầu (thư mục /docker-entrypoint-initdb.d).
-- Bài tập giai đoạn 3: chuyển sang công cụ migration thật (goose hoặc golang-migrate).
CREATE TABLE IF NOT EXISTS links (
    code       VARCHAR(16) PRIMARY KEY,
    url        TEXT        NOT NULL,
    clicks     BIGINT      NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
