package firmware_upgrade_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/service"
)

func TestRedGreen(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "firmware-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	firmwareDir := filepath.Join(tmpDir, "firmwares")
	if err := os.MkdirAll(firmwareDir, 0o755); err != nil {
		t.Fatalf("failed to create firmware dir: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = tmpDir
	cfg.FirmwareDir = firmwareDir

	fop := service.NewFileOpService(cfg)

	ctx := context.Background()
	content := []byte("test firmware content for permission check")
	reader := bytes.NewReader(content)

	err = os.Chmod(firmwareDir, 0o555)
	if err != nil {
		t.Skipf("cannot set permission 555, skipping: %v", err)
		return
	}
	defer os.Chmod(firmwareDir, 0o755)

	_, err = fop.SaveReader(ctx, reader, "test.bin", int64(len(content)))
	if err == nil {
		t.Errorf("expected permission error but got nil")
		fmt.Println("RED（红灯，缺陷未修复）: expected permission error but got nil")
		return
	}

	var pathErr *os.PathError
	isPathError := errors.As(err, &pathErr)
	rootErr := err
	if isPathError && pathErr != nil {
		rootErr = pathErr.Err
	}

	isPermission := errors.Is(err, os.ErrPermission)
	isRootPermission := errors.Is(rootErr, os.ErrPermission)

	if isPermission || isRootPermission {
		fmt.Println("GREEN（绿灯，缺陷已修复）: errors.Is can detect permission error")
	} else {
		t.Errorf("errors.Is(err, os.ErrPermission) returns false, error chain is broken")
		fmt.Println("RED（红灯，缺陷未修复）: errors.Is cannot detect permission error")
	}

	errMsg := err.Error()
	hasPermissionKeyword := containsWord(errMsg, "permission")
	if !hasPermissionKeyword {
		fmt.Println("RED（红灯，缺陷未修复）: error message does not contain 'permission' keyword")
	}

	t.Logf("Error: %v", err)
	t.Logf("errors.Is(err, os.ErrPermission): %v", isPermission)
	t.Logf("errors.Is(rootErr, os.ErrPermission): %v", isRootPermission)
	t.Logf("Error message contains 'permission': %v", hasPermissionKeyword)
}

func containsWord(s, word string) bool {
	lower := toLower(s)
	lowerWord := toLower(word)
	for i := 0; i <= len(lower)-len(lowerWord); i++ {
		if lower[i:i+len(lowerWord)] == lowerWord {
			before := byte(' ')
			after := byte(' ')
			if i > 0 {
				before = lower[i-1]
			}
			if i+len(lowerWord) < len(lower) {
				after = lower[i+len(lowerWord)]
			}
			if isWordChar(before) || isWordChar(after) {
				continue
			}
			return true
		}
	}
	return false
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
