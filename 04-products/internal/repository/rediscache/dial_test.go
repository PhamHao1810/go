package rediscache

import (
	"context"
	"net"
	"testing"
	"time"
)

// Máy Redis đầu chết → kết nối sang địa chỉ sau, không chờ lâu.
func TestDialFirst_FallsBackToNextAddr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := dead.Addr().String()
	dead.Close() // cổng không ai nghe

	start := time.Now()
	conn, err := dialFirst([]string{deadAddr, ln.Addr().String()}, 300*time.Millisecond)(context.Background(), "tcp", "")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if conn.RemoteAddr().String() != ln.Addr().String() {
		t.Fatalf("nối tới %s, muốn %s", conn.RemoteAddr(), ln.Addr())
	}
	if time.Since(start) > time.Second {
		t.Fatalf("mất %s", time.Since(start))
	}
	if _, err := dialFirst([]string{deadAddr}, 300*time.Millisecond)(context.Background(), "tcp", ""); err == nil {
		t.Fatal("mọi địa chỉ chết phải trả lỗi")
	}
}
