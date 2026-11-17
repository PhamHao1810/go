package exercises

import (
	"errors"
	"time"
)

var ErrTimeout = errors.New("timeout")

// Bài 3: fn là hàm chặn, KHÔNG hỗ trợ context (ví dụ thư viện cũ).
// Nếu fn xong trước timeout thì trả kết quả của nó.
// Nếu quá timeout thì trả ("", ErrTimeout) NGAY, không chờ fn.
// Gợi ý: chạy fn trong goroutine + select với time.After.
// Câu hỏi: channel kết quả nên có buffer hay không? Vì sao? (từ khóa: goroutine leak)
func CallWithTimeout(timeout time.Duration, fn func() string) (string, error) {
	// TODO
	return "", nil
}
