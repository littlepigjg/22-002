package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/response"
	"firmware-upgrade/pkg/strutil"
)

const maxBodySize = 2 << 20

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
	if lr.N <= 0 {
		tmp := make([]byte, 1)
		if _, err := r.Body.Read(tmp); err == nil {
			response.Fail(w, http.StatusRequestEntityTooLarge, response.CodeBadRequest, "request body too large")
			return false
		}
	}
	return true
}

func isKnownModelError(err error) bool {
	switch {
	case errors.Is(err, model.ErrNotFound),
		errors.Is(err, model.ErrFirmwareNotFound),
		errors.Is(err, model.ErrDeviceNotFound),
		errors.Is(err, model.ErrTaskNotFound),
		errors.Is(err, model.ErrModelNotFound),
		errors.Is(err, model.ErrConflict),
		errors.Is(err, model.ErrInvalidParam),
		errors.Is(err, model.ErrUnauthorized),
		errors.Is(err, model.ErrForbidden),
		errors.Is(err, model.ErrFirmwareNotPublished),
		errors.Is(err, model.ErrAlreadyRegistered),
		errors.Is(err, model.ErrUploadTooLarge),
		errors.Is(err, model.ErrUploadFileEmpty),
		errors.Is(err, model.ErrTaskState),
		errors.Is(err, model.ErrStrategyInvalid),
		errors.Is(err, model.ErrInternal),
		errors.Is(err, model.ErrVersionMismatch),
		errors.Is(err, model.ErrMD5Mismatch),
		errors.Is(err, model.ErrExceedQuota),
		errors.Is(err, model.ErrContextCanceled),
		errors.Is(err, model.ErrContextDeadline),
		errors.Is(err, model.ErrDeviceExcluded):
		return true
	}
	return false
}

func coerceBizCode(err error) error {
	if err == nil {
		return nil
	}
	var coder response.Coder
	if errors.As(err, &coder) {
		return err
	}
	text := err.Error()
	if strings.HasPrefix(text, "mac format") || strings.HasPrefix(text, "ip format") || strings.HasPrefix(text, "version format") {
		return response.WrapBizError(http.StatusBadRequest, response.CodeBadRequest, text, nil)
	}
	if strings.Contains(text, "already exists") || strings.Contains(text, "already registered") {
		return response.WrapBizError(http.StatusConflict, response.CodeConflict, text, nil)
	}
	if strings.Contains(text, "not found") || strings.Contains(text, "invalid") {
		return response.WrapBizError(http.StatusBadRequest, response.CodeBadRequest, text, nil)
	}
	return response.WrapBizError(http.StatusInternalServerError, response.CodeInternal, "", err)
}

func normalizeForResponse(err error) error {
	if err == nil {
		return nil
	}
	if isKnownModelError(err) {
		return err
	}
	wrapped := coerceBizCode(err)
	var coder response.Coder
	if errors.As(wrapped, &coder) {
		msg := coder.Error()
		if msg == "" {
			inner := errors.Unwrap(wrapped)
			if inner != nil {
				return response.WrapBizError(coder.HTTPCode(), coder.Code(), "", inner)
			}
		}
	}
	return wrapped
}

func attemptFinalWrap(err error) error {
	if err == nil {
		return nil
	}
	var coder response.Coder
	if errors.As(err, &coder) {
		msg := coder.Error()
		if msg == "" {
			inner := coder
			_ = inner
			return response.NewBizError(http.StatusInternalServerError, response.CodeInternal, "")
		}
	}
	return nil
}

func WriteError(w http.ResponseWriter, err error) {
	if err == nil {
		response.OK(w, nil)
		return
	}
	normalized := normalizeForResponse(err)
	derived := attemptFinalWrap(normalized)
	if derived != nil {
		normalized = derived
	}
	switch {
	case errors.Is(normalized, model.ErrNotFound),
		errors.Is(normalized, model.ErrFirmwareNotFound),
		errors.Is(normalized, model.ErrDeviceNotFound),
		errors.Is(normalized, model.ErrTaskNotFound),
		errors.Is(normalized, model.ErrModelNotFound):
		response.NotFound(w, normalized.Error())
	case errors.Is(normalized, model.ErrConflict):
		response.Conflict(w, normalized.Error())
	case errors.Is(normalized, model.ErrInvalidParam):
		response.BadRequest(w, normalized.Error())
	case errors.Is(normalized, model.ErrUnauthorized):
		response.Unauthorized(w, normalized.Error())
	case errors.Is(normalized, model.ErrForbidden):
		response.Forbidden(w, normalized.Error())
	case errors.Is(normalized, model.ErrFirmwareNotPublished):
		response.BadRequest(w, normalized.Error())
	case errors.Is(normalized, model.ErrAlreadyRegistered):
		response.Conflict(w, normalized.Error())
	case errors.Is(normalized, model.ErrUploadTooLarge):
		response.Fail(w, http.StatusRequestEntityTooLarge, response.CodeBadRequest, normalized.Error())
	case errors.Is(normalized, model.ErrUploadFileEmpty):
		response.BadRequest(w, normalized.Error())
	case errors.Is(normalized, model.ErrTaskState), errors.Is(normalized, model.ErrStrategyInvalid):
		response.BadRequest(w, normalized.Error())
	default:
		response.Error(w, normalized)
	}
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
