package exercises

import "errors"

// Bài 5: Xử lý lỗi kiểu Go.

// Sentinel error: kiểm tra bằng errors.Is(err, ErrInsufficientFunds).
var ErrInsufficientFunds = errors.New("insufficient funds")

// Custom error type: kiểm tra bằng errors.As(err, &ve), lấy được thêm thông tin (Field).
type ValidationError struct {
	Field string
	Msg   string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Msg }

type Account struct {
	Balance int64
}

// Withdraw rút tiền khỏi tài khoản.
//   - amount <= 0      -> trả &ValidationError{Field: "amount", Msg: "must be positive"}
//   - không đủ số dư   -> trả lỗi bọc ErrInsufficientFunds, ví dụ:
//     fmt.Errorf("withdraw %d: %w", amount, ErrInsufficientFunds)
//     Khi lỗi thì KHÔNG được đổi Balance.
//   - thành công       -> trừ Balance, trả nil
//
// Câu hỏi: vì sao receiver phải là *Account chứ không phải Account?
func (a *Account) Withdraw(amount int64) error {
	// TODO
	return nil
}
