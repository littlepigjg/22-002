package main

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/service"
)

func TestRedGreen(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fw-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.Default()
	cfg.DataDir = tmpDir
	cfg.FirmwareDir = filepath.Join(tmpDir, "firmwares")

	fop := service.NewFileOpService(cfg)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", "test-firmware.bin")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	content := []byte("test firmware content for leak detection with enough data to ensure temp file creation")
	if _, err := part.Write(content); err != nil {
		t.Fatalf("failed to write to part: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close multipart writer: %v", err)
	}

	reader := multipart.NewReader(&buf, writer.Boundary())
	form, err := reader.ReadForm(0)
	if err != nil {
		t.Fatalf("failed to read multipart form: %v", err)
	}
	defer form.RemoveAll()

	fileHeaders := form.File["file"]
	if len(fileHeaders) == 0 {
		t.Fatal("no file headers found in form")
	}

	fh := fileHeaders[0]
	initialCount := fop.GetOpenFileCount()

	fop.SetForceFail(5)
	for i := 0; i < 5; i++ {
		_, err := fop.SaveMultipartFile(nil, fh, "test-firmware.bin")
		if err == nil {
			t.Logf("warning: SaveMultipartFile returned nil error on iteration %d", i)
		}
	}

	finalCount := fop.GetOpenFileCount()
	t.Logf("Initial open file count: %d, Final open file count: %d", initialCount, finalCount)

	if finalCount > initialCount {
		t.Log("RED (红灯，缺陷未修复)：文件句柄泄漏检测到")
		fmt.Println("RESULT: RED")
		os.Exit(1)
	} else {
		t.Log("GREEN (绿灯，缺陷已修复)：无文件句柄泄漏")
		fmt.Println("RESULT: GREEN")
		os.Exit(0)
	}
}
