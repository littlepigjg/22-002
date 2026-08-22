// Package service 并发进度上报压测与一致性校验测试。
package service

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/idgen"
	"firmware-upgrade/pkg/timeutil"
)

// newProgressFixture 构建一组装配好的服务与数据，返回进度服务、容器、任务ID与设备ID列表。
// 任务覆盖全部 nDevices 台设备并处于 running，每台设备有预创建的 exec 与 history。
func newProgressFixture(t *testing.T, nDevices int) (*ProgressService, *store.Container, string, []string) {
	t.Helper()
	cfg := config.Default()
	c := store.NewContainer()
	svc := NewServices(cfg, c)
	ctx := context.Background()

	// 注册设备型号。
	modelID := "M" + idgen.NextID()
	if _, err := svc.Model.Create(ctx, &model.CreateModelRequest{
		ID: modelID, Name: "M", Arch: "arm64",
	}); err != nil {
		t.Fatalf("create model: %v", err)
	}
	// 创建并发布固件。
	fw, err := svc.Firmware.Create(ctx, &model.CreateFirmwareRequest{
		ModelID: modelID, Version: "v2.0.0", Name: "fw",
		MD5: "0123456789abcdef0123456789abcdef", Size: 1024,
		FilePath: "/tmp/fw.bin", FileName: "fw.bin",
	})
	if err != nil {
		t.Fatalf("create firmware: %v", err)
	}
	if _, err := svc.Firmware.UpdateStatus(ctx, fw.ID, model.FirmwarePublished); err != nil {
		t.Fatalf("publish firmware: %v", err)
	}
	// 注册 nDevices 台设备。
	devIDs := make([]string, 0, nDevices)
	for i := 0; i < nDevices; i++ {
		did := "D" + idgen.NextID()
		devIDs = append(devIDs, did)
		if _, err := svc.Device.Register(ctx, &model.RegisterDeviceRequest{
			ID: did, ModelID: modelID, CurrentVersion: "v1.0.0",
		}); err != nil {
			t.Fatalf("create device %d: %v", i, err)
		}
	}
	// 创建全量升级任务（Status 立即 Running），会预创建 exec 与 history。
	task, err := svc.Task.Create(ctx, &model.CreateTaskRequest{
		Name: "T", ModelID: modelID, TargetVersion: "v2.0.0",
		Strategy: model.StrategyDeviceList, DeviceIDs: devIDs,
		TimeoutSeconds: 3600, MaxRetry: 10,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Status != model.TaskStatusRunning {
		t.Fatalf("task not running: %s", task.Status)
	}
	return svc.Progress, c, task.ID, devIDs
}

// TestProgressReportConcurrentRetryConsistency 模拟「同一设备多协程并发失败上报」，
// 这是 history.RetryCount < exec.RetryCount 的根因场景：修复前各协程在锁外读到
// 同一份旧 history 副本，各自 +1 后覆盖回 1，而 exec 经 UpdateProgress 正确累加到 K。
// 校验：无 data race；history.RetryCount == exec.RetryCount。
//
// 选用部分设备做失败上报、其余保持 pending，避免全部设备进入终态触发任务 finished。
func TestProgressReportConcurrentRetryConsistency(t *testing.T) {
	const nDevices = 50
	const targetDevices = 10 // 仅前 10 台做并发失败上报，其余保持 pending，任务不进终态。
	const workersPerDevice = 8 // 每台设备并发失败上报次数，需 < MaxRetry(10) 以保证 retryInc。

	prog, c, taskID, devIDs := newProgressFixture(t, nDevices)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < targetDevices; i++ {
		did := devIDs[i]
		for w := 0; w < workersPerDevice; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := prog.Report(ctx, &model.ReportProgressRequest{
					TaskID: taskID, DeviceID: did,
					Status: model.UpgradeStatusFailed, Progress: 100,
					ErrorMessage: "boom",
				}); err != nil {
					t.Errorf("report failed for %s: %v", did, err)
				}
			}()
		}
	}
	wg.Wait()

	// 一致性校验：exec.RetryCount 与 history.RetryCount 必须完全一致。
	mismatch := 0
	for i := 0; i < targetDevices; i++ {
		did := devIDs[i]
		exec, err := c.Execs.Get(ctx, taskID, did)
		if err != nil {
			t.Fatalf("get exec %s: %v", did, err)
		}
		hist, err := c.Histories.FindLatestByDevice(ctx, did, taskID)
		if err != nil {
			t.Fatalf("get history %s: %v", did, err)
		}
		if hist.RetryCount != exec.RetryCount {
			mismatch++
			t.Errorf("device %s retry mismatch: exec=%d history=%d", did, exec.RetryCount, hist.RetryCount)
		}
		// 失败上报 progress=100，exec 进度应固定为 100。
		if exec.Progress != 100 {
			t.Errorf("device %s progress not 100: %d", did, exec.Progress)
		}
	}
	if mismatch > 0 {
		t.Fatalf("%d/%d devices have retry count mismatch", mismatch, targetDevices)
	}
}

