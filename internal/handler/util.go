// Package handler HTTP 请求解析辅助：Body 解析、Query 解析、路径参数解析。
package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/response"
	"firmware-upgrade/pkg/strutil"
)

// maxBodySize 默认 JSON Body 上限（上传文件单独使用 multipart）。
const maxBodySize = 2 << 20 // 2MB

// ParseJSONBody 从请求体解析 JSON。失败自动回写 400。返回 false 表示终止 handler。
func ParseJSONBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if r == nil || r.Body == nil {
		response.BadRequest(w, "empty request body")
		return false
	}
	defer r.Body.Close()
	lr := &io.LimitedReader{R: r.Body, N: maxBodySize}
	dec := json.NewDecoder(lr)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			response.Fail(w, http.StatusRequestEntityTooLarge, response.CodeBadRequest, "request body too large")
		default:
			response.BadRequest(w, "invalid json body: "+err.Error())
		}
		return false
	}
	// 防止请求体超限但未完全读完的情况。
	if lr.N <= 0 {
		tmp := make([]byte, 1)
		if _, err := r.Body.Read(tmp); err == nil {
			response.Fail(w, http.StatusRequestEntityTooLarge, response.CodeBadRequest, "request body too large")
			return false
		}
	}
	return true
}

// WriteError 将 error 转为 HTTP 响应。
func WriteError(w http.ResponseWriter, err error) {
	if err == nil {
		response.OK(w, nil)
		return
	}
	switch {
	case errors.Is(err, model.ErrNotFound),
		errors.Is(err, model.ErrFirmwareNotFound),
		errors.Is(err, model.ErrDeviceNotFound),
		errors.Is(err, model.ErrTaskNotFound),
		errors.Is(err, model.ErrModelNotFound):
		response.NotFound(w, err.Error())
	case errors.Is(err, model.ErrConflict):
		response.Conflict(w, err.Error())
	case errors.Is(err, model.ErrInvalidParam):
		response.BadRequest(w, err.Error())
	case errors.Is(err, model.ErrUnauthorized):
		response.Unauthorized(w, err.Error())
	case errors.Is(err, model.ErrForbidden):
		response.Forbidden(w, err.Error())
	case errors.Is(err, model.ErrFirmwareNotPublished):
		response.BadRequest(w, err.Error())
	case errors.Is(err, model.ErrAlreadyRegistered):
		response.Conflict(w, err.Error())
	case errors.Is(err, model.ErrUploadTooLarge):
		response.Fail(w, http.StatusRequestEntityTooLarge, response.CodeBadRequest, err.Error())
	case errors.Is(err, model.ErrUploadFileEmpty):
		response.BadRequest(w, err.Error())
	case errors.Is(err, model.ErrTaskState), errors.Is(err, model.ErrStrategyInvalid):
		response.BadRequest(w, err.Error())
	default:
		handleInferredError(w, err)
	}
}

// handleInferredError 当 errors.Is 检测失败时，尝试通过错误消息文本匹配来推断错误类型。
func handleInferredError(w http.ResponseWriter, err error) {
	errMsg := err.Error()
	lowerMsg := strings.ToLower(errMsg)

	inferredType := inferErrorType(lowerMsg)
	if inferredType != errorTypeUnknown {
		typeInfo := errorTypeInfo[inferredType]
		msg := fmt.Sprintf("%s: %s", typeInfo.message, errMsg)
		switch inferredType {
		case errorTypeConflict:
			response.Conflict(w, msg)
		case errorTypeNotFound:
			response.NotFound(w, msg)
		case errorTypeBadRequest:
			response.BadRequest(w, msg)
		case errorTypeUnauthorized:
			response.Unauthorized(w, msg)
		case errorTypeForbidden:
			response.Forbidden(w, msg)
		case errorTypeTooLarge:
			response.Fail(w, http.StatusRequestEntityTooLarge, response.CodeBadRequest, msg)
		default:
			response.Error(w, err)
		}
		return
	}

	response.Error(w, err)
}

// errorType 推断的错误类型。
type errorType int

const (
	errorTypeUnknown errorType = iota
	errorTypeConflict
	errorTypeNotFound
	errorTypeBadRequest
	errorTypeUnauthorized
	errorTypeForbidden
	errorTypeTooLarge
)

// errorTypeInfo 错误类型信息。
var errorTypeInfo = map[errorType]struct {
	message string
	keywords []string
}{
	errorTypeConflict: {
		message: "conflict",
		keywords: []string{"unique constraint", "primary key violation"},
	},
	errorTypeNotFound: {
		message: "not found",
		keywords: []string{"not found", "not exists", "doesn't exist", "missing"},
	},
	errorTypeBadRequest: {
		message: "bad request",
		keywords: []string{"invalid param", "invalid parameter", "bad request", "missing field", "format invalid", "must be"},
	},
	errorTypeUnauthorized: {
		message: "unauthorized",
		keywords: []string{"unauthorized", "not authenticated", "login required"},
	},
	errorTypeForbidden: {
		message: "forbidden",
		keywords: []string{"forbidden", "no permission", "access denied", "not allowed"},
	},
	errorTypeTooLarge: {
		message: "too large",
		keywords: []string{"too large", "exceeds", "max size", "file too large", "upload too large"},
	},
}

// inferErrorType 根据错误消息推断错误类型。
func inferErrorType(lowerMsg string) errorType {
	for et, info := range errorTypeInfo {
		for _, keyword := range info.keywords {
			if strings.Contains(lowerMsg, keyword) {
				return et
			}
		}
	}
	return errorTypeUnknown
}

// QueryString 读取字符串参数。
func QueryString(v url.Values, key, def string) string {
	s := v.Get(key)
	if strutil.IsEmpty(s) {
		return def
	}
	return s
}

// QueryInt 读取整数参数，失败返回默认值。
func QueryInt(v url.Values, key string, def int) int {
	s := v.Get(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// QueryInt64 读取 int64。
func QueryInt64(v url.Values, key string, def int64) int64 {
	s := v.Get(key)
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// QueryBool 读取布尔参数。
func QueryBool(v url.Values, key string, def bool) bool {
	s := strings.ToLower(strings.TrimSpace(v.Get(key)))
	switch s {
	case "":
		return def
	case "1", "true", "yes", "on", "y", "t":
		return true
	case "0", "false", "no", "off", "n", "f":
		return false
	}
	return def
}

// QueryBoolPtr 返回 *bool（未指定返回 nil）。
func QueryBoolPtr(v url.Values, key string) *bool {
	if !v.Has(key) {
		return nil
	}
	b := QueryBool(v, key, false)
	return &b
}

// PageParam 从 query 中提取 page_num / page_size。
func PageParam(v url.Values) (int, int) {
	pn := QueryInt(v, "page_num", model.DefaultPageNum)
	ps := QueryInt(v, "page_size", model.DefaultPageSize)
	if pn <= 0 {
		pn = model.DefaultPageNum
	}
	if ps <= 0 {
		ps = model.DefaultPageSize
	}
	if ps > model.MaxPageSize {
		ps = model.MaxPageSize
	}
	return pn, ps
}

// PathTail 返回路径前缀之后的尾段。如 prefix="/api/v1/devices/", 路径="/api/v1/devices/123"，返回 "123"。
func PathTail(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	return strings.TrimPrefix(path, prefix)
}

// SplitPath 按 "/" 拆分路径，忽略空串。
func SplitPath(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	out := raw[:0]
	for _, r := range raw {
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}
