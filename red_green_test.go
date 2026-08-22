package firmwareupgrade_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
)

func makeTask(id string, status model.TaskStatus) *model.UpgradeTask {
	now := time.Now()
	return &model.UpgradeTask{
		ID:             id,
		Name:           "task-" + id,
		ModelID:        "GW-100",
		TargetVersion:  "v1.0",
		FirmwareID:     "fw1",
		Strategy:       model.StrategyFull,
		Status:         status,
		ScheduleAt:     now,
		TimeoutSeconds: 60,
		MaxRetry:       3,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func makeDevice(id string, status model.DeviceStatus, hb time.Time) *model.Device {
	now := time.Now()
	return &model.Device{
		ID:              id,
		ModelID:         "GW-100",
		Name:            "dev-" + id,
		CurrentVersion:  "v0.9",
		TargetVersion:   "v1.0",
		Status:          status,
		IP:              "10.0.0.1",
		MAC:             "00:11:22:33:44:55",
		Group:           "g1",
		LastHeartbeatAt: hb,
		RegisterAt:      now,
		UpdatedAt:       now,
	}
}

type seqCounter struct {
	mu sync.Mutex
	v  int64
}

func (c *seqCounter) next() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.v++
	return c.v
}

func TestRedGreen(t *testing.T) {
	ctx := context.Background()
	stores := store.NewContainer()
	cfg := config.Default()
	cfg.HeartbeatTTL = 3600 * 24
	hist := service.NewHistoryService(stores.Histories)
	statsSvc := service.NewStatsService(stores, hist, cfg)
	statsSvc.SetTTL(0)

	totalGoroutines := 20
	rounds := 30
	var workerWG sync.WaitGroup
	var checkerWG sync.WaitGroup
	seq := &seqCounter{v: 0}
	var stop int32
	var failureSeen int32
	var firstFailureMu sync.Mutex
	var firstFailure string

	setFail := func(s string) {
		firstFailureMu.Lock()
		defer firstFailureMu.Unlock()
		if atomic.LoadInt32(&failureSeen) == 0 {
			firstFailure = s
		}
		atomic.StoreInt32(&failureSeen, 1)
	}

	workerWG.Add(totalGoroutines)
	for g := 0; g < totalGoroutines; g++ {
		go func(gi int) {
			defer workerWG.Done()
			for r := 0; r < rounds; r++ {
				n := seq.next()
				tid := fmt.Sprintf("t%d-%d-%d", gi, r, n)
				pick := n % 6
				var st model.TaskStatus
				switch pick {
				case 0:
					st = model.TaskStatusPending
				case 1:
					st = model.TaskStatusRunning
				case 2:
					st = model.TaskStatusPaused
				case 3:
					st = model.TaskStatusFinished
				case 4:
					st = model.TaskStatusCanceled
				case 5:
					st = model.TaskStatusFailed
				}
				if err := stores.Tasks.Create(ctx, makeTask(tid, st)); err == nil {
					switch pick {
					case 0:
						_ = stores.Tasks.SetStatus(ctx, tid, model.TaskStatusRunning, time.Time{})
						_ = stores.Tasks.SetStatus(ctx, tid, model.TaskStatusFinished, time.Now())
					case 1:
						_ = stores.Tasks.SetStatus(ctx, tid, model.TaskStatusFailed, time.Now())
					case 2:
						_ = stores.Tasks.SetStatus(ctx, tid, model.TaskStatusRunning, time.Time{})
					}
					_ = stores.Tasks.Delete(ctx, tid)
				}

				did := fmt.Sprintf("d%d-%d-%d", gi, r, n)
				d := makeDevice(did, model.DeviceStatusOnline, time.Now())
				if err := stores.Devices.Create(ctx, d); err == nil {
					_ = stores.Devices.Heartbeat(ctx, did, "v1.0", model.DeviceStatusOnline, "10.0.0.2", time.Now())
					_ = stores.Devices.Heartbeat(ctx, did, "v1.0", model.DeviceStatusOffline, "10.0.0.3", time.Now())
					_ = stores.Devices.Delete(ctx, did)
				}
			}
		}(g)
	}

	checkerWG.Add(4)
	for w := 0; w < 4; w++ {
		go func() {
			defer checkerWG.Done()
			for atomic.LoadInt32(&stop) == 0 {
				s, err := statsSvc.Get(ctx)
				if err != nil {
					continue
				}
				_, tReal, err := stores.Tasks.List(ctx, "", "", "", "", "", "created_at", "desc", 1, 1000000)
				if err != nil {
					continue
				}
				_, dReal, err := stores.Devices.List(ctx, "", "", "", "", "", "", 0, 1, 1000000)
				if err != nil {
					continue
				}
				if s.TaskCount != tReal {
					setFail(fmt.Sprintf("stats.TaskCount=%d but tasks.List total=%d", s.TaskCount, tReal))
				}
				if s.DeviceCount != dReal {
					setFail(fmt.Sprintf("stats.DeviceCount=%d but devices.List total=%d", s.DeviceCount, dReal))
				}
				if s.RunningTaskCount > s.TaskCount {
					setFail(fmt.Sprintf("stats.RunningTaskCount=%d > stats.TaskCount=%d", s.RunningTaskCount, s.TaskCount))
				}
				if s.OnlineCount > s.DeviceCount {
					setFail(fmt.Sprintf("stats.OnlineCount=%d > stats.DeviceCount=%d", s.OnlineCount, s.DeviceCount))
				}
			}
		}()
	}

	workerWG.Wait()
	atomic.StoreInt32(&stop, 1)
	checkerWG.Wait()

	for i := 0; i < 50; i++ {
		tid := fmt.Sprintf("final-task-%d", i)
		st := model.TaskStatusRunning
		if i%2 == 0 {
			st = model.TaskStatusPending
		} else if i%7 == 0 {
			st = model.TaskStatusPaused
		}
		_ = stores.Tasks.Create(ctx, makeTask(tid, st))
	}
	for i := 0; i < 120; i++ {
		did := fmt.Sprintf("final-dev-%d", i)
		ds := model.DeviceStatusOnline
		if i%5 == 0 {
			ds = model.DeviceStatusOffline
		} else if i%13 == 0 {
			ds = model.DeviceStatusUnknown
		}
		_ = stores.Devices.Create(ctx, makeDevice(did, ds, time.Now()))
	}
	statsSvc.Invalidate()
	st, err := statsSvc.Get(ctx)
	if err != nil {
		t.Fatalf("statsSvc.Get error: %v", err)
	}
	_, tReal, err := stores.Tasks.List(ctx, "", "", "", "", "", "created_at", "desc", 1, 100000)
	if err != nil {
		t.Fatalf("Tasks.List error: %v", err)
	}
	_, dReal, err := stores.Devices.List(ctx, "", "", "", "", "", "", 0, 1, 100000)
	if err != nil {
		t.Fatalf("Devices.List error: %v", err)
	}
	tCnt, _ := stores.Tasks.Total(ctx)
	dCnt, _ := stores.Devices.Total(ctx)
	rCnt, _ := stores.Tasks.RunningCount(ctx)
	oCnt, _, _, _ := stores.Devices.CountByStatus(ctx)

	inconsistent := atomic.LoadInt32(&failureSeen) == 1
	reason := firstFailure
	if !inconsistent {
		if st.TaskCount != tReal {
			inconsistent = true
			reason = fmt.Sprintf("stats.TaskCount=%d but tasks.List total=%d", st.TaskCount, tReal)
		} else if st.DeviceCount != dReal {
			inconsistent = true
			reason = fmt.Sprintf("stats.DeviceCount=%d but devices.List total=%d", st.DeviceCount, dReal)
		} else if st.RunningTaskCount > st.TaskCount {
			inconsistent = true
			reason = fmt.Sprintf("stats.RunningTaskCount=%d > stats.TaskCount=%d", st.RunningTaskCount, st.TaskCount)
		} else if st.OnlineCount > st.DeviceCount {
			inconsistent = true
			reason = fmt.Sprintf("stats.OnlineCount=%d > stats.DeviceCount=%d", st.OnlineCount, st.DeviceCount)
		} else if tCnt != tReal {
			inconsistent = true
			reason = fmt.Sprintf("tasks.Total=%d but tasks.List total=%d", tCnt, tReal)
		} else if dCnt != dReal {
			inconsistent = true
			reason = fmt.Sprintf("devices.Total=%d but devices.List total=%d", dCnt, dReal)
		} else if rCnt > tCnt {
			inconsistent = true
			reason = fmt.Sprintf("tasks.RunningCount=%d > tasks.Total=%d", rCnt, tCnt)
		} else if oCnt > dCnt {
			inconsistent = true
			reason = fmt.Sprintf("devices.CountByStatus.online=%d > devices.Total=%d", oCnt, dCnt)
		}
	}

	if inconsistent {
		fmt.Printf("RED（红灯，缺陷未修复） - %s\n", reason)
		t.Errorf("RED（红灯，缺陷未修复） - %s", reason)
		return
	}
	fmt.Println("GREEN（绿灯，缺陷已修复） - 所有统计结果与实际数据一致")
}
