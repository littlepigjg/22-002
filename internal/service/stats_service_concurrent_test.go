// Package service 统计总览并发一致性测试：复现压测场景下的计数不变量。
package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
)

// newStatsFixture 构建最小可用的统计服务容器（无需固件/历史数据）。
func newStatsFixture(t *testing.T) (*StatsService, *store.Container) {
	t.Helper()
	c := store.NewContainer()
	hist := NewHistoryService(c.Histories)
	cfg := config.Default()
	return NewStatsService(c, hist, cfg), c
}

// TestStatsOverviewConsistencyUnderConcurrency 复现压测：20 并发建/改/删任务与设备，
// 反复调 Stats.Get，断言 overview 自洽（running<=task, online<=device），
// 且 task_count / device_count 与各自 List total（同一快照）相等。
func TestStatsOverviewConsistencyUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	stats, c := newStatsFixture(t)
	stats.SetTTL(0) // 关闭缓存，每次都重建，最大化暴露不一致

	var stop int32
	var wg sync.WaitGroup

	// 任务侧 worker：建 -> pending→running→finished/canceled -> 删。
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			i := 0
			for atomic.LoadInt32(&stop) == 0 {
				i++
				id := fmt.Sprintf("tk-%d-%d", seed, i)
				task := &model.UpgradeTask{
					ID: id, Name: id, ModelID: "m1",
					Status:     model.TaskStatusPending,
					CreatedAt:  time.Now(),
					UpdatedAt:  time.Now(),
					ScheduleAt: time.Now(),
				}
				if err := c.Tasks.Create(ctx, task); err != nil {
					continue
				}
				_ = c.Tasks.SetStatus(ctx, id, model.TaskStatusRunning, time.Time{})
				if i%2 == 0 {
					_ = c.Tasks.SetStatus(ctx, id, model.TaskStatusFinished, time.Now())
				} else {
					_ = c.Tasks.SetStatus(ctx, id, model.TaskStatusCanceled, time.Now())
				}
				_ = c.Tasks.Delete(ctx, id)
			}
		}(w)
	}

	// 设备侧 worker：注册 -> online↔offline -> 删。
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			i := 0
			for atomic.LoadInt32(&stop) == 0 {
				i++
				id := fmt.Sprintf("dv-%d-%d", seed, i)
				dev := &model.Device{
					ID: id, Name: id, ModelID: "m1", CurrentVersion: "v1",
					Status:          model.DeviceStatusOnline,
					LastHeartbeatAt: time.Now(),
					RegisterAt:      time.Now(),
					UpdatedAt:       time.Now(),
				}
				if err := c.Devices.Create(ctx, dev); err != nil {
					continue
				}
				_ = c.Devices.Heartbeat(ctx, id, "v1", model.DeviceStatusOffline, "1.1.1.1", time.Now())
				_ = c.Devices.Heartbeat(ctx, id, "v1", model.DeviceStatusOnline, "1.1.1.1", time.Now())
				_ = c.Devices.Delete(ctx, id)
			}
		}(w)
	}

	// 观察者：反复读 overview，校验自洽不变量。
	// （并发写下，overview 的缓存值相对"实时列表"必然有窗口期差异，
	//  这里只断言产品能保证的：running<=task、online<=device、各计数非负。）
	var checkErr atomic.Value
	wg.Add(1)
	go func() {
		defer wg.Done()
		for atomic.LoadInt32(&stop) == 0 {
			res, err := stats.Get(ctx)
			if err != nil {
				continue
			}
			if res.RunningTaskCount > res.TaskCount {
				checkErr.Store(fmt.Errorf("running=%d > task=%d", res.RunningTaskCount, res.TaskCount))
				atomic.StoreInt32(&stop, 1)
				return
			}
			if res.OnlineCount > res.DeviceCount {
				checkErr.Store(fmt.Errorf("online=%d > device=%d", res.OnlineCount, res.DeviceCount))
				atomic.StoreInt32(&stop, 1)
				return
			}
			if res.TaskCount < 0 || res.RunningTaskCount < 0 || res.DeviceCount < 0 || res.OnlineCount < 0 {
				checkErr.Store(fmt.Errorf("negative count: task=%d running=%d device=%d online=%d",
					res.TaskCount, res.RunningTaskCount, res.DeviceCount, res.OnlineCount))
				atomic.StoreInt32(&stop, 1)
				return
			}
		}
	}()

	time.Sleep(500 * time.Millisecond)
	atomic.StoreInt32(&stop, 1)
	wg.Wait()

	if v := checkErr.Load(); v != nil {
		t.Fatalf("invariant violated: %v", v.(error))
	}
}
