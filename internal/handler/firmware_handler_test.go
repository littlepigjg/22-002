// Package handler 上传错误分类测试。
package handler

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"syscall"
	"testing"
)

// TestClassifyMultipartFormError 验证 ParseMultipartForm 错误的分类逻辑。
func TestClassifyMultipartFormError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		// wantStatusOK 为 true 时表示分类后经 buildTypedUploadError→resolveUploadHTTPStatus
		// 应映射的最终 HTTP 状态码。
		wantStatus int
	}{
		{
			name:       "非 multipart 请求 -> 400",
			err:        http.ErrNotMultipart,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "磁盘空间不足 ENOSPC -> 500",
			err:        fmt.Errorf("write temp: %w", syscall.ENOSPC),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "磁盘配额超限 EDQUOT -> 500",
			err:        fmt.Errorf("write temp: %w", syscall.EDQUOT),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "临时目录权限拒绝 EACCES -> 500",
			err:        fmt.Errorf("open temp: %w", syscall.EACCES),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "os.PathError 包裹的权限错误 -> 500",
			err: &fs.PathError{
				Op:   "open",
				Path: "/tmp/upload-xxx",
				Err:  syscall.EACCES,
			},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "普通上传过大错误 -> 413",
			err:        errors.New("http: request body too large"),
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classified := classifyMultipartFormError(tt.err)
			typed := buildTypedUploadError(classified, 256*1024*1024)
			uploadErr, ok := typed.(*UploadProcessingError)
			if !ok {
				t.Fatalf("buildTypedUploadError 返回类型非 *UploadProcessingError: %T", typed)
			}
			got := resolveUploadHTTPStatus(uploadErr)
			if got != tt.wantStatus {
				t.Errorf("状态码 = %d, 期望 %d (cause=%v)", got, tt.wantStatus, classified)
			}
		})
	}
}

// TestResolveUploadHTTPStatus_NilCause 直接覆盖空 cause 兜底为 500。
func TestResolveUploadHTTPStatus_NilCause(t *testing.T) {
	if got := resolveUploadHTTPStatus(&UploadProcessingError{}); got != http.StatusInternalServerError {
		t.Errorf("空 Cause 状态码 = %d, 期望 500", got)
	}
	if got := resolveUploadHTTPStatus(nil); got != http.StatusInternalServerError {
		t.Errorf("nil 错误状态码 = %d, 期望 500", got)
	}
}
