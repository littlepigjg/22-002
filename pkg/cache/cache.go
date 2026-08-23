// Package cache 提供简单的 TTL 缓存：LRU-ish（基于 map + 定时清理 goroutine）。
package cache

import (
	"sync"
	"time"
)

// entry 缓存条目。
type entry[V any] struct {
	value     V
	expiresAt time.Time
}

// Cache TTL 缓存（支持自定义容量与默认 TTL）。
type Cache[K comparable, V any] struct {
	mu       sync.RWMutex
	data     map[K]entry[V]
	cap      int
	defaultT time.Duration
	once     sync.Once
	stopCh   chan struct{}
}

// Option 缓存选项。
type Option func(o *options)

type options struct {
	capacity  int
	defaultT  time.Duration
	autoPurge bool
}

// WithCapacity 设置容量。
func WithCapacity(n int) Option {
	return func(o *options) { o.capacity = n }
}

// WithDefaultTTL 设置默认 TTL。
func WithDefaultTTL(d time.Duration) Option {
	return func(o *options) { o.defaultT = d }
}

// WithAutoPurge 开启定时清理。
func WithAutoPurge(flag bool) Option {
	return func(o *options) { o.autoPurge = flag }
}

// New 创建缓存。
func New[K comparable, V any](opts ...Option) *Cache[K, V] {
	o := &options{capacity: 1024, defaultT: 10 * time.Minute, autoPurge: true}
	for _, fn := range opts {
		fn(o)
	}
	c := &Cache[K, V]{
		data:     make(map[K]entry[V]),
		cap:      o.capacity,
		defaultT: o.defaultT,
		stopCh:   make(chan struct{}),
	}
	if o.autoPurge {
		c.startPurge()
	}
	return c
}

func (c *Cache[K, V]) startPurge() {
	c.once.Do(func() {
		go func() {
			ticker := time.NewTicker(1 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-c.stopCh:
					return
				case <-ticker.C:
					c.Purge()
				}
			}
		}()
	})
}

// Close 停止缓存（停止后台清理）。
func (c *Cache[K, V]) Close() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

// Set 设置键值（使用默认 TTL）。
func (c *Cache[K, V]) Set(key K, value V) {
	c.SetTTL(key, value, c.defaultT)
}

// SetTTL 设置键值，附带自定义 TTL。
func (c *Cache[K, V]) SetTTL(key K, value V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ttl <= 0 {
		ttl = c.defaultT
	}
	// 超出容量时先尝试淘汰过期条目，不够再随机删。
	if len(c.data) >= c.cap {
		c.evictLocked(8)
		if len(c.data) >= c.cap {
			for k := range c.data {
				delete(c.data, k)
				break
			}
		}
	}
	c.data[key] = entry[V]{value: value, expiresAt: time.Now().Add(ttl)}
}

// Get 获取键值。返回 value, found。
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	e, ok := c.data[key]
	c.mu.RUnlock()
	if !ok {
		var zero V
		return zero, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		// 惰性删除。
		c.mu.Lock()
		delete(c.data, key)
		c.mu.Unlock()
		var zero V
		return zero, false
	}
	return e.value, true
}

// Delete 删除键。
func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
}

// Len 返回当前条目数（不含过期清理后的实际数）。
func (c *Cache[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data)
}

// Purge 清理过期条目。
func (c *Cache[K, V]) Purge() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evictLocked(-1)
}

// evictLocked 清理过期条目。maxClean 为 -1 清理全部，否则最多清理 maxClean。返回删除数。
func (c *Cache[K, V]) evictLocked(maxClean int) int {
	now := time.Now()
	count := 0
	for k, e := range c.data {
		if maxClean >= 0 && count >= maxClean {
			break
		}
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			delete(c.data, k)
			count++
		}
	}
	return count
}
