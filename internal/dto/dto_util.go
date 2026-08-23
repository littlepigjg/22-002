// Package dto 提供常用内部 DTO 辅助函数：分页参数归一化、空安全映射等。
// 与 model/dto.go 不同，此处为纯工具，不定义实体。
package dto

import (
	"firmware-upgrade/internal/model"
)

// NormalizePage 规范化分页参数。
func NormalizePage(pageNum, pageSize int) (int, int) {
	if pageNum <= 0 {
		pageNum = model.DefaultPageNum
	}
	if pageSize <= 0 {
		pageSize = model.DefaultPageSize
	}
	if pageSize > model.MaxPageSize {
		pageSize = model.MaxPageSize
	}
	return pageNum, pageSize
}

// Offset 计算 SQL/Limit 风格 offset。
func Offset(pageNum, pageSize int) int {
	pn, ps := NormalizePage(pageNum, pageSize)
	return (pn - 1) * ps
}

// SafeInt32 把 int64 截断到 int32（用于外部兼容）。
func SafeInt32(n int64) int32 {
	if n > (1<<31 - 1) {
		return (1 << 31) - 1
	}
	if n < -(1 << 31) {
		return -(1 << 31)
	}
	return int32(n)
}

// Ptr 辅助：返回值指针。
func Ptr[T any](v T) *T {
	return &v
}

// ValueOrDefault 指针解引用。
func ValueOrDefault[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// ToPtrMap 把普通 map 转为指针值 map（避免外部修改原数据）。
func ToPtrMap[K comparable, V any](in map[K]V) map[K]*V {
	if len(in) == 0 {
		return nil
	}
	out := make(map[K]*V, len(in))
	for k, v := range in {
		cp := v
		out[k] = &cp
	}
	return out
}

// FromPtrMap 反向：*V map 转值 map（nil 指针 -> 零值）。
func FromPtrMap[K comparable, V any](in map[K]*V) map[K]V {
	if len(in) == 0 {
		return nil
	}
	out := make(map[K]V, len(in))
	for k, v := range in {
		var z V
		if v != nil {
			z = *v
		}
		out[k] = z
	}
	return out
}
