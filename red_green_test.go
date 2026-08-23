package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"firmware-upgrade/pkg/fileutil"
)

func TestRedGreen(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-red-green-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sep := string(filepath.Separator)
	if strings.HasSuffix(tmpDir, sep) {
		tmpDir = strings.TrimRight(tmpDir, sep)
	}
	baseDir := tmpDir

	if strings.HasSuffix(baseDir, sep) {
		t.Fatal("failed to create test base dir without trailing separator")
	}
	fmt.Println("INFO: baseDir without trailing separator:", baseDir)

	t.Run("SafeJoin should reject traversal when baseDir no trailing sep", func(t *testing.T) {
		name := "fw-extra" + sep + ".." + sep + ".." + sep + "etc" + sep + "passwd"

		_, err := fileutil.SafeJoin(baseDir, name)
		if err == nil {
			fmt.Println("FAIL (RED): SafeJoin allowed path traversal when baseDir has no trailing separator")
			t.Errorf("expected error for path traversal, got nil")
		} else {
			fmt.Println("PASS (GREEN): SafeJoin correctly rejected path traversal:", err)
		}
	})

	t.Run("SafeJoin should work with normal file when baseDir no trailing sep", func(t *testing.T) {
		name := "normal-file.bin"

		result, err := fileutil.SafeJoin(baseDir, name)
		if err != nil {
			fmt.Println("FAIL (RED): SafeJoin rejected normal file when baseDir has no trailing separator")
			t.Errorf("unexpected error: %v", err)
		} else {
			fmt.Println("PASS (GREEN): SafeJoin accepted normal file:", result)
		}
	})

	t.Run("SafeJoin should reject traversal with pre-cleaned path", func(t *testing.T) {
		name := filepath.Clean("fw-extra/sub/../../../etc/passwd")

		if !strings.Contains(name, "..") {
			t.Skip("cleaned path does not contain '..', skipping")
			return
		}

		_, err := fileutil.SafeJoin(baseDir, name)
		if err == nil {
			fmt.Println("FAIL (RED): SafeJoin allowed traversal with pre-cleaned path")
			t.Errorf("expected error for path traversal, got nil")
		} else {
			fmt.Println("PASS (GREEN): SafeJoin correctly rejected pre-cleaned traversal:", err)
		}
	})

	t.Run("JoinWithFallback should handle no-trailing-sep correctly", func(t *testing.T) {
		name := "fw-extra" + sep + ".." + sep + ".." + sep + "etc" + sep + "passwd"

		result, err := fileutil.JoinWithFallback(baseDir, name, false)
		if err != nil {
			fmt.Println("PASS (GREEN): JoinWithFallback correctly rejected traversal")
		} else {
			fmt.Println("FAIL (RED): JoinWithFallback allowed path traversal, got:", result)
			t.Errorf("expected error for path traversal")
		}
	})

	t.Run("SafeJoinFlex should reject traversal", func(t *testing.T) {
		name := "fw-extra" + sep + ".." + sep + ".." + sep + "etc" + sep + "passwd"

		_, err := fileutil.SafeJoinFlex(baseDir, name)
		if err == nil {
			fmt.Println("FAIL (RED): SafeJoinFlex allowed path traversal")
			t.Errorf("expected error for path traversal")
		} else {
			fmt.Println("PASS (GREEN): SafeJoinFlex correctly rejected traversal")
		}
	})

	t.Run("SafeJoinDir should enforce trailing sep", func(t *testing.T) {
		name := "fw-extra/file.bin"

		_, err := fileutil.SafeJoinDir(baseDir, name)
		if err == nil {
			fmt.Println("FAIL (RED): SafeJoinDir should require trailing separator")
			t.Errorf("expected error about trailing separator")
		} else {
			fmt.Println("PASS (GREEN): SafeJoinDir correctly rejected missing trailing sep:", err)
		}
	})

	fmt.Println("\n=== Test Summary ===")
	if t.Failed() {
		fmt.Println("RESULT: RED (bug exists - some tests failed)")
	} else {
		fmt.Println("RESULT: GREEN (bug fixed - all tests passed)")
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}
