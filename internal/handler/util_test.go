package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/response"
)

func assertNoPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked: %v", name, r)
		}
	}()
	fn()
}

func decodeResponse(t *testing.T, body []byte) (msg string, code int, httpStatus int) {
	t.Helper()
	var resp struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode body failed: %v body=%q", err, string(body))
	}
	return resp.Message, resp.Code, 0
}

func assertNonEmptyMessage(t *testing.T, name string, w *httptest.ResponseRecorder) {
	t.Helper()
	msg, _, _ := decodeResponse(t, w.Body.Bytes())
	if msg == "" {
		t.Fatalf("%s: empty message body=%s", name, w.Body.String())
	}
}

// curl 模拟的几种未知裸错误经 WriteError：不 panic、message 非空、状态 500。
func TestWriteErrorUnknownBareNoPanicNonEmpty(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"plain-unknown", errors.New("totally unknown transient fs descriptor error")},
		{"empty-text", errors.New("")},
		{"fmt-wrapped", fmt.Errorf("storage: %w", errors.New("empty descriptor"))},
		{"nested-empty", fmt.Errorf("%w", errors.New(""))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			assertNoPanic(t, c.name, func() { WriteError(w, c.err) })
			assertNonEmptyMessage(t, c.name, w)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("%s: status=%d want 500 body=%s", c.name, w.Code, w.Body.String())
			}
		})
	}
}

// 直接构造 Internal 错误（NewBizError 空 message）经 WriteError：不 panic、message 非空。
func TestWriteErrorConstructedInternalNoPanic(t *testing.T) {
	t.Run("newbiz-empty", func(t *testing.T) {
		be := response.NewBizError(http.StatusInternalServerError, response.CodeInternal, "")
		w := httptest.NewRecorder()
		assertNoPanic(t, "newbiz", func() { WriteError(w, be) })
		assertNonEmptyMessage(t, "newbiz", w)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d want 500", w.Code)
		}
	})
	t.Run("wrap-empty-plain-cause", func(t *testing.T) {
		be := response.WrapBizError(http.StatusInternalServerError, response.CodeInternal, "", errors.New("root cause detail"))
		w := httptest.NewRecorder()
		assertNoPanic(t, "wrap", func() { WriteError(w, be) })
		assertNonEmptyMessage(t, "wrap", w)
		msg, _, _ := decodeResponse(t, w.Body.Bytes())
		if msg == "internal server error" {
			t.Fatalf("cause text lost, got generic %q", msg)
		}
		if !containsStr(msg, "root cause detail") {
			t.Fatalf("cause text missing: %q", msg)
		}
	})
}

// bizErr 包装一层、cause 塞普通 errors.New：cause 原文保留到 message。
func TestWriteErrorBizErrWithPlainCausePreservesText(t *testing.T) {
	be := response.WrapBizError(http.StatusInternalServerError, response.CodeInternal, "upload save failed", errors.New("disk descriptor empty"))
	w := httptest.NewRecorder()
	assertNoPanic(t, "WriteError-cause", func() { WriteError(w, be) })
	msg, _, _ := decodeResponse(t, w.Body.Bytes())
	if msg != "upload save failed: disk descriptor empty" {
		t.Fatalf("msg=%q want cause preserved", msg)
	}
}

// 双层 bizErr + 普通 errors.New cause。
func TestWriteErrorTwoLayerBizErrPlainCause(t *testing.T) {
	mid := response.WrapBizError(http.StatusInternalServerError, response.CodeInternal, "", errors.New("校验失败: crc mismatch"))
	top := response.WrapBizError(http.StatusInternalServerError, response.CodeInternal, "device report failed", mid)
	w := httptest.NewRecorder()
	assertNoPanic(t, "WriteError-twolayer", func() { WriteError(w, top) })
	msg, _, _ := decodeResponse(t, w.Body.Bytes())
	if !containsStr(msg, "crc mismatch") {
		t.Fatalf("inner cause lost: %q", msg)
	}
	if !containsStr(msg, "device report failed") {
		t.Fatalf("top msg lost: %q", msg)
	}
}

