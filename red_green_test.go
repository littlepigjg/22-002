package firmwareupgrade_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/handler"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/fileutil"
	"firmware-upgrade/pkg/response"
	"firmware-upgrade/pkg/timeutil"
)

// md5hex 32 字母占位串，便于通过 SaveWithGuard 的长度校验。
const fakeMD5 = "0123456789abcdef0123456789abcdef"

// payload 抽取响应 JSON 的通用字段。
type payload struct {
	Success bool   `json:"success"`
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func setup(t *testing.T) (*store.Container, *service.Services, *handler.FirmwareHandler, *config.Config) {
	t.Helper()
	cfg := config.Default()
	dir := t.TempDir()
	cfg.DataDir = dir
	cfg.FirmwareDir = filepath.Join(dir, "firmwares")
	cfg.EnablePersist = false
	s := store.NewContainer()
	svcs := service.NewServices(cfg, s)
	h := handler.NewFirmwareHandler(svcs.Firmware, svcs.FileOp, cfg)
	return s, svcs, h, cfg
}

func writeFirmware(t *testing.T, st store.FirmwareStore, id, modelID, version, filePath, fileName string, size int64) {
	t.Helper()
	f := &model.Firmware{
		ID:           id,
		ModelID:      modelID,
		Version:      version,
		Name:         "fw-" + version,
		Description:  "desc",
		MD5:          fakeMD5,
		Size:         size,
		FilePath:     filePath,
		FileName:     fileName,
		Status:       model.FirmwarePublished,
		ReleaseDate:  timeutil.Now(),
		CreatedBy:    "admin",
		CreatedAt:    timeutil.Now(),
		UpdatedAt:    timeutil.Now(),
	}
	if err := st.SaveWithGuard(f, true); err != nil {
		t.Fatalf("SaveWithGuard failed: %v", err)
	}
}

func writeModel(t *testing.T, st store.DeviceModelStore, id string) {
	t.Helper()
	m := &model.DeviceModel{
		ID:        id,
		Name:      id,
		Vendor:    "v",
		Arch:      "arm64",
		MemoryMB:  256,
		FlashMB:   256,
		Enabled:   true,
		CreatedAt: timeutil.Now(),
		UpdatedAt: timeutil.Now(),
	}
	if err := st.Create(context.Background(), m); err != nil {
		t.Fatalf("create model failed: %v", err)
	}
}

// callDownload 构造 GET /firmwares/{id}/download 的 HTTP 测试请求并返回响应。
func callDownload(h *handler.FirmwareHandler, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firmwares/"+id+"/download", nil)
	ctx := handler.SetPathID(req.Context(), id)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Download(w, req)
	return w
}

// decodePayload 将响应体按通用 JSON 结构解码。
func decodePayload(t *testing.T, body io.Reader) payload {
	t.Helper()
	b, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var p payload
	if len(bytes.TrimSpace(b)) == 0 {
		return p
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("decode payload failed, body=%q: %v", string(b), err)
	}
	return p
}

func TestRedGreen(t *testing.T) {
	defer response.SetErrorClassifier(nil)

	t.Run("unit_fileutil_Size_empty_path_should_return_error", func(t *testing.T) {
		// 正常契约：Size("") 必须返回非 nil error，告诉调用方"非法路径"。
		_, err := fileutil.Size("")
		if err == nil {
			t.Errorf("fileutil.Size(\"\") error = nil, want non-nil")
		}
	})

	t.Run("unit_response_Internal_nil_err_message_non_empty", func(t *testing.T) {
		// 正常契约：response.Internal(nil) 响应 message 必须是一个非空字符串
		// （例如默认的 "internal server error"），以便客户端/日志至少定位到"有错误"。
		w := httptest.NewRecorder()
		response.Internal(w, nil)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("Internal status = %d, want 500", w.Code)
		}
		p := decodePayload(t, w.Body)
		if strings.TrimSpace(p.Message) == "" {
			t.Errorf("Internal(nil) message is empty, want non-empty fallback")
		}
	})

	t.Run("unit_response_Error_nil_err_message_non_empty", func(t *testing.T) {
		// response.Error(nil) 也应输出一个 message 非空的 5xx。
		w := httptest.NewRecorder()
		response.Error(w, nil)
		if w.Code < 500 || w.Code > 599 {
			t.Errorf("Error(nil) status = %d, want 5xx", w.Code)
		}
		p := decodePayload(t, w.Body)
		if strings.TrimSpace(p.Message) == "" {
			t.Errorf("Error(nil) message is empty, want non-empty fallback")
		}
	})

	t.Run("handler_Download_for_firmware_with_empty_FilePath", func(t *testing.T) {
		st, _, h, _ := setup(t)
		writeModel(t, st.Models, "M-1")
		writeFirmware(t, st.Firmwares, "FW-NOPATH", "M-1", "v1.0.0", "", "fw.bin", 1024)

		w := callDownload(h, "FW-NOPATH")

		// FilePath=="" 属于"文件不存在/未上传"的典型场景，合理行为：
		//   - 要么 404，message = "firmware file not found"（非空）；
		//   - 要么 5xx，message 为非空的错误描述。
		// 缺陷状态下，代码会走到 response.Internal(w, nil)，输出 500 且 message 为空。
		p := decodePayload(t, w.Body)

		if w.Code == http.StatusInternalServerError && strings.TrimSpace(p.Message) == "" {
			t.Errorf("download produced HTTP 500 with empty message (defect)")
		}

		if w.Code == http.StatusOK {
			t.Errorf("download returned HTTP 200 for empty-path firmware, want 404/5xx")
		}

		if w.Code == http.StatusNotFound {
			if strings.TrimSpace(p.Message) == "" {
				t.Errorf("download returned 404 but empty message, want message=firmware file not found")
			}
			if !strings.Contains(strings.ToLower(p.Message), "not found") &&
				!strings.Contains(strings.ToLower(p.Message), "不存在") {
				t.Errorf("download 404 message %q does not indicate file missing", p.Message)
			}
		}
	})

	t.Run("handler_Download_for_firmware_with_valid_file_matches_size", func(t *testing.T) {
		st, _, h, cfg := setup(t)
		writeModel(t, st.Models, "M-2")
		if err := os.MkdirAll(cfg.FirmwareDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		fp := filepath.Join(cfg.FirmwareDir, "good.bin")
		content := []byte("hello-firmware-payload-bytes")
		if err := os.WriteFile(fp, content, 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		writeFirmware(t, st.Firmwares, "FW-GOOD", "M-2", "v2.0.0", fp, "good.bin", int64(len(content)))

		w := callDownload(h, "FW-GOOD")

		if w.Code != http.StatusOK {
			// 如果非 200，message 必须是有意义的（非空），不能是缺陷特征"空字符串 500"。
			p := decodePayload(t, w.Body)
			if w.Code == 500 && strings.TrimSpace(p.Message) == "" {
				t.Errorf("valid firmware download produced HTTP 500 with empty message (defect)")
			} else {
				t.Fatalf("valid firmware download status = %d, want 200; body=%s", w.Code, w.Body.String())
			}
		}
	})

	t.Run("unit_fileutil_Size_missing_path_should_return_error", func(t *testing.T) {
		// 不存在文件：Size 应返回 error（而不是吞成 0,nil）。
		missing := filepath.Join(t.TempDir(), "no-such-file.bin")
		_, err := fileutil.Size(missing)
		if err == nil {
			t.Errorf("Size(<missing>) error = nil, want non-nil")
		}
	})

	t.Run("unit_fileutil_Size_dir_should_return_error", func(t *testing.T) {
		// 目录不是文件：Size 应返回 error（而不是吞成 0,nil）。
		_, err := fileutil.Size(t.TempDir())
		if err == nil {
			t.Errorf("Size(<dir>) error = nil, want non-nil")
		}
	})

	// 汇总判定：以上子测试全部通过 -> GREEN，否则 RED。
}

func TestMain(m *testing.M) {
	exitCode := m.Run()
	// 汇总输出 RED / GREEN。
	fmt.Println()
	if exitCode == 0 {
		fmt.Println("GREEN（绿灯，缺陷已修复）")
	} else {
		fmt.Println("RED（红灯，缺陷未修复）")
	}
	os.Exit(exitCode)
}

// 占位避免 time/timeutil 包未使用的告警（上面其实用了 timeutil）。
var _ = time.Second
