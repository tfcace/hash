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

// getStale returns a cached value that may already have expired, as long as it
// expired no longer than grace ago. Callers use it to answer instantly from an
// old result while a refresh runs in the background.
func (c *stringListCache) getStale(key string, now time.Time, grace time.Duration) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expiresAt.Add(grace)) {
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
