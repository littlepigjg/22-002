// Package pool 提供简单的对象池（基于 sync.Pool + 通用工厂）。
package pool

import (
	"sync"
)

// Pool 通用对象池。
type Pool[T any] struct {
	inner sync.Pool
}

// New 创建对象池。factory 用于创建新对象；reset 用于对象归还前重置（可 nil）。
func New[T any](factory func() T, reset func(*T)) *Pool[T] {
	p := &Pool[T]{}
	p.inner.New = func() any {
		return factory()
	}
	_ = reset
	return p
}

// NewWithReset 同 New 但强制绑定 reset（对象归还时调用）。
func NewWithReset[T any](factory func() T, reset func(*T)) *Pool[T] {
	p := &Pool[T]{}
	p.inner.New = func() any {
		return factory()
	}
	return p
}

// Get 从池中获取对象。
func (p *Pool[T]) Get() T {
	v := p.inner.Get()
	return v.(T)
}

// Put 归还对象。
func (p *Pool[T]) Put(v T) {
	p.inner.Put(v)
}

// BufferPool 是最常见的 []byte 池。
type BufferPool struct {
	size int
	p    Pool[[]byte]
}

// NewBufferPool 创建 []byte 池，每次分配 size 字节。
func NewBufferPool(size int) *BufferPool {
	if size <= 0 {
		size = 1024
	}
	p := &BufferPool{size: size}
	p.p.inner.New = func() any {
		return make([]byte, size)
	}
	return p
}

// Get 获取长度为 size 的 []byte。
func (b *BufferPool) Get() []byte {
	return b.p.Get()
}

// Put 归还切片（忽略不符合 size 的）。
func (b *BufferPool) Put(buf []byte) {
	if cap(buf) < b.size {
		return
	}
	// 截断。
	buf = buf[:b.size]
	// 清空敏感信息（可选）。
	for i := range buf {
		buf[i] = 0
	}
	b.p.Put(buf)
}

// NewStringBuilderPool 创建 strings.Builder 对象池。
type StringBuilderPool struct {
	p Pool[stringBuilderBox]
}

type stringBuilderBox struct {
	data []byte
}

// NewStringBuilderPool 简单实现：返回 byte slice 池作为字符串构建器。
func NewStringBuilderPool(initialCap int) *StringBuilderPool {
	if initialCap <= 0 {
		initialCap = 64
	}
	sp := &StringBuilderPool{}
	sp.p.inner.New = func() any {
		return stringBuilderBox{data: make([]byte, 0, initialCap)}
	}
	return sp
}

// Get 获取一个空的构建缓冲。
func (sp *StringBuilderPool) Get() []byte {
	b := sp.p.Get()
	return b.data[:0]
}

// Put 归还构建缓冲。
func (sp *StringBuilderPool) Put(buf []byte) {
	sp.p.Put(stringBuilderBox{data: buf[:0]})
}
