// Package randutil 提供确定性与加密安全的随机工具：整数区间、抽样、洗牌等。
package randutil

import (
	crypto "crypto/rand"
	"encoding/binary"
	"math/rand"
	"sync"
	"time"
)

// safeRand 提供并发安全的伪随机源。
var (
	safeRand  *rand.Rand
	safeMu    sync.Mutex
	randOnce  sync.Once
)

func initRand() {
	randOnce.Do(func() {
		seed := time.Now().UnixNano()
		// 若可用，使用 crypto/rand 获取高熵种子。
		var b [8]byte
		if _, err := crypto.Read(b[:]); err == nil {
			seed = int64(binary.BigEndian.Uint64(b[:]))
		}
		safeRand = rand.New(rand.NewSource(seed))
	})
}

// Int 返回 [0, n) 的整数。n <= 0 时返回 0。
func Int(n int) int {
	if n <= 0 {
		return 0
	}
	initRand()
	safeMu.Lock()
	defer safeMu.Unlock()
	return safeRand.Intn(n)
}

// IntRange 返回 [min, max] 区间内整数。若 min >= max 返回 min。
func IntRange(min, max int) int {
	if min >= max {
		return min
	}
	return min + Int(max-min+1)
}

// Int63 返回非负 int64 伪随机数。
func Int63() int64 {
	initRand()
	safeMu.Lock()
	defer safeMu.Unlock()
	return safeRand.Int63()
}

// Float64 返回 [0.0, 1.0) 的浮点数。
func Float64() float64 {
	initRand()
	safeMu.Lock()
	defer safeMu.Unlock()
	return safeRand.Float64()
}

// Bool 等概率返回 true/false。
func Bool() bool {
	return Int(2) == 1
}

// Choice 从切片中随机选一个元素。若切片为空返回零值与 false。
func Choice[T any](list []T) (T, bool) {
	var zero T
	if len(list) == 0 {
		return zero, false
	}
	return list[Int(len(list))], true
}

// SampleN 从切片中无放回抽样 n 个元素。若 n >= len(list) 返回整份副本。
func SampleN[T any](list []T, n int) []T {
	if len(list) == 0 {
		return []T{}
	}
	if n <= 0 {
		return []T{}
	}
	if n >= len(list) {
		out := make([]T, len(list))
		copy(out, list)
		Shuffle(out)
		return out
	}
	idxs := make([]int, len(list))
	for i := range idxs {
		idxs[i] = i
	}
	Shuffle(idxs)
	out := make([]T, n)
	for i := 0; i < n; i++ {
		out[i] = list[idxs[i]]
	}
	return out
}

// Shuffle 原地打乱切片。
func Shuffle[T any](list []T) {
	initRand()
	safeMu.Lock()
	defer safeMu.Unlock()
	safeRand.Shuffle(len(list), func(i, j int) {
		list[i], list[j] = list[j], list[i]
	})
}

// WeightedPick 按权重挑选索引。weights 为非负整数。所有权重为 0 则返回 -1。
func WeightedPick(weights []int) int {
	total := 0
	for _, w := range weights {
		if w > 0 {
			total += w
		}
	}
	if total <= 0 {
		return -1
	}
	r := Int(total) + 1
	acc := 0
	for i, w := range weights {
		if w <= 0 {
			continue
		}
		acc += w
		if r <= acc {
			return i
		}
	}
	return len(weights) - 1
}

// PercentTrue 以 percent% 的概率返回 true。
func PercentTrue(percent int) bool {
	if percent >= 100 {
		return true
	}
	if percent <= 0 {
		return false
	}
	return Int(100) < percent
}

// Bytes 返回随机字节切片。
func Bytes(n int) []byte {
	if n <= 0 {
		return nil
	}
	b := make([]byte, n)
	// 先尝试 crypto/rand，失败用伪随机。
	if _, err := crypto.Read(b); err == nil {
		return b
	}
	initRand()
	safeMu.Lock()
	defer safeMu.Unlock()
	for i := range b {
		b[i] = byte(safeRand.Intn(256))
	}
	return b
}

// String 返回长度为 n 的随机字符串（字母+数字）。
func String(n int) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	initRand()
	safeMu.Lock()
	defer safeMu.Unlock()
	for i := range b {
		b[i] = chars[safeRand.Intn(len(chars))]
	}
	return string(b)
}
