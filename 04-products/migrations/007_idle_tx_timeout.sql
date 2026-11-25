-- Máy app chết/treo giữa 1 transaction (vd search-sync đang giữ khoá advisory outbox): Postgres không biết client
-- đã chết (TCP keepalive mặc định ~2 giờ) → transaction + khoá treo → cả cụm ngừng đồng bộ search tới 2 giờ.
-- Transaction đứng yên quá 2 phút thì Postgres tự cắt, nhả khoá. Code không bao giờ giữ transaction lâu vậy:
-- job nền bị huỷ sau 1 phút (worker.RunTimeout), request bị huỷ sau vài giây.
-- Áp cho phiên MỚI của user này (kết nối cũ trong pool giữ giá trị cũ tới khi kết nối lại — deploy khởi động lại API).
ALTER ROLE CURRENT_USER SET idle_in_transaction_session_timeout = '2min';
