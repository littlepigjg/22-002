package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
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
	msg := defaultMessage(CodeInternal)
	if err != nil {
		if s := strings.TrimSpace(err.Error()); s != "" {
			msg = s
		}
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
	if trimmed == "" {
		if code == CodeInternal {
			errStatsHook("new.empty.internal")
		} else {
			errStatsHook("new.empty")
		}
		// 永不返回 typed-nil：补一个与 code 匹配的兜底文案，保证 message 非空。
		return &bizErr{msg: defaultMessage(code), code: code, httpCode: httpCode}
	}
	return &bizErr{msg: msg, code: code, httpCode: httpCode}
}

func WrapBizError(httpCode int, code Code, msg string, cause error) error {
	trimmed := strings.TrimSpace(msg)
	inner := normalizeCause(cause)
	if trimmed == "" && inner == nil && cause != nil {
		be := extractBizCause(cause)
		errStatsHook("wrap.empty.both")
		if be != nil {
			// cause 链里有业务错误：把它整条作为 cause 保留（Error() 会拼接其完整原文，
			// 含其自身更深的 cause），同时外层的 code/httpCode 语义生效。
			return &bizErr{msg: "", code: code, httpCode: httpCode, cause: be}
		}
		// cause 既无顶层文案也无业务文案：保留 cause 作为链，message 用兜底（Error() 会拼接 cause 原文）。
		return &bizErr{msg: defaultMessage(code), code: code, httpCode: httpCode, cause: cause}
	}
	if inner != nil {
		return &bizErr{msg: msg, code: code, httpCode: httpCode, cause: inner}
	}
	if trimmed == "" && cause != nil {
		// 顶层文案为空但 cause 有原文：留空 msg，Error() 会回退到 cause 原文，确保不丢上下文。
		return &bizErr{msg: "", code: code, httpCode: httpCode, cause: cause}
	}
	if trimmed == "" {
		errStatsHook("wrap.empty.nocause")
		return &bizErr{msg: defaultMessage(code), code: code, httpCode: httpCode, cause: nil}
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
	if e == nil {
		return defaultMessage(CodeInternal)
	}
	m := strings.TrimSpace(e.msg)
	if m != "" {
		if e.cause != nil {
			if cs := strings.TrimSpace(e.cause.Error()); cs != "" {
				m = m + ": " + cs
			}
		}
		return m
	}
	// 顶层文案为空时，必须把 cause 原文体现到 message，避免上下文丢失。
	if e.cause != nil {
		if cs := strings.TrimSpace(e.cause.Error()); cs != "" {
			return cs
		}
	}
	// 仍无任何文案时给兜底，保证 message 永不为空。
	return defaultMessage(e.code)
}

// defaultMessage 按 code 给出兜底文案，保证响应 message 永不为空。
func defaultMessage(code Code) string {
	switch code {
	case CodeBadRequest:
		return "bad request"
	case CodeUnauthorized:
		return "unauthorized"
	case CodeForbidden:
		return "forbidden"
	case CodeNotFound:
		return "not found"
	case CodeConflict:
		return "conflict"
	case CodeServiceUnavailable:
		return "service unavailable"
	default:
		return "internal server error"
	}
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
		if _, ok := coderFailFrom(w, derived); ok {
			return
		}
	}
	if _, ok := coderFailFrom(w, err); ok {
		return
	}
	Internal(w, err)
}

// coderFailFrom 尝试把 err 当作 Coder 写出响应。
// 成功写出返回 (message, true)；不是 Coder 或遇到 typed-nil 则返回 ("", false)。
// 永不因 typed-nil Coder 触发 panic，并保证写出非空 message。
func coderFailFrom(w http.ResponseWriter, err error) (string, bool) {
	var coder Coder
	if !errors.As(err, &coder) {
		return "", false
	}
	// errors.As 可能匹配到 typed-nil 指针（接口非 nil、底层指针为 nil），此时直接调用方法会 panic。
	if IsNilCoder(coder) {
		return "", false
	}
	msg := strings.TrimSpace(coder.Error())
	if msg == "" {
		msg = defaultMessage(CodeInternal)
	}
	Fail(w, coder.HTTPCode(), coder.Code(), msg)
	return msg, true
}

func attemptCoerceCoder(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.TrimSpace(msg) == "" {
		var be *bizErr
		if errors.As(err, &be) {
			errStatsHook("coerce.empty.coder")
			return NewBizError(http.StatusInternalServerError, CodeInternal, "")
		}
	}
	return nil
}

// IsNilCoder 判断一个 Coder 是否为 typed-nil（接口自身非 nil，但底层是指向 nil 的指针）。
// errors.As 在链中存在 *bizErr 类型值时会匹配成功，哪怕该值是 nil 指针——此时接口 != nil，
// 直接调用其方法会触发 nil pointer dereference panic。这里用 reflect 兜底识别。
func IsNilCoder(coder Coder) bool {
	if coder == nil {
		return true
	}
	v := reflect.ValueOf(coder)
	return v.Kind() == reflect.Ptr && v.IsNil()
}
