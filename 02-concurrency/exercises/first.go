
package exercises

import (
	"context"
	"errors"
)

type result struct {
	value string
	err   error
}

func First(
	ctx context.Context,
	fns ...func(ctx context.Context) (string, error),
) (string, error) {
	// Không có hàm nào để gọi
	if len(fns) == 0 {
		return "", errors.New("First: không có hàm nào được cung cấp")
	}

	// Tạo context con để có thể hủy tất cả lời gọi
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channel nhận kết quả từ các goroutine
	results := make(chan result, len(fns))

	// Chạy tất cả hàm song song
	for _, fn := range fns {
		go func() {
			value, err := fn(ctx)

			results <- result{
				value: value,
				err:   err,
			}
		}()
	}

	// Thu thập kết quả
	var errs []error

	for range fns {
		select {
		case res := <-results:
			if res.err == nil {
				// Đã có kết quả thành công đầu tiên
				// Hủy các goroutine còn lại
				cancel()
				return res.value, nil
			}

			// Lưu lỗi để tổng hợp nếu tất cả đều thất bại
			errs = append(errs, res.err)

		case <-ctx.Done():
			// Context cha bị hủy hoặc hết thời gian
			return "", ctx.Err()
		}
	}

	// Tất cả hàm đều thất bại
	return "", errors.Join(errs...)
}
