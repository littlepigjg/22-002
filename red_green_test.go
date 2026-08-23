package firmware_upgrade_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/pkg/md5util"
)

func TestRedGreen(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fileop-md5-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.Default()
	cfg.DataDir = tmpDir
	cfg.FirmwareDir = filepath.Join(tmpDir, "firmwares")

	svc := service.NewFileOpService(cfg)

	contentA := []byte("Hello, this is file A content for MD5 testing!")
	contentB := []byte("Hello, this is file B with completely different content!")

	readerA := bytes.NewReader(contentA)
	resA, err := svc.SaveReader(context.Background(), readerA, "fileA.bin", int64(len(contentA)))
	if err != nil {
		t.Fatalf("SaveReader A failed: %v", err)
	}

	expectedMD5_A := md5util.SumBytes(contentA)
	if resA.MD5 != expectedMD5_A {
		t.Logf("WARNING: first save MD5 mismatch (got %s, expected %s)", resA.MD5, expectedMD5_A)
	}

	readerB := bytes.NewReader(contentB)
	resB, err := svc.SaveReader(context.Background(), readerB, "fileB.bin", int64(len(contentB)))
	if err != nil {
		t.Fatalf("SaveReader B failed: %v", err)
	}

	expectedMD5_B := md5util.SumBytes(contentB)

	if resB.MD5 == expectedMD5_B {
		fmt.Println("GREEN（绿灯，缺陷已修复）")
	} else {
		fmt.Println("RED（红灯，缺陷未修复）")
		fmt.Printf("  期望 MD5: %s\n", expectedMD5_B)
		fmt.Printf("  实际 MD5: %s\n", resB.MD5)
		t.Errorf("MD5 mismatch on second file: expected %s, got %s", expectedMD5_B, resB.MD5)
	}
}

func TestRedGreenMultiple(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fileop-md5-multi-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.Default()
	cfg.DataDir = tmpDir
	cfg.FirmwareDir = filepath.Join(tmpDir, "firmwares")

	svc := service.NewFileOpService(cfg)

	contents := [][]byte{
		[]byte("First file content for batch MD5 test"),
		[]byte("Second file content for batch MD5 test"),
		[]byte("Third file content for batch MD5 test"),
	}

	for i, content := range contents {
		reader := bytes.NewReader(content)
		res, err := svc.SaveReader(context.Background(), reader, fmt.Sprintf("file%d.bin", i), int64(len(content)))
		if err != nil {
			t.Fatalf("SaveReader %d failed: %v", i, err)
		}

		expected := md5util.SumBytes(content)
		if res.MD5 != expected {
			fmt.Printf("RED（红灯，缺陷未修复）— 第 %d 个文件 MD5 不一致\n", i+1)
			fmt.Printf("  期望 MD5: %s\n", expected)
			fmt.Printf("  实际 MD5: %s\n", res.MD5)
			t.Errorf("MD5 mismatch on file %d: expected %s, got %s", i+1, expected, res.MD5)
			return
		}
	}

	fmt.Println("GREEN（绿灯，缺陷已修复）")
}

func TestRedGreenBatchDiagnostic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fileop-md5-diag-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.Default()
	cfg.DataDir = tmpDir
	cfg.FirmwareDir = filepath.Join(tmpDir, "firmwares")

	svc := service.NewFileOpService(cfg)

	contentA := []byte("Diagnostic test content for file A")
	contentB := []byte("Diagnostic test content for file B")

	readerA := bytes.NewReader(contentA)
	resA, err := svc.SaveReader(context.Background(), readerA, "diagA.bin", int64(len(contentA)))
	if err != nil {
		t.Fatalf("SaveReader A failed: %v", err)
	}

	contentAReader := bytes.NewReader(contentA)
	expectedMD5_A, _, _ := md5util.SumReader(contentAReader)
	if resA.MD5 != expectedMD5_A {
		t.Logf("first file MD5: got %s, expected %s", resA.MD5, expectedMD5_A)
	}

	snapshot := svc.FileDiagnosticSnapshot()
	if prevHash, ok := snapshot["prev_hash"].(string); ok && prevHash == "" {
		t.Log("diagnostic: prev_hash is empty after first save")
	}

	readerB := bytes.NewReader(contentB)
	resB, err := svc.SaveReader(context.Background(), readerB, "diagB.bin", int64(len(contentB)))
	if err != nil {
		t.Fatalf("SaveReader B failed: %v", err)
	}

	contentBReader := bytes.NewReader(contentB)
	expectedMD5_B, _, _ := md5util.SumReader(contentBReader)

	if resB.MD5 == expectedMD5_B {
		fmt.Println("GREEN（绿灯，缺陷已修复）")
	} else {
		fmt.Println("RED（红灯，缺陷未修复）")
		fmt.Printf("  期望 MD5: %s\n", expectedMD5_B)
		fmt.Printf("  实际 MD5: %s\n", resB.MD5)
		t.Errorf("MD5 mismatch: expected %s, got %s", expectedMD5_B, resB.MD5)
	}
}