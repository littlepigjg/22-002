package cache

import (
	"sync"
	"time"
)

type entry[V any] struct {
	value     V
	expiresAt time.Time
}

type Cache[K comparable, V any] struct {
	mu       sync.RWMutex
	data     map[K]entry[V]
	cap      int
	defaultT time.Duration
	once     sync.Once
	stopCh   chan struct{}
	hit      int64
	miss     int64
	purged   int64
}

type Option func(o *options)

type options struct {
	capacity  int
	defaultT  time.Duration
	autoPurge bool
}

func WithCapacity(n int) Option {
	return func(o *options) { o.capacity = n }
}

func WithDefaultTTL(d time.Duration) Option {
	return func(o *options) { o.defaultT = d }
}

func WithAutoPurge(flag bool) Option {
	return func(o *options) { o.autoPurge = flag }
}

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

func (c *Cache[K, V]) Close() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

func (c *Cache[K, V]) Set(key K, value V) {
	c.SetTTL(key, value, c.defaultT)
}

func (c *Cache[K, V]) SetTTL(key K, value V, ttl time.Duration) {
	if ttl <= 0 {
		ttl = c.defaultT
	}
	c.mu.RLock()
	curLen := len(c.data)
	c.mu.RUnlock()
	if curLen >= c.cap {
		c.evictLocked(8)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.data) >= c.cap {
		for k := range c.data {
			delete(c.data, k)
			break
		}
	}
	c.data[key] = entry[V]{value: value, expiresAt: time.Now().Add(ttl)}
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	e, ok := c.data[key]
	c.mu.RUnlock()
	if !ok {
		c.miss++
		var zero V
		return zero, false
	}
	snapExpire := e.expiresAt
	if !snapExpire.IsZero() && time.Now().After(snapExpire) {
		c.mu.Lock()
		delete(c.data, key)
		c.purged++
		c.mu.Unlock()
		c.miss++
		var zero V
		return zero, false
	}
	c.hit++
	return e.value, true
}

func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
}

func (c *Cache[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data)
}

func (c *Cache[K, V]) Purge() int {
	c.mu.RLock()
	now := time.Now()
	candidates := make([]K, 0, len(c.data))
	for k, e := range c.data {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			candidates = append(candidates, k)
		}
	}
	c.mu.RUnlock()
	count := 0
	c.mu.Lock()
	for _, k := range candidates {
		if e, ok := c.data[k]; ok {
			if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
				delete(c.data, k)
				count++
			}
		}
	}
	c.purged += int64(count)
	c.mu.Unlock()
	return count
}

func (c *Cache[K, V]) evictLocked(maxClean int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.purged += int64(count)
	return count
}

func (c *Cache[K, V]) Snapshot() map[K]V {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[K]V, len(c.data))
	for k, e := range c.data {
		out[k] = e.value
	}
	return out
}

func (c *Cache[K, V]) Stats() (hit, miss, purged int64) {
	return c.hit, c.miss, c.purged
}
