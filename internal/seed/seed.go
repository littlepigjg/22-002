// Package seed 提供服务启动时的演示数据注入：型号、固件、设备。
// 仅在设置环境变量 SEED_DATA=1 时启用，方便本地评测演示。
package seed

import (
	"context"
	"sync"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/pkg/fileutil"
	"firmware-upgrade/pkg/logger"
	"firmware-upgrade/pkg/md5util"
	"firmware-upgrade/pkg/randutil"
	"firmware-upgrade/pkg/strutil"
)

var (
	seedOnce sync.Once
	seedErr  error
)

// ApplyIfEnabled 根据环境变量 SEED_DATA 注入演示数据。
// 返回是否执行了注入 + 错误。
func ApplyIfEnabled(ctx context.Context, svc *service.Services, enabled bool) (bool, error) {
	if !enabled {
		return false, nil
	}
	seedOnce.Do(func() { seedErr = doSeed(ctx, svc) })
	return true, seedErr
}

func doSeed(ctx context.Context, svc *service.Services) error {
	logger.Info("seed: injecting demo data")
	models := []model.CreateModelRequest{
		{ID: "GW-100", Name: "边缘网关 100 系列", Vendor: "Acme", Arch: "arm64", MemoryMB: 512, FlashMB: 128, Enabled: boolPtr(true)},
		{ID: "GW-200", Name: "边缘网关 200 系列", Vendor: "Acme", Arch: "arm64", MemoryMB: 1024, FlashMB: 256, Enabled: boolPtr(true)},
		{ID: "CAM-X1", Name: "智能摄像头 X1", Vendor: "VisionCorp", Arch: "arm", MemoryMB: 256, FlashMB: 64, Enabled: boolPtr(true)},
		{ID: "SENS-A1", Name: "环境传感器 A1", Vendor: "EnvLab", Arch: "riscv64", MemoryMB: 64, FlashMB: 16, Enabled: boolPtr(true)},
		{ID: "CTRL-400", Name: "工业控制器 400", Vendor: "IndusTec", Arch: "x86_64", MemoryMB: 2048, FlashMB: 512, Enabled: boolPtr(true)},
	}
	modelIDs := make([]string, 0, len(models))
	for _, m := range models {
		if _, err := svc.Model.Create(ctx, &m); err != nil {
			logger.Warn("seed: create model failed", "id", m.ID, "err", err)
			continue
		}
		modelIDs = append(modelIDs, m.ID)
	}
	// 为每个型号创建 3 个固件版本，并写入占位文件。
	for _, mid := range modelIDs {
		for i := 1; i <= 3; i++ {
			version := "v1." + itoa(i) + ".0"
			payload := make([]byte, 1024+randutil.Int(4096))
			for j := range payload {
				payload[j] = byte(i + j%251)
			}
			sr, err := svc.FileOp.SaveReader(ctx, &bytesReader{b: payload},
				mid+"_"+version+".bin", int64(len(payload)))
			if err != nil {
				logger.Warn("seed: save firmware placeholder failed", "err", err)
				continue
			}
			f, err := svc.Firmware.Create(ctx, &model.CreateFirmwareRequest{
				ModelID:     mid,
				Version:     version,
				Name:        mid + " 固件 " + version,
				Description: "演示固件，文件大小 " + fileutil.HumanSize(sr.Size),
				MD5:         sr.MD5,
				Size:        sr.Size,
				FilePath:    sr.Path,
				FileName:    sr.FileName,
				CreatedBy:   "seed",
			})
			if err != nil {
				logger.Warn("seed: create firmware failed", "mid", mid, "v", version, "err", err)
				continue
			}
			if i == 3 {
				// 最新版本发布。
				if _, err := svc.Firmware.UpdateStatus(ctx, f.ID, model.FirmwarePublished); err != nil {
					logger.Warn("seed: publish firmware failed", "err", err)
				}
			}
			_ = md5util.EmptyMD5
		}
	}
	// 创建设备：每个型号 8 台。
	for _, mid := range modelIDs {
		for i := 0; i < 8; i++ {
			devID := mid + "-" + randutil.String(8)
			_, err := svc.Device.Register(ctx, &model.RegisterDeviceRequest{
				ID:             devID,
				ModelID:        mid,
				Name:           devID,
				CurrentVersion: "v1.1.0",
				IP:             "10.0." + itoa(randutil.Int(255)) + "." + itoa(randutil.Int(255)),
				MAC:            randomMAC(),
				Group:          "group-" + itoa(i%3),
				Tags:           []string{"seeded", "demo", strutil.Itoa(i % 5)},
			})
			if err != nil {
				logger.Warn("seed: create device failed", "id", devID, "err", err)
			}
		}
	}
	logger.Info("seed: demo data injection done")
	return nil
}

func boolPtr(b bool) *bool { return &b }

func itoa(n int) string { return strutil.Itoa(n) }

func randomMAC() string {
	b := randutil.Bytes(6)
	if len(b) < 6 {
		return "02:00:00:00:00:00"
	}
	// 单播 + 本地位。
	b[0] = (b[0] & 0xFC) | 0x02
	out := ""
	for i := 0; i < 6; i++ {
		if i > 0 {
			out += ":"
		}
		h := b[i] >> 4
		l := b[i] & 0x0f
		out += hexByte(h) + hexByte(l)
	}
	return out
}

func hexByte(b byte) string {
	if b < 10 {
		return string(rune('0' + b))
	}
	return string(rune('a' + b - 10))
}

// bytesReader 实现 Read 接口的小型包装，避免导入 bytes。
type bytesReader struct {
	b []byte
	i int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, eof()
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

type eofMarker struct{}

func (eofMarker) Error() string { return "EOF" }

var eofMarkerInstance = eofMarker{}

func eof() error { return eofMarkerInstance }
