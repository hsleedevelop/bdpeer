package discovery

import (
	"sync"
	"time"
)

type connectBackoffTable struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func newConnectBackoffTable() *connectBackoffTable {
	return &connectBackoffTable{until: make(map[string]time.Time)}
}

func (b *connectBackoffTable) Active(addr string, now time.Time) (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	until, ok := b.until[addr]
	if !ok {
		return time.Time{}, false
	}
	if !until.After(now) {
		delete(b.until, addr)
		return time.Time{}, false
	}
	return until, true
}

func (b *connectBackoffTable) Mark(addr string, now time.Time, duration time.Duration) time.Time {
	until := now.Add(duration)
	b.mu.Lock()
	b.until[addr] = until
	b.mu.Unlock()
	return until
}

func (b *connectBackoffTable) Clear(addr string) {
	b.mu.Lock()
	delete(b.until, addr)
	b.mu.Unlock()
}
