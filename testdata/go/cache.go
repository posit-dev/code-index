// Package cache provides a simple in-memory LRU cache.
package cache

import (
	"sync"
	"time"
)

// Entry is a single cache entry with an expiration time.
type Entry struct {
	Value     interface{}
	ExpiresAt time.Time
}

// Cache is a thread-safe in-memory cache with TTL-based expiration.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	ttl     time.Duration
}

// New creates a new cache with the given default TTL.
func New(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]*Entry),
		ttl:     ttl,
	}
}

// Get retrieves a value from the cache. Returns the value and true
// if found and not expired, or nil and false otherwise.
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.ExpiresAt) {
		return nil, false
	}
	return entry.Value, true
}

// Set stores a value in the cache with the default TTL.
func (c *Cache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &Entry{
		Value:     value,
		ExpiresAt: time.Now().Add(c.ttl),
	}
}

// Delete removes a key from the cache.
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, key)
}

// Size returns the number of entries in the cache (including expired ones).
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.entries)
}

// Purge removes all expired entries from the cache.
func (c *Cache) Purge() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	removed := 0
	for key, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			delete(c.entries, key)
			removed++
		}
	}
	return removed
}
