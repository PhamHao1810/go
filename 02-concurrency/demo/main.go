// Giai đoạn 2 — Concurrency: thứ khiến Go khác Node.js nhất.
// Chạy: go run ./02-concurrency/demo
//
// Node: 1 thread + event loop, I/O bất đồng bộ qua Promise.
// Go:   hàng nghìn goroutine chạy song song thật trên mọi CPU core; code viết TUẦN TỰ,
//       khi goroutine chờ I/O thì runtime tự chuyển sang goroutine khác. Không cần async/await.
package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

func main() {
	waitGroupDemo()
	channelDemo()
	selectTimeoutDemo()
	contextDemo()
	mutexDemo()
}

// 1. goroutine + WaitGroup ~ Promise.all
func waitGroupDemo() {
	fmt.Println("== 1. Goroutine + WaitGroup (giống Promise.all) ==")
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Go(func() { // Go 1.25+: wg.Go tự Add(1) và Done()
			time.Sleep(time.Duration(rand.IntN(100)) * time.Millisecond)
			fmt.Println("  worker", i, "xong") // thứ tự in ra ngẫu nhiên: chúng chạy song song
		})
	}
	wg.Wait()
}

// 2. Channel: "Don't communicate by sharing memory; share memory by communicating."
func channelDemo() {
	fmt.Println("== 2. Channel: producer -> consumer ==")
	jobs := make(chan int)
	go func() {
		defer close(jobs) // quy tắc: bên GỬI đóng channel
		for i := 1; i <= 3; i++ {
			jobs <- i // chặn cho tới khi có người nhận (unbuffered)
		}
	}()
	for j := range jobs { // lặp tới khi channel bị đóng
		fmt.Println("  nhận job", j)
	}
}

// 3. select ~ Promise.race
func selectTimeoutDemo() {
	fmt.Println("== 3. select + timeout (giống Promise.race) ==")
	// Buffer = 1: nếu ta timeout và bỏ đi, goroutine vẫn gửi được rồi thoát -> không bị leak.
	result := make(chan string, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		result <- "dữ liệu từ API chậm"
	}()
	select {
	case r := <-result:
		fmt.Println(" ", r)
	case <-time.After(100 * time.Millisecond):
		fmt.Println("  timeout sau 100ms!")
	}
}

// 4. context: truyền deadline/huỷ xuyên suốt request (handler -> service -> DB).
// Giống AbortController nhưng là chuẩn chung của cả hệ sinh thái Go.
func contextDemo() {
	fmt.Println("== 4. context: huỷ theo dây chuyền ==")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel() // luôn gọi cancel để giải phóng tài nguyên
	fmt.Println("  slowQuery:", slowQuery(ctx))
}

func slowQuery(ctx context.Context) error {
	select {
	case <-time.After(time.Second): // giả lập query mất 1s
		return nil
	case <-ctx.Done(): // bị huỷ hoặc hết hạn
		return ctx.Err()
	}
}

// 5. Mutex: nhiều goroutine cùng sửa 1 biến => phải khóa. (Node 1 thread nên không gặp vấn đề này.)
func mutexDemo() {
	fmt.Println("== 5. Mutex: tránh race condition ==")
	var (
		mu    sync.Mutex
		count int
		wg    sync.WaitGroup
	)
	for range 1000 {
		wg.Go(func() {
			mu.Lock()
			count++
			mu.Unlock()
		})
	}
	wg.Wait()
	fmt.Println("  count =", count, "(thử xoá Lock/Unlock rồi chạy lại vài lần xem kết quả)")
}
