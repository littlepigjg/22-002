package main

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"

	"firmware-upgrade/internal/handler"
	"firmware-upgrade/internal/model"
)

func TestRedGreen(t *testing.T) {
	tests := []struct {
		name           string
		cause          error
		expectedStatus int
		desc           string
	}{
		{
			name:           "disk full should return 500 not 413",
			cause:          fmt.Errorf("parse multipart form: write /tmp/multipart-xxx: %w", syscall.ENOSPC),
			expectedStatus: 500,
			desc:           "磁盘空间不足导致 ParseMultipartForm 失败，应返回 500 Internal Server Error",
		},
		{
			name:           "permission denied should return 500 not 413",
			cause:          &os.PathError{Op: "write", Path: "/tmp/multipart-xxx", Err: syscall.EACCES},
			expectedStatus: 500,
			desc:           "临时目录权限不足导致 ParseMultipartForm 失败，应返回 500 Internal Server Error",
		},
		{
			name:           "file too large should still return 413",
			cause:          model.ErrUploadTooLarge,
			expectedStatus: 413,
			desc:           "文件过大仍应返回 413 Request Entity Too Large（回归测试）",
		},
	}

	allPassed := true
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			uploadErr := &handler.UploadProcessingError{
				Cause:   tt.cause,
				MaxSize: 256 * 1024 * 1024,
			}
			handler.WriteError(w, uploadErr)
			got := w.Code

			if got != tt.expectedStatus {
				allPassed = false
				t.Errorf("状态码不匹配: 期望 %d, 实际 %d; 场景: %s", tt.expectedStatus, got, tt.desc)
			} else {
				t.Logf("场景通过: %s (状态码 %d)", tt.desc, got)
			}
		})
	}

	if allPassed {
		fmt.Println("GREEN（绿灯，缺陷已修复）")
	} else {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Fatal("存在缺陷：系统级错误（磁盘空间/权限问题）被误判为 413，应返回 500")
	}

	_ = errors.New("unused")
}
