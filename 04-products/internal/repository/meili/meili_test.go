package meili

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"golearn/04-products/internal/domain"
)

// Máy search chết (IP không ai trả lời): lỗi phải là "dial" và về TRƯỚC timeout search 800ms,
// để SearchService nhận ra máy chết (chuyển Postgres) chứ không nhầm với "bận".
func TestSearch_DeadHostIsDialError(t *testing.T) {
	c := New("http://10.255.255.1:7700", "k") // dải private không ai dùng: gói SYN rơi mất như máy chết
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Search(ctx, domain.SearchQuery{Q: "áo", Limit: 1, Page: 1})
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "dial" {
		t.Fatalf("err = %v, muốn lỗi dial", err)
	}
	if d := time.Since(start); d > 600*time.Millisecond {
		t.Errorf("mất %v, dial timeout 300ms không có tác dụng", d)
	}
}
