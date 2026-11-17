package exercises

import (
	"errors"
	"testing"
)

func TestWithdraw(t *testing.T) {
	t.Run("thành công", func(t *testing.T) {
		a := &Account{Balance: 100}
		if err := a.Withdraw(30); err != nil {
			t.Fatalf("Withdraw(30) lỗi không mong muốn: %v", err)
		}
		if a.Balance != 70 {
			t.Errorf("Balance = %d, muốn 70", a.Balance)
		}
	})

	t.Run("số tiền không hợp lệ", func(t *testing.T) {
		a := &Account{Balance: 100}
		err := a.Withdraw(0)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Field != "amount" {
			t.Fatalf("muốn *ValidationError với Field=amount, nhận %v", err)
		}
	})

	t.Run("không đủ tiền", func(t *testing.T) {
		a := &Account{Balance: 10}
		err := a.Withdraw(50)
		if !errors.Is(err, ErrInsufficientFunds) {
			t.Fatalf("muốn lỗi bọc ErrInsufficientFunds, nhận %v", err)
		}
		if a.Balance != 10 {
			t.Errorf("Balance bị đổi thành %d dù rút lỗi", a.Balance)
		}
	})
}
