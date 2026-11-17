package exercises

import (
	"sync"
	"testing"
)

func TestSafeCounter(t *testing.T) {
	c := NewSafeCounter()
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			for range 100 {
				c.Inc("a")
				_ = c.Value("a")
			}
		})
	}
	wg.Wait()
	if got := c.Value("a"); got != 10000 {
		t.Errorf(`Value("a") = %d, muốn 10000`, got)
	}
	if got := c.Value("không-có"); got != 0 {
		t.Errorf("Value của key chưa có = %d, muốn 0", got)
	}
}
