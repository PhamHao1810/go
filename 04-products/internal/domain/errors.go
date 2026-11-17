// Package domain chứa entity, quy tắc nghiệp vụ và lỗi nghiệp vụ.
// Không import package nào của project: mọi tầng khác phụ thuộc vào domain, domain không phụ thuộc ai.
package domain

// Kind phân loại lỗi; tầng HTTP map Kind → status code (xem httpapi/response).
type Kind uint8

const (
	KindInternal Kind = iota
	KindInvalid
	KindNotFound
	KindConflict
	KindUnauthenticated // chưa đăng nhập / token sai → 401
	KindForbidden       // đã đăng nhập nhưng không đủ quyền → 403
	KindTooManyRequests // → 429
)

// Error là lỗi nghiệp vụ có mã ổn định (Code) để client xử lý bằng code, không phải bằng câu chữ.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Fields  []FieldError
}

type FieldError struct {
	Field   string
	Message string
}

func (e *Error) Error() string { return e.Message }

func Invalid(code, message string) *Error {
	return &Error{Kind: KindInvalid, Code: code, Message: message}
}

var (
	ErrProductNotFound = &Error{Kind: KindNotFound, Code: "PRODUCT_NOT_FOUND", Message: "product not found"}
	ErrSKUTaken        = &Error{Kind: KindConflict, Code: "SKU_TAKEN", Message: "sku already exists"}
	ErrVersionConflict = &Error{Kind: KindConflict, Code: "VERSION_CONFLICT",
		Message: "product was changed by someone else, reload and retry"}
	ErrOutOfStock    = &Error{Kind: KindConflict, Code: "OUT_OF_STOCK", Message: "not enough stock"}
	ErrInvalidCursor = Invalid("INVALID_CURSOR", "cursor is invalid or does not match the current sort")

	// Xác thực & phân quyền
	ErrUnauthenticated = &Error{Kind: KindUnauthenticated, Code: "UNAUTHENTICATED", Message: "missing or invalid access token"}
	ErrForbidden       = &Error{Kind: KindForbidden, Code: "FORBIDDEN", Message: "you do not have permission to do this"}
	// Cùng 1 thông báo cho sai email và sai mật khẩu: không cho kẻ tấn công dò email nào đã đăng ký.
	ErrInvalidCredentials  = &Error{Kind: KindUnauthenticated, Code: "INVALID_CREDENTIALS", Message: "invalid email or password"}
	ErrInvalidRefreshToken = &Error{Kind: KindUnauthenticated, Code: "INVALID_REFRESH_TOKEN", Message: "refresh token is invalid, expired or already used"}
	ErrTooManyAttempts     = &Error{Kind: KindTooManyRequests, Code: "TOO_MANY_ATTEMPTS", Message: "too many failed logins, try again later"}
	ErrEmailTaken          = &Error{Kind: KindConflict, Code: "EMAIL_TAKEN", Message: "email already registered"}
	ErrUserNotFound        = &Error{Kind: KindNotFound, Code: "USER_NOT_FOUND", Message: "user not found"}

	// File / ảnh
	ErrAssetNotFound    = &Error{Kind: KindNotFound, Code: "ASSET_NOT_FOUND", Message: "asset not found"}
	ErrAssetNotUploaded = &Error{Kind: KindConflict, Code: "ASSET_NOT_UPLOADED", Message: "file has not been uploaded to upload_url yet"}
	ErrAssetMismatch    = Invalid("ASSET_MISMATCH", "uploaded file does not match the declared content_type/size")
	ErrImageNotFound    = &Error{Kind: KindNotFound, Code: "IMAGE_NOT_FOUND", Message: "image is not attached to this product"}
	ErrTooManyImages    = &Error{Kind: KindConflict, Code: "TOO_MANY_IMAGES", Message: "a product can have at most 10 images"}
)

// Validator gom TẤT CẢ lỗi field rồi trả 1 lần, để client sửa hết trong 1 lần gửi.
type Validator struct {
	fields []FieldError
}

func (v *Validator) Check(ok bool, field, message string) {
	if !ok {
		v.fields = append(v.fields, FieldError{Field: field, Message: message})
	}
}

func (v *Validator) Err() error {
	if len(v.fields) == 0 {
		return nil
	}
	return &Error{Kind: KindInvalid, Code: "VALIDATION_FAILED", Message: "request has invalid fields", Fields: v.fields}
}