// TestProgressReportProgressMonotonic 校验进度不被乱序/迟到上报写回更低值：
// 并发上报 progress=100 与 progress=0（同为非终态 downloading），修复前可能落到 0，
// 修复后必为 100。
func TestProgressReportProgressMonotonic(t *testing.T) {
	prog, c, taskID, devIDs := newProgressFixture(t, 1)
	ctx := context.Background()
	did := devIDs[0]
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = prog.Report(ctx, &model.ReportProgressRequest{
				TaskID: taskID, DeviceID: did,
				Status: model.UpgradeStatusDownloading, Progress: 100,
			})
		}()
		go func() {
			defer wg.Done()
			_, _ = prog.Report(ctx, &model.ReportProgressRequest{
				TaskID: taskID, DeviceID: did,
				Status: model.UpgradeStatusDownloading, Progress: 0,
			})
		}()
	}
	wg.Wait()
	exec, err := c.Execs.Get(ctx, taskID, did)
	if err != nil {
		t.Fatalf("get exec: %v", err)
	}
	if exec.Progress != 100 {
		t.Fatalf("progress regressed to %d, expected 100", exec.Progress)
	}
	hist, err := c.Histories.FindLatestByDevice(ctx, did, taskID)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if hist.Progress != 100 {
		t.Fatalf("history progress regressed to %d, expected 100", hist.Progress)
	}
}

// TestHistoryStoreFindLatestConcurrent 并发读 + 并发 Update/Create，确保无 map race 崩溃。
func TestHistoryStoreFindLatestConcurrent(t *testing.T) {
	s := store.NewUpgradeHistoryStore()
	ctx := context.Background()
	const n = 200
	// 预创建若干 history。
	hids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		h := &model.UpgradeHistory{
			ID: idgen.NextID(), TaskID: "task", DeviceID: fmt.Sprintf("dev%d", i%5),
			StartedAt: timeutil.Now(), Status: model.UpgradeStatusPending,
		}
		if err := s.Create(ctx, h); err != nil {
			t.Fatalf("create: %v", err)
		}
		hids = append(hids, h.ID)
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = s.FindLatestByDevice(ctx, fmt.Sprintf("dev%d", idx%5), "task")
			h, err := s.Get(ctx, hids[idx])
			if err != nil {
				return
			}
			h.Progress = (idx * 7) % 101
			h.Status = model.UpgradeStatusDownloading
			_ = s.Update(ctx, h)
			// 并发 Create 新 history 触发缓存失效（写锁路径）。
			_ = s.Create(ctx, &model.UpgradeHistory{
				ID: idgen.NextID(), TaskID: "task", DeviceID: fmt.Sprintf("dev%d", idx%5),
				StartedAt: timeutil.Now(), Status: model.UpgradeStatusPending,
			})
		}(i)
	}
	wg.Wait()
}
