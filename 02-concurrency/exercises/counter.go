package exercises

import "sync"

type SafeCounter struct {
    mu     sync.RWMutex
    counts map[string]int
}

func NewSafeCounter() *SafeCounter {
    return &SafeCounter{
        counts: make(map[string]int),
    }
}

func (c *SafeCounter) Inc(key string) {
    c.mu.Lock()
    defer c.mu.Unlock()

    c.counts[key]++
}

func (c *SafeCounter) Value(key string) int {
    c.mu.RLock()
    defer c.mu.RUnlock()

    return c.counts[key]
}
