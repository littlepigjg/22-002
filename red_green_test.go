package firmware_upgrade_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	fu "firmware-upgrade"
	"firmware-upgrade/internal/model"
)

func buildExec(taskID, deviceID string, progress int) *model.TaskDeviceExecution {
	return &model.TaskDeviceExecution{
		TaskID:     taskID,
		DeviceID:   deviceID,
		Status:     model.UpgradeStatusUpgrading,
		Progress:   progress,
		AssignedAt: time.Now(),
	}
}

func TestRedGreen(t *testing.T) {
	ctx := context.Background()
	asm := fu.NewAssembly(nil)
	ps := asm.Services.Progress

	const keysPerRound = 10
	const rounds = 2
	const writers = 4
	const readers = 4
	const deleters = 1
	const setPerKey = 200

	var failures int64
	var totalGets int64

	for r := 0; r < rounds; r++ {
		var wg sync.WaitGroup
		taskID := fmt.Sprintf("task-%d", r)
		otherTask := fmt.Sprintf("task-other-%d", r)

		for i := 0; i < keysPerRound; i++ {
			deviceID := fmt.Sprintf("dev-%d", i)
			e0 := buildExec(taskID, deviceID, 0)
			if err := ps.SaveExecWithGuard(ctx, e0, true); err != nil {
				t.Fatalf("initial save failed: %v", err)
			}
			eo := buildExec(otherTask, deviceID, 0)
			_ = ps.SaveExecWithGuard(ctx, eo, true)
		}

		check := func() {
			snap := ps.ExecSnapshot()
			if len(snap) == 0 {
				return
			}
			for k := range snap {
				taskIDWant := snap[k].TaskID
				devIDWant := snap[k].DeviceID
				got, err := ps.GetExecWithGuard(ctx, taskIDWant, devIDWant)
				if err != nil {
					atomic.AddInt64(&failures, 1)
					continue
				}
				if got == nil {
					atomic.AddInt64(&failures, 1)
					continue
				}
				if got.TaskID != taskIDWant || got.DeviceID != devIDWant {
					atomic.AddInt64(&failures, 1)
				}
			}
			for i := 0; i < keysPerRound; i++ {
				deviceID := fmt.Sprintf("dev-%d", i)
				gotA, errA := ps.GetExecWithGuard(ctx, taskID, deviceID)
				if errA == nil && gotA != nil {
					key := gotA.TaskID + "|" + gotA.DeviceID
					if _, inSnap := snap[key]; !inSnap {
						atomic.AddInt64(&failures, 1)
					}
				}
				gotB, errB := ps.GetExecWithGuard(ctx, otherTask, deviceID)
				if errB == nil && gotB != nil {
					key := gotB.TaskID + "|" + gotB.DeviceID
					if _, inSnap := snap[key]; !inSnap {
						atomic.AddInt64(&failures, 1)
					}
				}
			}
		}

		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func(wi, ri int) {
				defer wg.Done()
				for s := 0; s < setPerKey; s++ {
					idx := (wi*setPerKey + s) % keysPerRound
					deviceID := fmt.Sprintf("dev-%d", idx)
					e := buildExec(taskID, deviceID, (s%100)+1)
					_ = ps.SaveExecWithGuard(ctx, e, true)
					if (wi*setPerKey+s)%13 == 0 {
						ps.PurgeExecCacheNow()
					}
				}
			}(w, r)
		}

		for rd := 0; rd < readers; rd++ {
			wg.Add(1)
			go func(rdi, ri int) {
				defer wg.Done()
				totalLoops := setPerKey * 2
				for s := 0; s < totalLoops; s++ {
					idx := (rdi*totalLoops + s) % keysPerRound
					deviceID := fmt.Sprintf("dev-%d", idx)
					_, err := ps.GetExecWithGuard(ctx, taskID, deviceID)
					atomic.AddInt64(&totalGets, 1)
					if err != nil && err != model.ErrNotFound {
						atomic.AddInt64(&failures, 1)
						continue
					}
					if (rdi*totalLoops+s)%19 == 0 {
						ps.PurgeExecCacheNow()
					}
				}
			}(rd, r)
		}

		for dl := 0; dl < deleters; dl++ {
			wg.Add(1)
			go func(dli, ri int) {
				defer wg.Done()
				for s := 0; s < setPerKey; s++ {
					var tgt string
					if (dli*setPerKey + s) % 2 == 0 {
						tgt = taskID
					} else {
						tgt = otherTask
					}
					if (dli*setPerKey+s)%7 == 0 {
						_ = ps.DeleteTaskExecs(ctx, tgt)
					}
					idx := (dli*setPerKey + s) % keysPerRound
					deviceID := fmt.Sprintf("dev-%d", idx)
					eo := buildExec(tgt, deviceID, s%50)
					_ = ps.SaveExecWithGuard(ctx, eo, true)
				}
			}(dl, r)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			iterations := setPerKey / 4
			if iterations < 1 {
				iterations = 1
			}
			for s := 0; s < iterations; s++ {
				check()
			}
		}()

		wg.Wait()

		check()
	}

	fmt.Printf("total_gets=%d failures=%d\n", totalGets, atomic.LoadInt64(&failures))

	if atomic.LoadInt64(&failures) > 0 {
		fmt.Printf("RED（红灯，缺陷未修复）: exec store+cache 发生 %d 次键值一致性失败；快照与带缓存 Get 结果不匹配\n", atomic.LoadInt64(&failures))
		t.Errorf("RED: %d consistency failures detected", atomic.LoadInt64(&failures))
		return
	}
	fmt.Println("GREEN（绿灯，缺陷已修复）: 所有并发写+淘汰+删除+读路径下键值一致性通过")
}
