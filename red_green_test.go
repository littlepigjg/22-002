package firmware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/handler"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/pkg/response"
)

type RespBody struct {
	Success   bool        `json:"success"`
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

func safeCallWriteError(w http.ResponseWriter, err error) (panicked bool, panicVal interface{}, stack string) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			panicVal = r
			stack = string(debug.Stack())
		}
	}()
	handler.WriteError(w, err)
	return false, nil, ""
}

func safeCallResponseError(w http.ResponseWriter, err error) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()
	response.Error(w, err)
	return false
}

func parseRespBody(t *testing.T, raw []byte) *RespBody {
	var b RespBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&b); err != nil {
		t.Logf("parse response body failed: %v, raw=%s", err, string(raw))
		return nil
	}
	return &b
}

func checkMsgNotEmpty(t *testing.T, label string, msg string) bool {
	m := strings.TrimSpace(msg)
	if m == "" {
		t.Logf("[%s] FAIL: message is empty or whitespace-only", label)
		return false
	}
	if strings.Contains(m, "runtime error") || strings.Contains(m, "nil pointer") {
		t.Logf("[%s] FAIL: message contains panic residue: %q", label, msg)
		return false
	}
	return true
}

func TestRedGreen(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.FirmwareDir = t.TempDir()
	fop := service.NewFileOpService(cfg)

	pass := 0
	fail := 0
	totalCases := 0

	// Case 1: NewBizError with CodeInternal and empty message through WriteError.
	t.Run("case1_internal_empty_msg", func(t *testing.T) {
		totalCases++
		err := response.NewBizError(http.StatusInternalServerError, response.CodeInternal, "   ")
		if err == nil {
			t.Logf("case1: expected non-nil error interface, got nil")
			fail++
			return
		}
		rec := httptest.NewRecorder()
		panicked, _, stack := safeCallWriteError(rec, err)
		if panicked {
			t.Logf("case1: panic during WriteError: %s", stack)
			fail++
			return
		}
		if rec.Code/100 != 4 && rec.Code/100 != 5 {
			t.Logf("case1: expected 4xx/5xx status, got %d", rec.Code)
		}
		body := parseRespBody(t, rec.Body.Bytes())
		if body == nil {
			fail++
			return
		}
		if body.Success {
			t.Logf("case1: success should be false on error path")
			fail++
			return
		}
		if !checkMsgNotEmpty(t, "case1", body.Message) {
			fail++
			return
		}
		pass++
	})

	// Case 2: WrapBizError with empty message but non-nil plain cause through WriteError.
	t.Run("case2_wrap_empty_msg_plain_cause", func(t *testing.T) {
		totalCases++
		cause := errors.New("storage backend: corrupt allocation table")
		err := response.WrapBizError(http.StatusInternalServerError, response.CodeInternal, " 	", cause)
		if err == nil {
			t.Logf("case2: expected non-nil error, got nil interface")
			fail++
			return
		}
		rec := httptest.NewRecorder()
		panicked, _, stack := safeCallWriteError(rec, err)
		if panicked {
			t.Logf("case2: panic during WriteError: %s", stack)
			fail++
			return
		}
		body := parseRespBody(t, rec.Body.Bytes())
		if body == nil {
			fail++
			return
		}
		if body.Success {
			t.Logf("case2: success should be false on error path")
			fail++
			return
		}
		if !checkMsgNotEmpty(t, "case2", body.Message) {
			fail++
			return
		}
		msg := body.Message
		if !strings.Contains(msg, "corrupt allocation table") &&
			!strings.Contains(msg, "storage backend") &&
			msg == "internal server error" {
			t.Logf("case2: message lacks cause detail, got %q (may be nil-chain swallowed cause)", msg)
			fail++
			return
		}
		pass++
	})

	// Case 3: Unknown non-model, non-Coder plain error passes through WriteError.
	t.Run("case3_unknown_plain_error_coerce", func(t *testing.T) {
		totalCases++
		err := errors.New("sector checksum mismatch on device block 0x3F11")
		rec := httptest.NewRecorder()
		panicked, _, stack := safeCallWriteError(rec, err)
		if panicked {
			t.Logf("case3: panic during WriteError: %s", stack)
			fail++
			return
		}
		body := parseRespBody(t, rec.Body.Bytes())
		if body == nil {
			fail++
			return
		}
		if body.Success {
			t.Logf("case3: success should be false on error path")
			fail++
			return
		}
		if !checkMsgNotEmpty(t, "case3", body.Message) {
			fail++
			return
		}
		msg := body.Message
		if !strings.Contains(msg, "sector checksum") &&
			!strings.Contains(msg, "0x3F11") &&
			!strings.Contains(msg, "device block") {
			t.Logf("case3: cause detail lost in error chain, got %q", msg)
			fail++
			return
		}
		pass++
	})

	// Case 4: response.Error directly on a constructed Coder-like nil chain.
	t.Run("case4_response_error_coder_chain", func(t *testing.T) {
		totalCases++
		cause := errors.New("")
		err := response.WrapBizError(http.StatusInternalServerError, response.CodeInternal, "", cause)
		if err == nil {
			t.Logf("case4: expected non-nil error interface, got nil")
			fail++
			return
		}
		rec := httptest.NewRecorder()
		panicked := safeCallResponseError(rec, err)
		if panicked {
			t.Logf("case4: panic during response.Error")
			fail++
			return
		}
		if rec.Code == http.StatusOK {
			t.Logf("case4: error path should not produce 200")
			fail++
			return
		}
		body := parseRespBody(t, rec.Body.Bytes())
		if body == nil {
			fail++
			return
		}
		if body.Success {
			t.Logf("case4: success should be false")
			fail++
			return
		}
		if !checkMsgNotEmpty(t, "case4", body.Message) {
			fail++
			return
		}
		pass++
	})

	// Case 5: Coder-like error interface through NewBizError empty message path.
	t.Run("case5_typed_nil_coder_direct", func(t *testing.T) {
		totalCases++
		e := response.NewBizError(http.StatusInternalServerError, response.CodeInternal, "")
		if e == nil {
			t.Logf("case5: expected non-nil error interface")
		}
		rec := httptest.NewRecorder()
		panicked, _, stack := safeCallWriteError(rec, e)
		if panicked {
			t.Logf("case5: panic during WriteError (typed-nil Coder): %s", stack)
			fail++
			return
		}
		body := parseRespBody(t, rec.Body.Bytes())
		if body == nil {
			fail++
			return
		}
		if !checkMsgNotEmpty(t, "case5", body.Message) {
			fail++
			return
		}
		pass++
	})

	// Case 6: FileOpService edge case through service wrapping.
	t.Run("case6_fileop_wrap_chain", func(t *testing.T) {
		totalCases++
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		res, err := fop.SaveMultipartFile(ctx, nil, "")
		_ = res
		if err == nil {
			t.Logf("case6: expected error for nil file header")
			fail++
			return
		}
		rec := httptest.NewRecorder()
		panicked, _, stack := safeCallWriteError(rec, err)
		if panicked {
			t.Logf("case6: panic during WriteError for fileop wrapped error: %s", stack)
			fail++
			return
		}
		body := parseRespBody(t, rec.Body.Bytes())
		if body == nil {
			fail++
			return
		}
		if body.Success {
			t.Logf("case6: success should be false on fileop error")
			fail++
			return
		}
		if !checkMsgNotEmpty(t, "case6", body.Message) {
			fail++
			return
		}
		pass++
	})

	// Case 7: Sanity — model sentinels still produce correct, non-empty messages.
	t.Run("case7_model_sentinels_sane", func(t *testing.T) {
		totalCases++
		errs := []error{
			model.ErrNotFound,
			model.ErrConflict,
			model.ErrInvalidParam,
			model.ErrDeviceNotFound,
			model.ErrFirmwareNotFound,
		}
		for i, e := range errs {
			rec := httptest.NewRecorder()
			panicked, _, stack := safeCallWriteError(rec, e)
			if panicked {
				t.Logf("case7[%d] panic: %s", i, stack)
				fail++
				return
			}
			body := parseRespBody(t, rec.Body.Bytes())
			if body == nil {
				t.Logf("case7[%d] failed to parse body for err=%v", i, e)
				fail++
				return
			}
			if !checkMsgNotEmpty(t, fmt.Sprintf("case7[%d]", i), body.Message) {
				fail++
				return
			}
		}
		pass++
	})

	fmt.Println()
	fmt.Println("============== RED / GREEN 判定 ==============")
	fmt.Printf("总计: %d case, 通过: %d, 失败: %d\n", totalCases, pass, fail)
	if fail > 0 {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Fatalf("RED（红灯，缺陷未修复） — %d/%d cases failed", fail, totalCases)
	} else if pass == totalCases && totalCases > 0 {
		fmt.Println("GREEN（绿灯，缺陷已修复）")
	} else {
		fmt.Println("RED（红灯，缺陷未修复） — 无任何 case 运行")
		t.Fatalf("RED（红灯，缺陷未修复） — no cases executed")
	}
}
