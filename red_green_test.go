package firmwareupgrade_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
)

const (
	numDevices   = 50
	concurrency  = 50
	itersPerGor  = 20
	modelID      = "GW-TEST-001"
	modelIDAlt   = "GW-TEST-002"
)

func buildService() (*service.DeviceService, store.DeviceModelStore) {
	ds := store.NewDeviceStore()
	ms := store.NewDeviceModelStore()
	_ = ms.Create(context.Background(), &model.DeviceModel{
		ID:        modelID,
		Name:      "Test Gateway A",
		Vendor:    "acme",
		Arch:      "arm64",
		MemoryMB:  256,
		FlashMB:   128,
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	_ = ms.Create(context.Background(), &model.DeviceModel{
		ID:        modelIDAlt,
		Name:      "Test Gateway B",
		Vendor:    "acme",
		Arch:      "x86_64",
		MemoryMB:  512,
		FlashMB:   256,
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	return service.NewDeviceService(ds, ms), ms
}

func TestRedGreen(t *testing.T) {
	svc, _ := buildService()
	ctx := context.Background()

	deviceIDs := make([]string, numDevices)
	for i := 0; i < numDevices; i++ {
		deviceIDs[i] = fmt.Sprintf("SN-DEVICE-%04d", i+1)
	}

	for i := 0; i < numDevices/2; i++ {
		_, err := svc.Register(ctx, &model.RegisterDeviceRequest{
			ID:             deviceIDs[i],
			ModelID:        modelID,
			Name:           fmt.Sprintf("Device-%d", i),
			CurrentVersion: "v1.0.0",
			IP:             fmt.Sprintf("10.0.0.%d", i+1),
			Group:          "grp-a",
			Tags:           []string{"alpha", "v1"},
		})
		if err != nil {
			t.Fatalf("pre-register %s failed: %v", deviceIDs[i], err)
		}
	}

	var panicCount int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	registeredIdx := int32(numDevices/2 - 1)

	for g := 0; g < concurrency; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&panicCount, 1)
				}
			}()
			<-start
			for it := 0; it < itersPerGor; it++ {
				kind := (gid + it) % 5
				switch kind {
				case 0:
					idx := int(atomic.AddInt32(&registeredIdx, 1))
					if idx < numDevices {
						mid := modelID
						if idx%2 == 1 {
							mid = modelIDAlt
						}
						_, _ = svc.Register(ctx, &model.RegisterDeviceRequest{
							ID:             deviceIDs[idx],
							ModelID:        mid,
							Name:           fmt.Sprintf("Dev-%d", idx),
							CurrentVersion: fmt.Sprintf("v1.%d.%d", gid%3, it),
							IP:             fmt.Sprintf("192.168.1.%d", (idx%250)+1),
							Group:          fmt.Sprintf("grp-%c", 'a'+idx%4),
							Tags:           []string{fmt.Sprintf("tag-%d", idx%7)},
						})
					}
				case 1:
					target := deviceIDs[(gid*3+it)%(numDevices/2)]
					st := model.DeviceStatusOnline
					if (gid+it)%3 == 0 {
						st = model.DeviceStatusOffline
					} else if (gid+it)%5 == 0 {
						st = model.DeviceStatusUnknown
					}
					_ = svc.Heartbeat(ctx, &model.HeartbeatRequest{
						ID:             target,
						CurrentVersion: fmt.Sprintf("v1.%d.%d-hb", gid%4, it),
						Status:         st,
						IP:             fmt.Sprintf("172.16.%d.%d", gid%16, it%200+1),
						FreeSpaceMB:    1024,
						CPUPercent:     (gid*7 + it*3) % 100,
						MemoryPercent:  (gid*11 + it*5) % 100,
					})
				case 2:
					target := deviceIDs[(gid+it)%(numDevices/2)]
					_, _ = svc.Get(ctx, target)
				case 3:
					_, _, _ = svc.List(ctx, &model.ListDeviceRequest{
						Keyword:       fmt.Sprintf("SN-DEVICE-%02d", (gid+it)%10),
						ModelID:       modelID,
						Status:        "online",
						PageNum:       1,
						PageSize:      100,
					})
				case 4:
					_, _, _, _ = svc.CountStatus(ctx)
					_, _ = svc.CountByModel(ctx)
					_, _ = svc.CountByVersion(ctx)
					_, _ = svc.Total(ctx)
				}
			}
		}(g)
	}

	close(start)
	wg.Wait()

	if panicCount > 0 {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Fatalf("concurrent panic count = %d", panicCount)
	}

	total, terr := svc.Total(ctx)
	if terr != nil {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Fatalf("total failed: %v", terr)
	}
	online, offline, unknown, cerr := svc.CountStatus(ctx)
	if cerr != nil {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Fatalf("count status failed: %v", cerr)
	}
	if total != online+offline+unknown {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Fatalf("count mismatch: total=%d online+offline+unknown=%d (data race on status bucket/byID)",
			total, online+offline+unknown)
	}

	modelMap, merr := svc.CountByModel(ctx)
	if merr != nil {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Fatalf("count by model failed: %v", merr)
	}
	modelSum := int64(0)
	for _, c := range modelMap {
		modelSum += c
	}
	if modelSum != total {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Fatalf("count by model sum mismatch: modelSum=%d total=%d (data race on modelIndex/byID concurrent write)",
			modelSum, total)
	}

	list, _, lerr := svc.List(ctx, &model.ListDeviceRequest{PageNum: 1, PageSize: 1000})
	if lerr != nil {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Fatalf("list failed: %v", lerr)
	}
	uniq := make(map[string]struct{}, len(list))
	for _, d := range list {
		if d == nil || d.ID == "" {
			fmt.Println("RED（红灯，缺陷未修复）")
			t.Fatalf("list returned nil/empty device (partial write data race)")
		}
		uniq[d.ID] = struct{}{}
	}
	if total > 0 && int64(len(uniq)) != total {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Fatalf("list unique count mismatch: unique=%d total=%d (modelIndex/byID torn concurrent read)",
			len(uniq), total)
	}

	fmt.Println("GREEN（绿灯，缺陷已修复）")
}
