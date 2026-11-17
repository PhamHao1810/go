-- Người dùng + phân quyền.
CREATE TABLE IF NOT EXISTS users (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email           TEXT         NOT NULL UNIQUE,  -- luôn lưu chữ thường (domain chuẩn hoá trước khi lưu)
    password_hash   TEXT         NOT NULL,         -- bcrypt, không bao giờ lưu mật khẩu gốc
    name            VARCHAR(100) NOT NULL,
    role            VARCHAR(20)  NOT NULL DEFAULT 'customer' CHECK (role IN ('customer', 'admin')),
    avatar_asset_id BIGINT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- File đã upload (ảnh sản phẩm, avatar, sau này: hoá đơn...). Bảng này CHỈ chứa thông tin file;
-- ai dùng file nào được nối bằng foreign key thật (users.avatar_asset_id, product_images) chứ không dùng
-- cột kiểu owner_type/owner_id: kiểu đó không đặt được foreign key nên DB không chặn được dữ liệu mồ côi.
CREATE TABLE IF NOT EXISTS assets (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key          TEXT        NOT NULL UNIQUE,  -- đường dẫn trong bucket; URL = STORAGE_PUBLIC_URL + key
    content_type VARCHAR(50) NOT NULL,
    size         BIGINT      NOT NULL CHECK (size > 0),
    status       VARCHAR(10) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready')),
    uploaded_by  BIGINT      NOT NULL REFERENCES users (id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS assets_gc_idx ON assets (status, updated_at);

-- Không có ON DELETE: DB từ chối xoá asset đang được dùng → job dọn rác không thể xoá nhầm ảnh đang hiển thị.
ALTER TABLE users ADD CONSTRAINT users_avatar_fk FOREIGN KEY (avatar_asset_id) REFERENCES assets (id);
CREATE INDEX IF NOT EXISTS users_avatar_idx ON users (avatar_asset_id) WHERE avatar_asset_id IS NOT NULL;

-- Nhiều ảnh cho 1 sản phẩm (bảng nối N-N). Xoá sản phẩm → xoá dòng nối; file thành rác, job dọn rác sẽ xoá.
CREATE TABLE IF NOT EXISTS product_images (
    product_id BIGINT NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    asset_id   BIGINT NOT NULL REFERENCES assets (id),
    position   INT    NOT NULL,
    PRIMARY KEY (product_id, asset_id)
);
CREATE INDEX IF NOT EXISTS product_images_asset_idx ON product_images (asset_id);

-- Refresh token: chỉ lưu hash SHA-256 (lộ DB cũng không dùng được token), dùng 1 lần rồi đổi token mới.
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash BYTEA       NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS refresh_tokens_user_idx ON refresh_tokens (user_id);
