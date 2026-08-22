package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/idgen"
	"firmware-upgrade/pkg/timeutil"
)

// seedFixtures 构建一个完整可用的服务容器，并预置型号、固件与 N 台在线设备。
func seedFixtures(t *testing.T, deviceCount int) (*Services, *model.DeviceModel, *model.Firmware, []*model.Device) {
	t.Helper()
	cfg := config.Default()
	c := store.NewContainer()
	svc := NewServices(cfg, c)
	ctx := context.Background()

	m := &model.DeviceModel{
		ID:        "GW-100",
		Name:      "Gateway 100",
		Vendor:    "Acme",
		Arch:      "arm64",
		Enabled:   true,
		CreatedAt: timeutil.Now(),
		UpdatedAt: timeutil.Now(),
	}
	if _, err := svc.Model.Create(ctx, &model.CreateModelRequest{
		ID: m.ID, Name: m.Name, Vendor: m.Vendor, Arch: m.Arch,
	}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	fw := &model.Firmware{
		ID:        idgen.NextID(),
		ModelID:   m.ID,
		Version:   "v2.0.0",
		Name:      "fw-2.0.0",
		Size:      1024,
		MD5:       "deadbeef",
		FilePath:   "/tmp/fw.bin",
		FileName:   "fw.bin",
		Status:     model.FirmwareDraft,
		CreatedAt: timeutil.Now(),
		UpdatedAt: timeutil.Now(),
	}
	if err := c.Firmwares.Create(ctx, fw); err != nil {
		t.Fatalf("create firmware: %v", err)
	}
	if err := c.Firmwares.SetStatus(ctx, fw.ID, model.FirmwarePublished); err != nil {
		t.Fatalf("publish firmware: %v", err)
	}

	devs := make([]*model.Device, 0, deviceCount)
	now := timeutil.Now()
	for i := 0; i < deviceCount; i++ {
		d := &model.Device{
			ID:             "DEV-" + itoaPad(i),
			ModelID:        m.ID,
			Name:           "dev",
			CurrentVersion: "v1.0.0",
			Status:         model.DeviceStatusOnline,
			Group:          "default",
			LastHeartbeatAt: now,
			RegisterAt:      now,
			UpdatedAt:       now,
		}
		if err := c.Devices.Create(ctx, d); err != nil {
			t.Fatalf("create device %d: %v", i, err)
		}
		devs = append(devs, d)
	}
	return svc, m, fw, devs
}

func itoaPad(i int) string {
	s := idSuffix(i)
	// 固定宽度，避免前缀碰撞。
	const width = 6
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func idSuffix(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	out := []byte{}
	for i > 0 {
		out = append([]byte{digits[i%10]}, out...)
		i /= 10
	}
	return string(out)
}

// TestCreateGrayRatioBatchConcurrent 复现「同一型号并发批量创建灰度比例任务」压测场景：
// 预热型号设备缓冲后，并发创建 40 个 gray_ratio=50 的任务，每个任务各自选设备并写入执行记录。
// 修复前在 go test -race 下会爆出 DATA RACE（scratchHits / sort.Slice / sharedStrBuf），
// 并伴随偶发 index out of range、设备分配数量异常、同一设备被重复分配等问题。
func TestCreateGrayRatioBatchConcurrent(t *testing.T) {
	const deviceCount = 200
	const taskCount = 40
	const ratio = 50

	svc, m, fw, _ := seedFixtures(t, deviceCount)
	ctx := context.Background()

	// 先预热型号设备缓冲（与压测流程一致）。
	if err := svc.Task.WarmModelBuffer(ctx, m.ID); err != nil {
		t.Fatalf("warm buffer: %v", err)
	}

	base := &model.CreateTaskRequest{
		Name:           "gray-batch",
		ModelID:        m.ID,
		TargetVersion:  fw.Version,
		FirmwareID:     fw.ID,
		Strategy:       model.StrategyGrayRatio,
		GrayRatio:      ratio,
		TimeoutSeconds: 300,
		MaxRetry:       3,
		CreatedBy:      "tester",
	}

	tasks, errs := svc.Task.CreateGrayRatioBatch(ctx, base, taskCount, ratio)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("task %d create error: %v", i, err)
		}
		if tasks[i] == nil {
			t.Fatalf("task %d is nil", i)
		}
	}

	taskIDs := make([]string, 0, len(tasks))
	for _, tk := range tasks {
		taskIDs = append(taskIDs, tk.ID)
	}

	totalByTask, dupCount, err := svc.Task.VerifyTaskAssignments(ctx, taskIDs)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	// 1) 不应有任何任务出现重复设备分配。
	for tid, dups := range dupCount {
		if dups > 0 {
			t.Errorf("task %s has %d duplicate device assignments", tid, dups)
		}
	}

	// 2) 每个任务分配的设备数应在合理区间：gray_ratio=50 时约为设备数的一半，
	//    叠加 fallback 采样与稳定哈希分布的自然波动。给出 25%~75% 的宽松区间，
	//    既容纳自然抖动，又能捕获修复前因切片损坏导致的 0 分配或异常超额分配。
	totalDevs := int64(deviceCount)
	lower := totalDevs / 4
	upper := totalDevs * 3 / 4
	for tid, n := range totalByTask {
		if n <= 0 {
			t.Errorf("task %s assigned 0 devices", tid)
		}
		if int64(n) < lower || int64(n) > upper {
			t.Errorf("task %s assigned %d devices, want in [%d,%d]", tid, n, lower, upper)
		}
	}

	// 3) 每个任务的执行记录互不冲突：同一设备在不同任务里被分配是允许的（灰度可重叠），
	//    但同一任务内不得重复（已在 dupCount 校验）。此处额外确认执行记录落库数与返回一致。
	for _, tid := range taskIDs {
		execs, err := svc.Stores.Execs.ListByTask(ctx, tid)
		if err != nil {
			t.Fatalf("list execs: %v", err)
		}
		if len(execs) != totalByTask[tid] {
			t.Errorf("task %s exec count %d != verified %d", tid, len(execs), totalByTask[tid])
		}
	}
}

