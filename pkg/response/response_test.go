package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeMsg(t *testing.T, body []byte) (string, int, int) {
	t.Helper()
	var resp struct {
		Message string `json:"message"`
		Code    Code   `json:"code"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode body failed: %v body=%q", err, string(body))
	}
	return resp.Message, int(resp.Code), len(resp.Message)
}

func assertNoPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked: %v", name, r)
		}
	}()
	fn()
}

// 所有写响应路径必须输出非空 message。
func assertNonEmpty(t *testing.T, name string, w *httptest.ResponseRecorder) {
	t.Helper()
	_, code, n := decodeMsg(t, w.Body.Bytes())
	if n == 0 {
		t.Fatalf("%s: empty message; body=%s", name, w.Body.String())
	}
	if code == 0 {
		t.Fatalf("%s: zero code", name)
	}
}

// NewBizError with empty message: typed-nil 必须被消除，message 非空，不 panic。
func TestNewBizErrorEmptyNeverPanicsNorEmpty(t *testing.T) {
	be := NewBizError(http.StatusInternalServerError, CodeInternal, "")
	if be == nil {
		t.Fatalf("NewBizError empty returned nil interface (typed-nil regression)")
	}
	// 直接当 error 用：Error() 非空。
	if be.Error() == "" {
		t.Fatalf("NewBizError empty: Error() empty")
	}
	w := httptest.NewRecorder()
	assertNoPanic(t, "Error-emptypednil", func() { Error(w, be) })
	assertNonEmpty(t, "Error-emptypednil", w)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", w.Code)
	}
}

// 直接构造 typed-nil *bizErr（外部代码可能这么做）也不应 panic。
func TestErrorTypedNilBizErrPointer(t *testing.T) {
	var be *bizErr // typed-nil: 接口非 nil、底层指针 nil
	var asErr error = be
	if asErr == nil {
		t.Fatalf("expected typed-nil non-nil interface")
	}
	w := httptest.NewRecorder()
	assertNoPanic(t, "Error-typednil-ptr", func() { Error(w, asErr) })
	assertNonEmpty(t, "Error-typednil-ptr", w)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", w.Code)
	}
}

// 未知裸错误（非 sentinel、无前缀/关键字匹配）经 Error 写响应：不 panic、message 非空。
func TestErrorUnknownBareError(t *testing.T) {
	cases := []error{
		errors.New("some unknown transient fs descriptor error"),
		errors.New(""), // 文本为空的裸错误
		fmt.Errorf("disk: %w", errors.New("empty descriptor")),
		nil, // nil 应直接返回不写
	}
	for i, err := range cases {
		name := fmt.Sprintf("case-%d", i)
		w := httptest.NewRecorder()
		if err == nil {
			Error(w, nil)
			continue
		}
		assertNoPanic(t, name, func() { Error(w, err) })
		assertNonEmpty(t, name, w)
	}
}

// Internal：err 为 nil 或 Error() 为空时仍必须有非空 message。
func TestInternalNeverEmpty(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		w := httptest.NewRecorder()
		assertNoPanic(t, "Internal-nil", func() { Internal(w, nil) })
		assertNonEmpty(t, "Internal-nil", w)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d want 500", w.Code)
		}
	})
	t.Run("empty-text", func(t *testing.T) {
		w := httptest.NewRecorder()
		assertNoPanic(t, "Internal-emptytext", func() {
			Internal(w, NewBizError(http.StatusInternalServerError, CodeInternal, ""))
		})
		assertNonEmpty(t, "Internal-emptytext", w)
	})
}

// cause 原文必须保留并体现到 message。
func TestCauseTextPreservedInMessage(t *testing.T) {
	t.Run("msg-plus-plain-cause", func(t *testing.T) {
		be := WrapBizError(http.StatusInternalServerError, CodeInternal, "upload save failed", errors.New("disk descriptor empty"))
		if be.Error() != "upload save failed: disk descriptor empty" {
			t.Fatalf("Error()=%q", be.Error())
		}
		w := httptest.NewRecorder()
		assertNoPanic(t, "Error-cause1", func() { Error(w, be) })
		msg, _, _ := decodeMsg(t, w.Body.Bytes())
		if msg != "upload save failed: disk descriptor empty" {
			t.Fatalf("msg=%q want cause preserved", msg)
		}
	})
	t.Run("empty-msg-plain-cause", func(t *testing.T) {
		// 顶层文案为空、cause 是普通 errors.New：cause 原文必须出现在 message。
		be := WrapBizError(http.StatusInternalServerError, CodeInternal, "", errors.New("root cause detail"))
		if be == nil {
			t.Fatalf("typed-nil regression")
		}
		w := httptest.NewRecorder()
		assertNoPanic(t, "Error-cause2", func() { Error(w, be) })
		msg, _, _ := decodeMsg(t, w.Body.Bytes())
		if msg == "" {
			t.Fatalf("empty message, cause lost")
		}
		if msg == "internal server error" {
			t.Fatalf("cause text lost, got generic %q", msg)
		}
		if !contains(msg, "root cause detail") {
			t.Fatalf("cause text missing from message: %q", msg)
		}
	})
}

// bizErr 包装一层 bizErr + 普通 errors.New cause（cause 链里塞普通错误）。
func TestBizErrWrappingBizErrWithPlainCause(t *testing.T) {
	inner := WrapBizError(http.StatusBadRequest, CodeBadRequest, "model id already exists", nil)
	outer := WrapBizError(http.StatusInternalServerError, CodeInternal, "", inner)
	if outer == nil {
		t.Fatalf("typed-nil regression")
	}
	w := httptest.NewRecorder()
	assertNoPanic(t, "Error-bizwrapbiz", func() { Error(w, outer) })
	msg, _, _ := decodeMsg(t, w.Body.Bytes())
	if msg == "" {
		t.Fatalf("empty message")
	}
}

// 双层 bizErr，cause 是普通 errors.New（"bizErr 包装再 cause 塞 errors.New"）。
func TestTwoLayerBizErrPlainCause(t *testing.T) {
	mid := WrapBizError(http.StatusInternalServerError, CodeInternal, "", errors.New("校验失败: crc mismatch"))
	top := WrapBizError(http.StatusInternalServerError, CodeInternal, "device report failed", mid)
	if top == nil {
		t.Fatalf("typed-nil regression")
	}
	w := httptest.NewRecorder()
	assertNoPanic(t, "Error-twolayer", func() { Error(w, top) })
	msg, _, _ := decodeMsg(t, w.Body.Bytes())
	if msg == "" {
		t.Fatalf("empty message")
	}
	if !contains(msg, "crc mismatch") {
		t.Fatalf("inner cause text lost: %q", msg)
	}
	if !contains(msg, "device report failed") {
		t.Fatalf("top msg lost: %q", msg)
	}
}

// bizErr 空 msg + 普通 cause 的 cause 原文必须出现在 message（不再只剩 internal server error）。
func TestWrapEmptyPlainCauseNoGenericOnly(t *testing.T) {
	be := WrapBizError(http.StatusInternalServerError, CodeInternal, "", errors.New("校验失败: crc mismatch"))
	if be == nil {
		t.Fatalf("typed-nil regression")
	}
	w := httptest.NewRecorder()
	assertNoPanic(t, "Error-generic", func() { Error(w, be) })
	msg, _, _ := decodeMsg(t, w.Body.Bytes())
	if msg == "internal server error" {
		t.Fatalf("cause text lost, only generic: %q", msg)
	}
	if !contains(msg, "crc mismatch") {
		t.Fatalf("cause text missing: %q", msg)
	}
}

// Coder 行为：带 httpCode/code 的 bizErr 经 Error 应写出对应状态。
func TestErrorCoderStatusCodes(t *testing.T) {
	be := NewBizError(http.StatusConflict, CodeConflict, "dup")
	w := httptest.NewRecorder()
	assertNoPanic(t, "Error-coder", func() { Error(w, be) })
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409", w.Code)
	}
	msg, code, _ := decodeMsg(t, w.Body.Bytes())
	if code != int(CodeConflict) {
		t.Fatalf("code=%d want %d", code, CodeConflict)
	}
	if msg != "dup" {
		t.Fatalf("msg=%q want dup", msg)
	}
}

// IsNilCoder 兜底。
func TestIsNilCoder(t *testing.T) {
	if !IsNilCoder(nil) {
		t.Fatalf("nil interface should be nil coder")
	}
	var be *bizErr
	if !IsNilCoder(be) {
		t.Fatalf("typed-nil *bizErr should be detected as nil coder")
	}
	real := &bizErr{msg: "x", code: CodeInternal, httpCode: http.StatusInternalServerError}
	if IsNilCoder(real) {
		t.Fatalf("real bizErr wrongly detected nil")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
