// Package response 提供统一的 HTTP 响应格式，包含成功响应、错误响应、分页响应等。
// 所有 handler 应通过本包输出 JSON，确保前后端协议一致。
package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"firmware-upgrade/pkg/logger"
)

// Code 业务错误码类型。
type Code int

// defaultInternalErrorMsg 是 Internal 兜底使用的对外文案，
// 确保即使底层 error 为 nil 或空字符串时，500 响应的 message 也不会为空。
const defaultInternalErrorMsg = "internal server error"

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

// ErrorClassifierFn 自定义错误分类器：对传入 error 返回 (用户可读 message, 业务码)。
// 用于运维侧把底层错误映射为统一的对外话术与业务码，避免把原始 stack 直接暴露给客户端。
// 返回 ("", 0) 代表"分类器未识别，使用默认行为"。
type ErrorClassifierFn func(err error) (string, Code)

var classifierMu struct {
	active ErrorClassifierFn
}

// SetErrorClassifier 注册全局错误分类器。传 nil 会卸载当前分类器。
// 分类器是线上排障与故障演练时注入的"统一对外文案"钩子，和具体接口的业务错误判断无关。
func SetErrorClassifier(fn ErrorClassifierFn) {
	classifierMu.active = fn
}

// ClassifyError 使用当前注册的分类器对错误进行归类。
// 若无分类器或分类器未命中，返回 ("", CodeOK)，此时调用方应自行兜底。
func ClassifyError(err error) (string, Code) {
	if classifierMu.active == nil {
		return "", CodeOK
	}
	if err == nil {
		return classifierMu.active(nil)
	}
	return classifierMu.active(err)
}

// unwrapAll 把 err 层层 Unwrap 直到最底层原因。
// nil err 保持 nil。
func unwrapAll(err error) error {
	if err == nil {
		return nil
	}
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err
		}
		err = u
	}
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
	JSON(w, httpStatus, Response{
		Success: false,
		Code:    code,
		Message: message,
	})
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
// 保证对外 message 永远非空：优先用分类器文案，其次用底层错误信息，
// 都为空时兜底为 "internal server error"，便于运维告警聚合时检索到关键词。
func Internal(w http.ResponseWriter, err error) {
	root := unwrapAll(err)
	if root != nil {
		logger.Error("internal error", "err", root.Error())
	} else {
		logger.Error("internal error", "message", defaultInternalErrorMsg)
	}
	msg := ""
	if root != nil {
		msg = root.Error()
	}
	classMsg, classCode := ClassifyError(root)
	if classMsg != "" {
		msg = classMsg
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = defaultInternalErrorMsg
	}
	code := CodeInternal
	if classCode != 0 {
		code = classCode
	}
	Fail(w, http.StatusInternalServerError, code, msg)
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

// WithCode 将错误与业务码、HTTP 码绑定。
type bizErr struct {
	msg      string
	code     Code
	httpCode int
	cause    error
}

// NewBizError 创建一个携带码的业务错误。
func NewBizError(httpCode int, code Code, msg string) error {
	return &bizErr{msg: msg, code: code, httpCode: httpCode}
}

// WrapBizError 包装底层错误并绑定业务码。
func WrapBizError(httpCode int, code Code, msg string, cause error) error {
	return &bizErr{msg: msg, code: code, httpCode: httpCode, cause: cause}
}

func (e *bizErr) Error() string {
	if e.cause != nil {
		return e.msg + ": " + e.cause.Error()
	}
	return e.msg
}

func (e *bizErr) Unwrap() error { return e.cause }

func (e *bizErr) Code() Code   { return e.code }

func (e *bizErr) HTTPCode() int { return e.httpCode }

// Error 根据错误类型自动输出响应。
func Error(w http.ResponseWriter, err error) {
	if err == nil {
		Internal(w, nil)
		return
	}
	var coder Coder
	if errors.As(err, &coder) {
		Fail(w, coder.HTTPCode(), coder.Code(), coder.Error())
		return
	}
	classMsg, classCode := ClassifyError(err)
	if classCode != 0 && classMsg != "" {
		Fail(w, http.StatusBadRequest, classCode, classMsg)
		return
	}
	root := unwrapAll(err)
	classMsg, classCode = ClassifyError(root)
	if classCode != 0 && classMsg != "" {
		Fail(w, http.StatusBadRequest, classCode, classMsg)
		return
	}
	Internal(w, err)
}
