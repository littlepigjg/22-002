package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/service"
)

// failingReader 模拟底层磁盘异常：Read 返回一个文本为空的错误（空描述符错误）。
type failingReader struct{}

func (failingReader) Read(p []byte) (int, error) { return 0, errors.New("") }

// 上传固件走到保存文件那一步碰到底层磁盘异常（空描述符错误）：
// 经 WriteError 写响应必须非空 message、不 panic、状态 500。
func TestUploadSaveDiskAnomalyThroughWriteError(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.FirmwareDir = filepath.Join(dir, "firmware")
	cfg.FirmwareMaxSize = 10 << 20
	fop := service.NewFileOpService(cfg)

	_, err := fop.SaveReader(context.Background(), failingReader{}, "fw.bin", 1<<20)
	if err == nil {
		t.Fatalf("expected error from failing reader")
	}
	t.Logf("SaveReader err=%q (empty text? %v)", err.Error(), strings.TrimSpace(err.Error()) == "")

	w := httptest.NewRecorder()
	assertNoPanic(t, "WriteError-disk-anomaly", func() { WriteError(w, err) })
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", w.Code, w.Body.String())
	}
	assertNonEmptyMessage(t, "disk-anomaly", w)
	t.Logf("response: %s", w.Body.String())
}

// nil reader（reader is nil）经 WriteError 后非空 message。
func TestSaveReaderNilReaderThroughWriteError(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.FirmwareDir = filepath.Join(dir, "firmware")
	cfg.FirmwareMaxSize = 10 << 20
	fop := service.NewFileOpService(cfg)

	_, err := fop.SaveReader(context.Background(), nil, "fw.bin", 1<<20)
	if err == nil {
		t.Fatalf("expected error")
	}
	w := httptest.NewRecorder()
	assertNoPanic(t, "WriteError-nil-reader", func() { WriteError(w, err) })
	assertNonEmptyMessage(t, "nil-reader", w)
}

// 防止 io 未引用告警。
var _ = io.EOF
