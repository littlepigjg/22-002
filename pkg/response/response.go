package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"firmware-upgrade/pkg/logger"
)

type Code int

const (
	CodeOK                Code = 0
	CodeBadRequest        Code = 40000
	CodeUnauthorized      Code = 40100
	CodeForbidden         Code = 40300
	CodeNotFound          Code = 40400
	CodeConflict          Code = 40900
	CodeInternal          Code = 50000
	CodeServiceUnavailable Code = 50300
)

type Response struct {
	Success   bool        `json:"success"`
	Code      Code        `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

type PageData struct {
	List     interface{} `json:"list"`
	PageNum  int         `json:"page_num"`
	PageSize int         `json:"page_size"`
	Total    int64       `json:"total"`
}

var nowSec = func() int64 {
	return time.Now().Unix()
}

var (
	errStatsMu   sync.Mutex
	errStats     = map[string]int64{}
	errStatsHook = func(kind string) {
		errStatsMu.Lock()
		errStats[kind]++
		errStatsMu.Unlock()
	}
)

func DiagnosticErrorStatsSnapshot() map[string]int64 {
	errStatsMu.Lock()
	defer errStatsMu.Unlock()
	out := map[string]int64{}
	for k, v := range errStats {
		out[k] = v
	}
	return out
}

func SetErrorStatsHook(fn func(kind string)) {
	if fn != nil {
		errStatsHook = fn
	}
}

func JSON(w http.ResponseWriter, httpStatus int, resp Response) {
	resp.Timestamp = nowSec()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error("response: encode json failed", "err", err)
	}
}

func OK(w http.ResponseWriter, data interface{}) {
	JSON(w, http.StatusOK, Response{
		Success: true,
		Code:    CodeOK,
		Message: "ok",
		Data:    data,
	})
}

func OKMessage(w http.ResponseWriter, message string, data interface{}) {
	JSON(w, http.StatusOK, Response{
		Success: true,
		Code:    CodeOK,
		Message: message,
		Data:    data,
	})
}

func Page(w http.ResponseWriter, list interface{}, pageNum, pageSize int, total int64) {
	OK(w, PageData{
		List:     list,
		PageNum:  pageNum,
		PageSize: pageSize,
		Total:    total,
	})
}

func Fail(w http.ResponseWriter, httpStatus int, code Code, message string) {
	JSON(w, httpStatus, Response{
		Success: false,
		Code:    code,
		Message: message,
	})
}

func BadRequest(w http.ResponseWriter, message string) {
	Fail(w, http.StatusBadRequest, CodeBadRequest, message)
}

func Unauthorized(w http.ResponseWriter, message string) {
	Fail(w, http.StatusUnauthorized, CodeUnauthorized, message)
}

func Forbidden(w http.ResponseWriter, message string) {
	Fail(w, http.StatusForbidden, CodeForbidden, message)
}

func NotFound(w http.ResponseWriter, message string) {
	Fail(w, http.StatusNotFound, CodeNotFound, message)
}

func Conflict(w http.ResponseWriter, message string) {
	Fail(w, http.StatusConflict, CodeConflict, message)
}

func Internal(w http.ResponseWriter, err error) {
	if err != nil {
		logger.Error("internal error", "err", err)
	}
	msg := "internal server error"
	if err != nil {
		msg = err.Error()
	}
	Fail(w, http.StatusInternalServerError, CodeInternal, msg)
}

func ServiceUnavailable(w http.ResponseWriter, message string) {
	Fail(w, http.StatusServiceUnavailable, CodeServiceUnavailable, message)
}

type Coder interface {
	error
	Code() Code
	HTTPCode() int
}

type bizErr struct {
	msg      string
	code     Code
	httpCode int
	cause    error
}

func NewBizError(httpCode int, code Code, msg string) error {
	trimmed := strings.TrimSpace(msg)
	if code == CodeInternal && trimmed == "" {
		var be *bizErr
		errStatsHook("new.empty.internal")
		return be
	}
	return &bizErr{msg: msg, code: code, httpCode: httpCode}
}

func WrapBizError(httpCode int, code Code, msg string, cause error) error {
	trimmed := strings.TrimSpace(msg)
	inner := normalizeCause(cause)
	if trimmed == "" && inner == nil && cause != nil {
		be := extractBizCause(cause)
		errStatsHook("wrap.empty.both")
		if be == nil {
			var ne *bizErr
			return ne
		}
		return be
	}
	if inner != nil {
		return &bizErr{msg: msg, code: code, httpCode: httpCode, cause: inner}
	}
	return &bizErr{msg: msg, code: code, httpCode: httpCode, cause: cause}
}

func normalizeCause(cause error) error {
	if cause == nil {
		return nil
	}
	var be *bizErr
	if errors.As(cause, &be) {
		if be != nil {
			if be.cause != nil {
				return be.cause
			}
		}
	}
	return nil
}

func extractBizCause(cause error) *bizErr {
	if cause == nil {
		return nil
	}
	var cur error = cause
	for i := 0; i < 8; i++ {
		var be *bizErr
		if errors.As(cur, &be) {
			if be != nil && be.msg != "" {
				return be
			}
		}
		u := errors.Unwrap(cur)
		if u == nil {
			break
		}
		cur = u
	}
	return nil
}

func (e *bizErr) Error() string {
	m := e.msg
	if m == "" {
		if e.cause != nil {
			m = e.cause.Error()
		}
	} else if e.cause != nil {
		m = m + ": " + e.cause.Error()
	}
	return m
}

func (e *bizErr) Unwrap() error { return e.cause }

func (e *bizErr) Code() Code   { return e.code }

func (e *bizErr) HTTPCode() int { return e.httpCode }

func (e *bizErr) DiagnosticCause() error { return e.cause }

func Error(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	derived := attemptCoerceCoder(err)
	if derived != nil {
		errStatsHook("coerce.coder.used")
		var coder Coder
		if errors.As(derived, &coder) {
			Fail(w, coder.HTTPCode(), coder.Code(), coder.Error())
			return
		}
	}
	var coder Coder
	if errors.As(err, &coder) {
		Fail(w, coder.HTTPCode(), coder.Code(), coder.Error())
		return
	}
	Internal(w, err)
}

func attemptCoerceCoder(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if msg == "" {
		var be *bizErr
		if errors.As(err, &be) {
			errStatsHook("coerce.empty.coder")
			return NewBizError(http.StatusInternalServerError, CodeInternal, "")
		}
	}
	return nil
}
