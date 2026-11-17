// Giai đoạn 1 — Go cơ bản cho người đến từ Node.js.
// Chạy: go run ./01-basics/demo
// Đọc từ trên xuống, sửa thử, chạy lại. Mỗi khối có so sánh với JS/TS.
package main

import (
	"errors"
	"fmt"
	"strings"
)

// ===== 1. Struct thay cho class =====
// Go không có class/kế thừa. Dữ liệu là struct, hành vi là method gắn vào struct.
// Chữ cái đầu VIẾT HOA = public (export), viết thường = private trong package.
type User struct {
	ID    int
	Name  string
	Email string
}

// Pointer receiver (*User): method sửa được struct gốc. Giống `this.name = ...` trong JS.
func (u *User) Rename(name string) { u.Name = name }

// Value receiver (User): method nhận bản COPY, chỉ nên dùng để đọc.
func (u User) Greeting() string { return "Xin chào " + u.Name }

// ===== 2. Interface ngầm định =====
// Không có `implements`. Kiểu nào có đủ method thì tự động thỏa interface.
type Notifier interface {
	Notify(msg string) error
}

type EmailNotifier struct{ From string }

func (e EmailNotifier) Notify(msg string) error {
	fmt.Printf("  [email từ %s] %s\n", e.From, msg)
	return nil
}

type SMSNotifier struct{}

func (SMSNotifier) Notify(msg string) error {
	fmt.Println("  [sms]", msg)
	return nil
}

// ===== 3. Error là giá trị, không có try/catch =====
// Sentinel error: giá trị lỗi cố định để so sánh bằng errors.Is (giống `instanceof NotFoundException`).
var ErrUserNotFound = errors.New("user not found")

func findUser(users map[int]User, id int) (User, error) {
	u, ok := users[id]
	if !ok {
		// %w "bọc" lỗi gốc: thêm ngữ cảnh mà vẫn giữ được ErrUserNotFound bên trong.
		return User{}, fmt.Errorf("findUser id=%d: %w", id, ErrUserNotFound)
	}
	return u, nil
}

// ===== 4. Generics =====
// Tương đương: function map<T, R>(arr: T[], f: (x: T) => R): R[]
func Map[T, R any](in []T, f func(T) R) []R {
	out := make([]R, 0, len(in)) // cấp phát trước đúng dung lượng => không phải grow nhiều lần
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

func main() {
	fmt.Println("== Slice (giống Array trong JS nhưng có len/cap) ==")
	nums := []int{1, 2, 3}
	nums = append(nums, 4) // append trả về slice mới -> PHẢI gán lại
	fmt.Println(" ", nums, "len:", len(nums), "cap:", cap(nums))
	part := nums[1:3] // KHÔNG copy, dùng chung mảng bên dưới!
	part[0] = 99
	fmt.Println("  sau khi sửa part[0]=99, nums =", nums, "<- nums cũng bị đổi")

	fmt.Println("== Map (giống Object/Map trong JS) ==")
	ages := map[string]int{"an": 20}
	ages["binh"] = 25
	if v, ok := ages["chi"]; !ok {
		fmt.Println("  'chi' không tồn tại, trả zero value =", v, "(không có undefined)")
	}
	// var m map[string]int; m["x"] = 1  // PANIC: ghi vào map nil. Luôn dùng make() hoặc literal.

	fmt.Println("== Struct + method ==")
	u := User{ID: 1, Name: "Hao"}
	u.Rename("Hào")
	fmt.Println(" ", u.Greeting())

	fmt.Println("== Interface ==")
	notifiers := []Notifier{EmailNotifier{From: "noreply@shop.vn"}, SMSNotifier{}}
	for _, n := range notifiers {
		if err := n.Notify("đơn hàng đã giao"); err != nil {
			fmt.Println("  lỗi:", err)
		}
	}

	fmt.Println("== Error ==")
	users := map[int]User{1: u}
	if _, err := findUser(users, 42); err != nil {
		fmt.Println("  lỗi:", err)
		fmt.Println("  errors.Is(err, ErrUserNotFound) =", errors.Is(err, ErrUserNotFound))
	}

	fmt.Println("== Generics ==")
	fmt.Println(" ", Map([]string{"go", "node"}, strings.ToUpper))

	fmt.Println("== Defer ==")
	deferDemo()
}

// defer chạy khi hàm return, theo thứ tự NGƯỢC (LIFO). Dùng để đóng file, unlock mutex, close connection...
// Giống `finally` nhưng đặt ngay cạnh chỗ mở tài nguyên.
func deferDemo() {
	for i := range 3 { // range trên số nguyên (Go 1.22+): i = 0,1,2
		defer fmt.Println("  defer", i)
	}
	fmt.Println("  thân hàm chạy xong, giờ tới các defer:")
}
