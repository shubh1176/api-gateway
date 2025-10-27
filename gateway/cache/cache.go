package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// Response represents a cached HTTP response
type Response struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
	TTL        int64 // Unix timestamp
}

// Cache is a thread-safe in-memory cache with LRU eviction
type Cache struct {
	data     map[string]*entry
	capacity int
	maxSize  int64 // bytes
	totalSize int64
	mu       sync.RWMutex
}

type entry struct {
	response *Response
	lastUsed int64 // Unix nanosecond timestamp
	size     int64
}

// NewCache creates a new cache with the specified capacity and max size in bytes
func NewCache(capacity int, maxSizeMB int) *Cache {
	return &Cache{
		data:     make(map[string]*entry, capacity),
		capacity: capacity,
		maxSize:  int64(maxSizeMB) * 1024 * 1024,
	}
}

// Get retrieves a cached response
func (c *Cache) Get(key string) (*Response, bool) {
	c.mu.RLock()
	entry, exists := c.data[key]
	c.mu.RUnlock()

	if !exists {
		return nil, false
	}

	// Check TTL
	if entry.response.TTL < time.Now().Unix() {
		c.mu.Lock()
		delete(c.data, key)
		c.totalSize -= entry.size
		c.mu.Unlock()
		return nil, false
	}

	// Update last used time
	atomic.StoreInt64(&entry.lastUsed, time.Now().UnixNano())

	return entry.response, true
}

// Set stores a response in the cache
func (c *Cache) Set(key string, response *Response, ttlSeconds int) {
	if ttlSeconds <= 0 {
		return
	}

	now := time.Now()
	response.TTL = now.Unix() + int64(ttlSeconds)
	entry := &entry{
		response: response,
		lastUsed: now.UnixNano(),
	}
	
	// Calculate size (approximate)
	entry.size = int64(len(key) + len(response.Body))
	for k, v := range response.Headers {
		entry.size += int64(len(k))
		for _, val := range v {
			entry.size += int64(len(val))
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if key exists and update
	if oldEntry, exists := c.data[key]; exists {
		c.totalSize -= oldEntry.size
	}

	// Evict if necessary
	if c.totalSize + entry.size > c.maxSize {
		c.evict()
	}

	// Evict if capacity exceeded
	if len(c.data) >= c.capacity && entry.size+c.totalSize > c.maxSize {
		c.evict()
	}

	c.data[key] = entry
	c.totalSize += entry.size
}

func (c *Cache) evict() {
	// Simple eviction: remove oldest entry
	var oldestKey string
	var oldestTime int64 = time.Now().UnixNano()

	for k, v := range c.data {
		if v.lastUsed < oldestTime {
			oldestTime = v.lastUsed
			oldestKey = k
		}
	}

	if oldestKey != "" {
		if oldEntry, exists := c.data[oldestKey]; exists {
			c.totalSize -= oldEntry.size
			delete(c.data, oldestKey)
		}
	}
}

// Clear removes all entries
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[string]*entry)
	c.totalSize = 0
}

// Stats returns cache statistics
func (c *Cache) Stats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return map[string]interface{}{
		"entries":   len(c.data),
		"size_mb":   float64(c.totalSize) / 1024 / 1024,
		"capacity":  c.capacity,
		"max_size":  c.maxSize,
	}
}

// Hash generates a cache key hash from request components
func Hash(method, path, query string, body []byte) string {
	h := sha256.New()
	h.Write(unsafe.Slice(unsafe.StringData(method), len(method)))
	h.Write(unsafe.Slice(unsafe.StringData(path), len(path)))
	h.Write(unsafe.Slice(unsafe.StringData(query), len(query)))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