// TestSelectDevicesConcurrentStress 对 GrayService.SelectDevices 做纯并发压测，
// 确保共享单例在 -race 下无 DATA RACE 且无越界。
func TestSelectDevicesConcurrentStress(t *testing.T) {
	const deviceCount = 300
	const workers = 40
	const iterations = 20

	svc, m, fw, _ := seedFixtures(t, deviceCount)
	ctx := context.Background()
	if err := svc.Task.WarmModelBuffer(ctx, m.ID); err != nil {
		t.Fatalf("warm buffer: %v", err)
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				task := &model.UpgradeTask{
					ID:        idgen.NextID(),
					ModelID:   m.ID,
					Strategy:  model.StrategyGrayRatio,
					GrayRatio: 50,
					FirmwareID: fw.ID,
				}
				hits, _, err := svc.Gray.SelectDevices(ctx, task)
				if err != nil {
					t.Errorf("SelectDevices err: %v", err)
					return
				}
				// 长度合法。
				if len(hits) < 0 || len(hits) > deviceCount {
					t.Errorf("hits len %d out of range", len(hits))
				}
				// 同一批次内部不应出现重复设备。
				seen := make(map[string]struct{}, len(hits))
				for _, d := range hits {
					if d == nil {
						t.Errorf("nil device in hits")
						return
					}
					if _, ok := seen[d.ID]; ok {
						t.Errorf("duplicate device %s within one SelectDevices call", d.ID)
						return
					}
					seen[d.ID] = struct{}{}
				}
			}
		}(w)
	}
	wg.Wait()
}

// 防止 time 未使用（兼容性占位）。
var _ = time.Second
