// Package store 并发计数一致性测试：验证计数器与 List 在并发写下保持一致。
package store

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"firmware-upgrade/internal/model"
)

// TestTaskStoreConcurrentCountConsistency 并发创建/改状态/删除任务，
// 反复断言 Total / RunningCount 与 List 真实长度一致、Running <= Total。
func TestTaskStoreConcurrentCountConsistency(t *testing.T) {
	ctx := context.Background()
	s := NewUpgradeTaskStore()

	var stop int32
	var wg sync.WaitGroup

	// N 个 worker 不断创建→改状态→删除任务。
	const workers = 12
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			i := 0
			for atomic.LoadInt32(&stop) == 0 {
				i++
				id := fmt.Sprintf("t-%d-%d", seed, i)
				task := &model.UpgradeTask{
					ID:        id,
					Name:      id,
					ModelID:   "m1",
					Status:    model.TaskStatusPending,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				if err := s.Create(ctx, task); err != nil {
					continue
				}
				// pending -> running -> finished/canceled
				_ = s.SetStatus(ctx, id, model.TaskStatusRunning, time.Time{})
				if i%2 == 0 {
					_ = s.SetStatus(ctx, id, model.TaskStatusFinished, time.Now())
				} else {
					_ = s.SetStatus(ctx, id, model.TaskStatusCanceled, time.Now())
				}
				_ = s.Delete(ctx, id)
			}
		}(w)
	}

	// 校验 goroutine：反复检查不变量。使用单次加锁的快照，
	// 保证 total / running / listTotal 同源，避免多次独立加锁的 TOCTOU 误报。
	var checkErr atomic.Value
	wg.Add(1)
	go func() {
		defer wg.Done()
		for atomic.LoadInt32(&stop) == 0 {
			total, running, listTotal := s.(*inMemoryTaskStore).TaskCountSnapshot()

			if total != listTotal {
				checkErr.Store(fmt.Errorf("Total=%d but List total=%d", total, listTotal))
				atomic.StoreInt32(&stop, 1)
				return
			}
			if running > total {
				checkErr.Store(fmt.Errorf("Running=%d > Total=%d", running, total))
				atomic.StoreInt32(&stop, 1)
				return
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	atomic.StoreInt32(&stop, 1)
	wg.Wait()

	if v := checkErr.Load(); v != nil {
		t.Fatalf("invariant violated: %v", v.(error))
	}
}

// TestDeviceStoreConcurrentCountConsistency 并发注册/心跳/删除设备，
// 断言 Total / CountByStatus 与 List 真实长度一致、online <= total。
func TestDeviceStoreConcurrentCountConsistency(t *testing.T) {
	ctx := context.Background()
	s := NewDeviceStore()

	var stop int32
	var wg sync.WaitGroup

	const workers = 12
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			i := 0
			for atomic.LoadInt32(&stop) == 0 {
				i++
				id := fmt.Sprintf("d-%d-%d", seed, i)
				dev := &model.Device{
					ID:              id,
					Name:            id,
					ModelID:         "m1",
					CurrentVersion:  "v1",
					Status:          model.DeviceStatusOnline,
					LastHeartbeatAt: time.Now(),
					RegisterAt:      time.Now(),
					UpdatedAt:       time.Now(),
				}
				if err := s.Create(ctx, dev); err != nil {
					continue
				}
				// online -> offline -> online 心跳切换
				_ = s.Heartbeat(ctx, id, "v1", model.DeviceStatusOffline, "1.1.1.1", time.Now())
				_ = s.Heartbeat(ctx, id, "v1", model.DeviceStatusOnline, "1.1.1.1", time.Now())
				_ = s.Delete(ctx, id)
			}
		}(w)
	}

	var checkErr atomic.Value
	wg.Add(1)
	go func() {
		defer wg.Done()
		for atomic.LoadInt32(&stop) == 0 {
			total, online, listTotal := s.(*inMemoryDeviceStore).DeviceCountSnapshot()

			if total != listTotal {
				checkErr.Store(fmt.Errorf("Device Total=%d but List total=%d", total, listTotal))
				atomic.StoreInt32(&stop, 1)
				return
			}
			if online > total {
				checkErr.Store(fmt.Errorf("Online=%d > Device=%d", online, total))
				atomic.StoreInt32(&stop, 1)
				return
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	atomic.StoreInt32(&stop, 1)
	wg.Wait()

	if v := checkErr.Load(); v != nil {
		t.Fatalf("invariant violated: %v", v.(error))
	}
}
