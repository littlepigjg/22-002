// Package store 历史存储并发安全测试。
package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/timeutil"
)

// makeHistory 构造一条历史记录。
func makeHistory(deviceID, taskID, id string, status model.UpgradeStatus, ts time.Time) *model.UpgradeHistory {
	return &model.UpgradeHistory{
		ID:        id,
		TaskID:    taskID,
		DeviceID:  deviceID,
		ModelID:   "GW-100",
		Status:    status,
		Progress:  100,
		StartedAt: ts,
	}
}

// TestHistoryStoreConcurrentRace 压测：一半协程反复上报 Progress 到达终态（经 AppendHistoryRecord
// 的 Unsafe 写路径），另一半反复读 CountDaily / FindLatestByDevice，验证无 data race、无 panic。
func TestHistoryStoreConcurrentRace(t *testing.T) {
	s := NewUpgradeHistoryStore()

	const writers = 20
	const readers = 20
	const iters = 200

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	// writers: 反复到达 Success/Failed 终态，经 Unsafe 写路径写入。
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer wg.Done()
			devID := fmt.Sprintf("dev-%d", i)
			taskID := fmt.Sprintf("task-%d", i%4)
			for n := 0; n < iters; n++ {
				ts := timeutil.Now()
				status := model.UpgradeStatusSuccess
				if n%2 == 1 {
					status = model.UpgradeStatusFailed
				}
				id := fmt.Sprintf("%s:%s:%d-%d", taskID, devID, n, ts.UnixNano())
				h := makeHistory(devID, taskID, id, status, ts)
				// 模拟 HistoryService.AppendHistoryRecord 的 Unsafe 写路径（单次写入）。
				putHistoryUnsafe(s, h, taskID, devID)
			}
		}()
	}

	// readers: 反复读 CountDaily 与 FindLatestByDevice。
	for i := 0; i < readers; i++ {
		i := i
		go func() {
			defer wg.Done()
			devID := fmt.Sprintf("dev-%d", i%writers)
			taskID := fmt.Sprintf("task-%d", i%4)
			for n := 0; n < iters; n++ {
				if _, err := s.CountDaily(context.Background(), 7); err != nil {
					t.Errorf("CountDaily error: %v", err)
				}
				if _, err := s.FindLatestByDevice(context.Background(), devID, taskID); err != nil && err != model.ErrNotFound {
					t.Errorf("FindLatestByDevice error: %v", err)
				}
				if _, _, _, err := s.Count(context.Background()); err != nil {
					t.Errorf("Count error: %v", err)
				}
			}
		}()
	}

	wg.Wait()
}

// TestHistoryStoreConcurrentCorrectness 验证高并发写后统计数对得上。
func TestHistoryStoreConcurrentCorrectness(t *testing.T) {
	s := NewUpgradeHistoryStore()
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			devID := fmt.Sprintf("dev-%d", i)
			taskID := "task-X"
			ts := timeutil.Now()
			id := fmt.Sprintf("%s:%s:%d", taskID, devID, ts.UnixNano())
			h := makeHistory(devID, taskID, id, model.UpgradeStatusSuccess, ts)
			// 走生产环境 AppendHistoryRecord 的 Unsafe 写路径（单次写入索引）。
			putHistoryUnsafe(s, h, taskID, devID)
		}()
	}
	wg.Wait()

	total, success, failed, err := s.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if total != n {
		t.Errorf("total = %d, want %d", total, n)
	}
	if success != n {
		t.Errorf("success = %d, want %d", success, n)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0", failed)
	}

	daily, err := s.CountDaily(context.Background(), 1)
	if err != nil {
		t.Fatalf("CountDaily: %v", err)
	}
	if len(daily) != 1 {
		t.Fatalf("daily len = %d, want 1", len(daily))
	}
	if daily[0].Total != n {
		t.Errorf("daily total = %d, want %d", daily[0].Total, n)
	}
}

// putHistoryUnsafe 走 Unsafe 写路径（模拟 HistoryService.AppendHistoryRecord）。
func putHistoryUnsafe(s UpgradeHistoryStore, h *model.UpgradeHistory, taskID, deviceID string) {
	type writer interface {
		UnsafePutData(hh *model.UpgradeHistory)
		UnsafePutByDeviceIndex(did, hid string)
		UnsafePutByTaskIndex(tid, hid string)
		UnsafePutByDayIndex(day, hid string)
	}
	w, ok := s.(writer)
	if !ok {
		return
	}
	w.UnsafePutData(h)
	if taskID != "" {
		w.UnsafePutByTaskIndex(taskID, h.ID)
	}
	w.UnsafePutByDeviceIndex(deviceID, h.ID)
	if !h.StartedAt.IsZero() {
		w.UnsafePutByDayIndex(timeutil.FormatDate(h.StartedAt), h.ID)
	}
}

var _ = time.Now
