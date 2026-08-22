package firmware_upgrade

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/idgen"
	"firmware-upgrade/pkg/md5util"
)

func buildTestServices(t *testing.T) *service.Services {
	t.Helper()
	cfg := config.Default()
	st := store.NewContainer()
	return service.NewServices(cfg, st)
}

func seedTestData(ctx context.Context, svc *service.Services, modelID string, deviceCount int, targetVersion string) (string, error) {
	enabled := true
	if err := svc.Stores.Models.Create(ctx, &model.DeviceModel{
		ID:        modelID,
		Name:      "Test Model " + modelID,
		Vendor:    "TestVendor",
		Arch:      "arm64",
		MemoryMB:  512,
		FlashMB:   128,
		Enabled:   enabled,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}); err != nil {
		return "", err
	}
	fwID := idgen.NextID()
	payload := make([]byte, 2048)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	md := md5util.SumBytes(payload)
	fw := &model.Firmware{
		ID:          fwID,
		ModelID:     modelID,
		Version:     targetVersion,
		Name:        "Test Firmware " + targetVersion,
		Description: "test placeholder",
		MD5:         md,
		Size:        int64(len(payload)),
		FilePath:    "/tmp/placeholder_" + fwID + ".bin",
		FileName:    modelID + "_" + targetVersion + ".bin",
		Status:      model.FirmwarePublished,
		ReleaseDate: time.Now(),
		CreatedBy:   "test",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := svc.Stores.Firmwares.Create(ctx, fw); err != nil {
		return "", err
	}
	sourceVersion := "v1.1.0"
	for i := 0; i < deviceCount; i++ {
		devID := fmt.Sprintf("%s-DEV%05d", modelID, i)
		d := &model.Device{
			ID:              devID,
			ModelID:         modelID,
			Name:            devID,
			CurrentVersion:  sourceVersion,
			TargetVersion:   "",
			Status:          model.DeviceStatusOnline,
			IP:              fmt.Sprintf("10.0.%d.%d", i/254, (i%254)+1),
			MAC:             fmt.Sprintf("02:00:00:%02x:%02x:%02x", i, i+1, i+2),
			Group:           fmt.Sprintf("group-%d", i%3),
			Tags:            []string{"tagged"},
			LastHeartbeatAt: time.Now(),
			RegisterAt:      time.Now(),
			UpdatedAt:       time.Now(),
		}
		if err := svc.Stores.Devices.Create(ctx, d); err != nil {
			return "", err
		}
	}
	return fwID, nil
}

func syncCreateGrayBatch(ctx context.Context, svc *service.Services, baseReq *model.CreateTaskRequest, n int, ratio int) ([]*model.UpgradeTask, []error) {
	if baseReq == nil || n <= 0 {
		return nil, nil
	}
	results := make([]*model.UpgradeTask, n)
	errs := make([]error, n)
	if err := svc.Task.WarmModelBuffer(ctx, baseReq.ModelID); err != nil {
		for i := range errs {
			errs[i] = err
		}
		return results, errs
	}
	reqs := make([]*model.CreateTaskRequest, n)
	for i := 0; i < n; i++ {
		cp := *baseReq
		cp.Strategy = model.StrategyGrayRatio
		cp.GrayRatio = ratio
		cp.Name = baseReq.Name + "-" + fmt.Sprintf("%d", i)
		reqs[i] = &cp
	}
	startGate := make(chan struct{})
	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(n)
	done.Add(n)
	for i, r := range reqs {
		go func(idx int, req *model.CreateTaskRequest) {
			defer done.Done()
			ready.Done()
			<-startGate
			t, e := svc.Task.Create(ctx, req)
			results[idx] = t
			errs[idx] = e
		}(i, r)
	}
	ready.Wait()
	close(startGate)
	done.Wait()
	return results, errs
}

func TestRedGreen(t *testing.T) {
	green := true
	var resultMessage string
	var failedOnce bool
	defer func() {
		if r := recover(); r != nil {
			green = false
			resultMessage = fmt.Sprintf("RED（红灯，缺陷未修复） - panic recovered: %v", r)
		}
		if t.Failed() {
			green = false
		}
		if !green {
			if resultMessage == "" {
				resultMessage = "RED（红灯，缺陷未修复）"
			}
			fmt.Println(resultMessage)
			if !failedOnce && !t.Failed() {
				t.Errorf("%s", resultMessage)
			}
		} else {
			resultMessage = "GREEN（绿灯，缺陷已修复）"
			fmt.Println(resultMessage)
		}
	}()

	ctx := context.Background()
	modelID := "GW-CONCUR-BUG"
	deviceCount := 200
	targetVersion := "v1.3.0"
	batchCount := 50
	ratio := 50

	rounds := 8
	totalDupTasks := 0
	totalZeroCountTasks := 0
	totalOverflowTasks := 0
	anyPanic := false
	taskCreationErrCount := 0

	for r := 0; r < rounds; r++ {
		svc := buildTestServices(t)
		fwID, err := seedTestData(ctx, svc, modelID, deviceCount, targetVersion)
		if err != nil {
			green = false
			failedOnce = true
			resultMessage = fmt.Sprintf("RED（红灯，缺陷未修复） - seed failed round %d: %v", r, err)
			t.Fatalf("seed data failed: %v", err)
			return
		}
		baseReq := &model.CreateTaskRequest{
			Name:           fmt.Sprintf("round-%d-gray", r),
			ModelID:        modelID,
			TargetVersion:  targetVersion,
			FirmwareID:     fwID,
			Strategy:       model.StrategyGrayRatio,
			GrayRatio:      ratio,
			DeviceIDs:      nil,
			GroupFilter:    nil,
			ScheduleAt:     0,
			TimeoutSeconds: 3600,
			MaxRetry:       1,
			Description:    fmt.Sprintf("round-%d concurrency defect validation", r),
			CreatedBy:      "test-runner",
		}
		var results []*model.UpgradeTask
		var errs []error
		roundPanicked := false
		func() {
			defer func() {
				if pr := recover(); pr != nil {
					roundPanicked = true
					_ = pr
				}
			}()
			results, errs = syncCreateGrayBatch(ctx, svc, baseReq, batchCount, ratio)
		}()
		if roundPanicked {
			anyPanic = true
			green = false
			continue
		}
		ids := make([]string, 0, batchCount)
		for idx, tt := range results {
			if errs[idx] != nil {
				taskCreationErrCount++
				continue
			}
			if tt == nil {
				taskCreationErrCount++
				continue
			}
			ids = append(ids, tt.ID)
		}
		totals, dups, vErr := svc.Task.VerifyTaskAssignments(ctx, ids)
		if vErr != nil {
			continue
		}
		for _, tid := range ids {
			tot := totals[tid]
			dup := dups[tid]
			if dup > 0 {
				totalDupTasks++
				green = false
			}
			if tot == 0 {
				totalZeroCountTasks++
				green = false
			}
			if tot > deviceCount {
				totalOverflowTasks++
				green = false
			}
		}
	}

	if anyPanic {
		failedOnce = true
		resultMessage = "RED（红灯，缺陷未修复） - concurrent batch creation panicked (round recovered panic)"
		t.Errorf("concurrent creation panic")
		return
	}
	if !green {
		failedOnce = true
		details := fmt.Sprintf("dup_tasks=%d zero_tasks=%d overflow_tasks=%d create_errs=%d rounds_ran=%d",
			totalDupTasks, totalZeroCountTasks, totalOverflowTasks, taskCreationErrCount, rounds)
		resultMessage = "RED（红灯，缺陷未修复） - " + details
		t.Errorf("defect symptoms: %s", details)
		return
	}
}
