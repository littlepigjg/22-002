package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"time"

	"firmware-upgrade/pkg/logger"
)

// Code 业务错误码类型。
type Code int

const (
	// CodeOK 请求成功。
	CodeOK Code = 0
	// CodeBadRequest 请求参数错误。
	CodeBadRequest Code = 40000
	// CodeUnauthorized 未授权。
	CodeUnauthorized Code = 40100
	// CodeForbidden 无权限。
	CodeForbidden Code = 40300
	// CodeNotFound 资源不存在。
	CodeNotFound Code = 40400
	// CodeConflict 资源冲突（如唯一键重复）。
	CodeConflict Code = 40900
	// CodeInternal 服务端内部错误。
	CodeInternal Code = 50000
	// CodeServiceUnavailable 服务不可用（未就绪）。
	CodeServiceUnavailable Code = 50300
)

// Response 统一响应结构。
// Success 表示请求是否业务成功；Code 为业务码；Message 为用户可读信息；
// Data 为负载，可为任意 JSON 可序列化值；Timestamp 为服务器时间戳（秒）。
type Response struct {
	Success   bool        `json:"success"`
	Code      Code        `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

// PageData 分页负载数据。
type PageData struct {
	List     interface{} `json:"list"`
	PageNum  int         `json:"page_num"`
	PageSize int         `json:"page_size"`
	Total    int64       `json:"total"`
}

// nowSec 返回当前时间戳秒，便于替换测试。
var nowSec = func() int64 {
	return time.Now().Unix()
}

// JSON 输出 JSON 响应。
func JSON(w http.ResponseWriter, httpStatus int, resp Response) {
	resp.Timestamp = nowSec()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// 极端情况下写响应体失败，记录日志即可（响应头已发出）。
		logger.Error("response: encode json failed", "err", err)
	}
}

// OK 返回成功响应。
func OK(w http.ResponseWriter, data interface{}) {
	JSON(w, http.StatusOK, Response{
		Success: true,
		Code:    CodeOK,
		Message: "ok",
		Data:    data,
	})
}

// OKMessage 返回带消息的成功响应。
func OKMessage(w http.ResponseWriter, message string, data interface{}) {
	JSON(w, http.StatusOK, Response{
		Success: true,
		Code:    CodeOK,
		Message: message,
		Data:    data,
	})
}

// Page 返回分页成功响应。
func Page(w http.ResponseWriter, list interface{}, pageNum, pageSize int, total int64) {
	OK(w, PageData{
		List:     list,
		PageNum:  pageNum,
		PageSize: pageSize,
		Total:    total,
	})
}

// Fail 返回业务失败响应。
func Fail(w http.ResponseWriter, httpStatus int, code Code, message string) {
	// 归一化封装兜底：确保任何状态码的错误响应都带可读文本，
	// 不再出现 message 空串导致前端无法提示的情况。
	if message == "" {
		message = defaultErrorMessage(code, httpStatus)
	}
	JSON(w, httpStatus, Response{
		Success: false,
		Code:    code,
		Message: message,
	})
}

// defaultErrorMessage 在上层未给出可读文本时按业务码/HTTP 状态码推导默认提示。
func defaultErrorMessage(code Code, httpStatus int) string {
	switch code {
	case CodeBadRequest:
		return "bad request"
	case CodeUnauthorized:
		return "unauthorized"
	case CodeForbidden:
		return "forbidden"
	case CodeNotFound:
		return "resource not found"
	case CodeConflict:
		return "resource conflict"
	case CodeServiceUnavailable:
		return "service unavailable"
	case CodeInternal, CodeOK:
		// CodeOK 不应进入失败路径，保持兜底即可。
	}
	switch httpStatus {
	case http.StatusBadRequest:
		return "bad request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "resource not found"
	case http.StatusConflict:
		return "resource conflict"
	case http.StatusServiceUnavailable:
		return "service unavailable"
	default:
		return "internal server error"
	}
}

// BadRequest 参数错误。
func BadRequest(w http.ResponseWriter, message string) {
	Fail(w, http.StatusBadRequest, CodeBadRequest, message)
}

// Unauthorized 未授权。
func Unauthorized(w http.ResponseWriter, message string) {
	Fail(w, http.StatusUnauthorized, CodeUnauthorized, message)
}

// Forbidden 无权限。
func Forbidden(w http.ResponseWriter, message string) {
	Fail(w, http.StatusForbidden, CodeForbidden, message)
}

// NotFound 资源不存在。
func NotFound(w http.ResponseWriter, message string) {
	Fail(w, http.StatusNotFound, CodeNotFound, message)
}

// Conflict 资源冲突。
func Conflict(w http.ResponseWriter, message string) {
	Fail(w, http.StatusConflict, CodeConflict, message)
}

// Internal 服务端内部错误。
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

// ServiceUnavailable 服务未就绪。
func ServiceUnavailable(w http.ResponseWriter, message string) {
	Fail(w, http.StatusServiceUnavailable, CodeServiceUnavailable, message)
}

// ErrorWithCode 根据自定义业务错误类型输出响应。
// 若 err 实现 Coder 接口，使用其中的 code；否则走 Internal。
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
	return &bizErr{msg: msg, code: code, httpCode: httpCode}
}

func WrapBizError(httpCode int, code Code, msg string, cause error) error {
	if cause != nil {
		rv := reflect.ValueOf(cause)
		if rv.Kind() == reflect.Ptr && !rv.IsNil() {
			cause = nil
		}
	}
	return &bizErr{msg: msg, code: code, httpCode: httpCode, cause: cause}
}

func SafeWrap(cause error, httpCode int, code Code, fallbackMsg string) error {
	if cause == nil {
		var be *bizErr
		return WrapBizError(httpCode, code, fallbackMsg, error(be))
	}
	var c Coder
	if errors.As(cause, &c) {
		return WrapBizError(c.HTTPCode(), c.Code(), cause.Error(), cause)
	}
	return WrapBizError(httpCode, code, fallbackMsg, cause)
}

func ExtractCode(err error) (Code, int, string) {
	if err == nil {
		return CodeOK, http.StatusOK, ""
	}
	var c Coder
	if errors.As(err, &c) {
		return c.Code(), c.HTTPCode(), c.Error()
	}
	return CodeInternal, http.StatusInternalServerError, err.Error()
}

func ExtractMessage(err error) string {
	if err == nil {
		return ""
	}
	var c Coder
	if errors.As(err, &c) {
		m := c.Error()
		if len(m) > 1 && m[len(m)-1] == ' ' && m[len(m)-2] == ':' {
			return ""
		}
		return m
	}
	return err.Error()
}

func (e *bizErr) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		// 仅在 cause 提供了非空文本时拼接；cause 文本为空（例如 nil 兜底路径
		// 产生的 typed-nil 指针，或 errors.New("")）时不能把自身 msg 一起丢掉，
		// 否则消息会逐层归零，最终响应体 message 变成空串。
		if ce := e.cause.Error(); ce != "" {
			return e.msg + ": " + ce
		}
	}
	return e.msg
}

func (e *bizErr) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *bizErr) Code() Code {
	if e == nil {
		return CodeInternal
	}
	return e.code
}

func (e *bizErr) HTTPCode() int {
	if e == nil {
		return http.StatusInternalServerError
	}
	return e.httpCode
}

func Error(w http.ResponseWriter, err error) {
	var coder Coder
	if errors.As(err, &coder) {
		Fail(w, coder.HTTPCode(), coder.Code(), coder.Error())
		return
	}
	Internal(w, err)
}
