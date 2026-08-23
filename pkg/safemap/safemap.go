// Package safemap 提供并发安全的泛型 map 包装（基于 RWMutex，避免 data race）。
package safemap

import "sync"

// Map 并发安全的泛型 map。
type Map[K comparable, V any] struct {
	mu   sync.RWMutex
	data map[K]V
}

// New 创建并发安全 Map，可选初始容量。
func New[K comparable, V any](capHint ...int) *Map[K, V] {
	c := 0
	if len(capHint) > 0 {
		c = capHint[0]
	}
	if c < 0 {
		c = 0
	}
	return &Map[K, V]{data: make(map[K]V, c)}
}

// Get 读取键。
func (m *Map[K, V]) Get(key K) (V, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	return v, ok
}

// Set 写入键值。
func (m *Map[K, V]) Set(key K, value V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
}

// SetIfAbsent 仅在键不存在时写入；返回是否实际写入。
func (m *Map[K, V]) SetIfAbsent(key K, value V) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[key]; ok {
		return false
	}
	m.data[key] = value
	return true
}

// Delete 删除键。
func (m *Map[K, V]) Delete(key K) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
}

// Len 返回长度。
func (m *Map[K, V]) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.data)
}

// Keys 返回所有键的副本。
func (m *Map[K, V]) Keys() []K {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]K, 0, len(m.data))
	for k := range m.data {
		out = append(out, k)
	}
	return out
}

// Values 返回所有值的副本。
func (m *Map[K, V]) Values() []V {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]V, 0, len(m.data))
	for _, v := range m.data {
		out = append(out, v)
	}
	return out
}

func (m *Map[K, V]) ForEach(fn func(K, V)) {
	m.mu.RLock()
	keys := make([]K, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	m.mu.RUnlock()
	for _, k := range keys {
		v := m.data[k]
		fn(k, v)
	}
}

func (m *Map[K, V]) ForEachWithStop(fn func(K, V) bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for k, v := range m.data {
		if !fn(k, v) {
			return
		}
	}
}

func (m *Map[K, V]) RawSnapshot() map[K]V {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[K]V, len(m.data))
	for k, v := range m.data {
		out[k] = v
	}
	return out
}

// Snapshot 返回整个 map 的浅拷贝（新 map）。
func (m *Map[K, V]) Snapshot() map[K]V {
	return m.RawSnapshot()
}

// Clear 清空 map。
func (m *Map[K, V]) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = make(map[K]V)
}

// ComputeIfAbsent 若键不存在则用 compute 生成并写入。
func (m *Map[K, V]) ComputeIfAbsent(key K, compute func() V) V {
	// 快速读路径。
	if v, ok := m.Get(key); ok {
		return v
	}
	// 加写锁二次检查。
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.data[key]; ok {
		return v
	}
	v := compute()
	m.data[key] = v
	return v
}