// sentinel 行为保持一致，不回退成 500。
func TestWriteErrorSentinelsPreserved(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"NotFound", model.ErrNotFound, http.StatusNotFound},
		{"Conflict", model.ErrConflict, http.StatusConflict},
		{"UploadTooLarge", model.ErrUploadTooLarge, http.StatusRequestEntityTooLarge},
		{"UploadFileEmpty", model.ErrUploadFileEmpty, http.StatusBadRequest},
		{"FirmwareNotFound", model.ErrFirmwareNotFound, http.StatusNotFound},
		{"DeviceNotFound", model.ErrDeviceNotFound, http.StatusNotFound},
		{"ModelNotFound", model.ErrModelNotFound, http.StatusNotFound},
		{"TaskNotFound", model.ErrTaskNotFound, http.StatusNotFound},
		{"AlreadyRegistered", model.ErrAlreadyRegistered, http.StatusConflict},
		{"InvalidParam", model.ErrInvalidParam, http.StatusBadRequest},
		{"Unauthorized", model.ErrUnauthorized, http.StatusUnauthorized},
		{"Forbidden", model.ErrForbidden, http.StatusForbidden},
		{"FirmwareNotPublished", model.ErrFirmwareNotPublished, http.StatusBadRequest},
		{"TaskState", model.ErrTaskState, http.StatusBadRequest},
		{"StrategyInvalid", model.ErrStrategyInvalid, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			assertNoPanic(t, c.name, func() { WriteError(w, c.err) })
			if w.Code != c.wantStatus {
				t.Fatalf("%s: status=%d want %d body=%s", c.name, w.Code, c.wantStatus, w.Body.String())
			}
			assertNonEmptyMessage(t, c.name, w)
		})
	}
}

// 这几个 sentinel 原本就走 default 分支由 response.Error 处理（500），
// 修复后必须保持原行为：不 panic、message 非空、不因 typed-nil 退回空 message。
func TestWriteErrorSentinelsDefaultBranchNonEmpty(t *testing.T) {
	cases := []error{
		model.ErrVersionMismatch,
		model.ErrMD5Mismatch,
		model.ErrExceedQuota,
		model.ErrDeviceExcluded,
	}
	for _, err := range cases {
		name := err.Error()
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			assertNoPanic(t, name, func() { WriteError(w, err) })
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("%s: status=%d want 500 body=%s", name, w.Code, w.Body.String())
			}
			assertNonEmptyMessage(t, name, w)
		})
	}
}

// 包了一层普通 errors.New 的 sentinel（errors.Is 仍命中）也应保持原行为。
func TestWriteErrorWrappedSentinelPreserved(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"wrapped-NotFound", fmt.Errorf("ctx: %w", model.ErrNotFound), http.StatusNotFound},
		{"wrapped-Conflict", fmt.Errorf("ctx: %w", model.ErrConflict), http.StatusConflict},
		{"wrapped-UploadTooLarge", fmt.Errorf("ctx: %w", model.ErrUploadTooLarge), http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			assertNoPanic(t, c.name, func() { WriteError(w, c.err) })
			if w.Code != c.wantStatus {
				t.Fatalf("%s: status=%d want %d body=%s", c.name, w.Code, c.wantStatus, w.Body.String())
			}
			assertNonEmptyMessage(t, c.name, w)
		})
	}
}

// 前缀/关键字匹配的错误保持原行为（不回退 500）。
func TestWriteErrorPrefixKeywordPreserved(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"mac-format", errors.New("mac format invalid"), http.StatusBadRequest},
		{"already-exists", errors.New("model id already exists"), http.StatusConflict},
		{"already-registered", errors.New("device already registered"), http.StatusConflict},
		{"not-found-text", errors.New("xxx not found"), http.StatusBadRequest},
		{"invalid-text", errors.New("field invalid"), http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			assertNoPanic(t, c.name, func() { WriteError(w, c.err) })
			if w.Code != c.wantStatus {
				t.Fatalf("%s: status=%d want %d body=%s", c.name, w.Code, c.wantStatus, w.Body.String())
			}
			assertNonEmptyMessage(t, c.name, w)
		})
	}
}

// WriteError(nil) 应返回 OK。
func TestWriteErrorNilReturnsOK(t *testing.T) {
	w := httptest.NewRecorder()
	assertNoPanic(t, "nil", func() { WriteError(w, nil) })
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
