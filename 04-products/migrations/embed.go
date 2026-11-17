// Package migrations nhúng các file .sql vào binary: app tự chạy migration lúc khởi động,
// deploy chỉ cần copy 1 file binary/image.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
