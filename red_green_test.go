package firmware_upgrade

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/timeutil"
)

func buildFixture() (*service.ProgressService, *service.HistoryService, *service.StatsService, []string, string) {
	cfg := config.Default()
	c := store.NewContainer()
	ctx := context.Background()
	_ = c.Models.Create(ctx, &model.DeviceModel{
		ID:        "GW-100",
		Name:      "Gateway-100",
		Vendor:    "V",
		Arch:      "arm64",
		MemoryMB:  256,
		FlashMB:   512,
		Enabled:   true,
		CreatedAt: timeutil.Now(),
		UpdatedAt: timeutil.Now(),
	})
	_ = c.Firmwares.Create(ctx, &model.Firmware{
		ID:          "FW-1",
		ModelID:     "GW-100",
		Version:     "v2.0.0",
		Name:        "FW v2.0.0",
		MD5:         "deadbeef",
		Size:        1024,
		Status:      model.FirmwarePublished,
		ReleaseDate: timeutil.Now(),
		CreatedAt:   timeutil.Now(),
		UpdatedAt:   timeutil.Now(),
	})
	devices := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("dev-%03d", i)
		_ = c.Devices.Create(ctx, &model.Device{
			ID:             id,
			ModelID:        "GW-100",
			Name:           id,
			CurrentVersion: "v1.0.0",
			Status:         model.DeviceStatusOnline,
			RegisterAt:     timeutil.Now(),
			UpdatedAt:      timeutil.Now(),
		})
		devices = append(devices, id)
	}
	taskID := "TASK-001"
	_ = c.Tasks.Create(ctx, &model.UpgradeTask{
		ID:             taskID,
		Name:           "Daily Upgrade",
		ModelID:        "GW-100",
		FromVersion:    "v1.0.0",
		TargetVersion:  "v2.0.0",
		FirmwareID:     "FW-1",
		Strategy:       model.StrategyFull,
		DeviceIDs:      devices,
		Status:         model.TaskStatusRunning,
		ScheduleAt:     timeutil.Now(),
		StartTime:      timeutil.Now(),
		TimeoutSeconds: 600,
		MaxRetry:       3,
		CreatedAt:      timeutil.Now(),
		UpdatedAt:      timeutil.Now(),
	})
	for _, id := range devices {
		_ = c.Execs.Upsert(ctx, &model.TaskDeviceExecution{
			TaskID:     taskID,
			DeviceID:   id,
			Status:     model.UpgradeStatusPending,
			Progress:   0,
			AssignedAt: timeutil.Now(),
		})
		_ = c.Histories.Create(ctx, &model.UpgradeHistory{
			ID:          taskID + ":" + id,
			TaskID:      taskID,
			DeviceID:    id,
			ModelID:     "GW-100",
			FromVersion: "v1.0.0",
			ToVersion:   "v2.0.0",
			FirmwareID:  "FW-1",
			Status:      model.UpgradeStatusPending,
			StartedAt:   timeutil.Now(),
		})
	}
	history := service.NewHistoryService(c.Histories)
	stats := service.NewStatsService(c, history, cfg)
	progress := service.NewProgressService(c.Execs, c.Tasks, c.Devices, c.Histories, stats, cfg)
	progress.BindHistory(history)
	return progress, history, stats, devices, taskID
}

func TestSubConcurrentRace(t *testing.T) {
	if os.Getenv("GO_RG_SUB") != "1" {
		t.Skip("skip sub, main driver runs it")
		return
	}
	progress, history, stats, devices, taskID := buildFixture()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var panicCount int64
	var mu sync.Mutex
	errs := []string{}

	addErr := func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		if len(errs) < 50 {
			errs = append(errs, msg)
		}
	}

	const goroutines = 40
	half := goroutines / 2
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < half; i++ {
		go func(idx int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&panicCount, 1)
					addErr(fmt.Sprintf("writer panic: %v", r))
				}
			}()
			for k := 0; k < 50; k++ {
				if ctx.Err() != nil {
					return
				}
				devIdx := (idx*5 + k) % len(devices)
				dev := devices[devIdx]
				req := &model.ReportProgressRequest{
					TaskID:        taskID,
					DeviceID:      dev,
					Progress:      100,
					Status:        model.UpgradeStatusSuccess,
					DownloadSpeed: 1024 * int64(k+1),
					MD5Verified:   true,
				}
				_, _ = progress.Report(ctx, req)
				req2 := &model.ReportProgressRequest{
					TaskID:        taskID,
					DeviceID:      dev,
					Progress:      100,
					Status:        model.UpgradeStatusFailed,
					ErrorMessage:  "simulated race",
					DownloadSpeed: 1024 * int64(k+1),
					MD5Verified:   true,
				}
				_, _ = progress.Report(ctx, req2)
			}
		}(i)
	}

	for i := 0; i < half; i++ {
		go func(idx int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&panicCount, 1)
					addErr(fmt.Sprintf("reader panic: %v", r))
				}
			}()
			for k := 0; k < 50; k++ {
				if ctx.Err() != nil {
					return
				}
				devIdx := (idx*7 + k) % len(devices)
				dev := devices[devIdx]
				_, _ = history.FindLatestByDevice(ctx, dev, taskID)
				_, _ = history.CountDaily(ctx, 14)
				_, _ = stats.Get(ctx)
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(25 * time.Second):
	}

	panics := atomic.LoadInt64(&panicCount)
	if panics > 0 {
		t.Errorf("observed panics=%d details=%v", panics, errs)
	}
}

func TestRedGreen(t *testing.T) {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = os.Args[0]
	}
	args := []string{"-test.v", "-test.run", "TestSubConcurrentRace", "-test.count=1", "-test.timeout=60s"}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "GO_RG_SUB=1")
	out, runErr := cmd.CombinedOutput()
	sout := string(out)
	exitCode := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	raceDetected := strings.Contains(sout, "DATA RACE") || strings.Contains(sout, "WARNING: DATA RACE") ||
		strings.Contains(sout, "concurrent map read and map write") || strings.Contains(sout, "concurrent map iteration and map write") ||
		strings.Contains(sout, "fatal error: concurrent map")
	panicSeen := strings.Contains(sout, "panic:") || strings.Contains(sout, "observed panics=")
	failed := exitCode != 0 || raceDetected || panicSeen
	_ = failed
	if raceDetected || panicSeen || exitCode != 0 {
		fmt.Printf("RED（红灯，缺陷未修复） exitCode=%d race=%v panic=%v\n", exitCode, raceDetected, panicSeen)
		if len(sout) > 512 {
			sout = sout[:512] + "..."
		}
		fmt.Printf("关键输出片段: %s\n", sout)
		t.Errorf("RED（红灯，缺陷未修复） exitCode=%d raceDetected=%v panicSeen=%v", exitCode, raceDetected, panicSeen)
		return
	}
	fmt.Println("GREEN（绿灯，缺陷已修复）")
}
