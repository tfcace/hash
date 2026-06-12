package completion

import (
	"sync"
	"time"
)

type stringListCache struct {
	mu      sync.Mutex
	entries map[string]stringListCacheEntry
}

type stringListCacheEntry struct {
	values    []string
	expiresAt time.Time
}

func (c *stringListCache) get(key string, now time.Time) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expiresAt) {
		return nil, false
	}
	return append([]string(nil), entry.values...), true
}

func (c *stringListCache) set(key string, values []string, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[string]stringListCacheEntry)
	}
	c.entries[key] = stringListCacheEntry{
		values:    append([]string(nil), values...),
		expiresAt: expiresAt,
	}
}
