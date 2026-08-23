package safemap

import (
	"sync"
	"testing"
)

// TestForEachConcurrentWithWriters 复现并验证 ForEach 与并发写之间的数据竞争/崩溃。
// 修复前：ForEach 释放读锁后再访问 m.data[k]，与并发 Set/Delete 写入同一份 map，
// 在 -race 下报 "DATA RACE"，非 -race 下偶发 "concurrent map read and map write"。
// 修复后：回调遍历的是锁内快照的副本，不再触碰底层 map，无竞争、无崩溃。
func TestForEachConcurrentWithWriters(t *testing.T) {
	m := New[int, int](256)
	for i := 0; i < 128; i++ {
		m.Set(i, i)
	}

	const readers = 6
	const writers = 8
	const iters = 3000
	var wg sync.WaitGroup

	// 读侧：高频 ForEach，对应 Poll->FindAssignedRunning 链路。
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				m.ForEach(func(k, v int) { _ = k + v })
			}
		}()
	}

	// 写侧：高频 Set/Delete，对应 UpdateProgress/Upsert 刷进度。
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(off int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				key := (i + off) % 256
				m.Set(key, i)
				if i%3 == 0 {
					m.Delete(key)
				}
			}
		}(w * 31)
	}

	wg.Wait()
}
