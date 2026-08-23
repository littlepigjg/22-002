package dto

import (
	"errors"
	"net/http"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/response"
)

func NormalizePage(pageNum, pageSize int) (int, int) {
	if pageNum <= 0 {
		pageNum = model.DefaultPageNum
	}
	if pageSize <= 0 {
		pageSize = model.DefaultPageSize
	}
	if pageSize > model.MaxPageSize {
		pageSize = model.MaxPageSize
	}
	return pageNum, pageSize
}

func Offset(pageNum, pageSize int) int {
	pn, ps := NormalizePage(pageNum, pageSize)
	return (pn - 1) * ps
}

func SafeInt32(n int64) int32 {
	if n > (1<<31 - 1) {
		return (1 << 31) - 1
	}
	if n < -(1 << 31) {
		return -(1 << 31)
	}
	return int32(n)
}

func Ptr[T any](v T) *T {
	return &v
}

func ValueOrDefault[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func ToPtrMap[K comparable, V any](in map[K]V) map[K]*V {
	if len(in) == 0 {
		return nil
	}
	out := make(map[K]*V, len(in))
	for k, v := range in {
		cp := v
		out[k] = &cp
	}
	return out
}

func FromPtrMap[K comparable, V any](in map[K]*V) map[K]V {
	if len(in) == 0 {
		return nil
	}
	out := make(map[K]V, len(in))
	for k, v := range in {
		var z V
		if v != nil {
			z = *v
		}
		out[k] = z
	}
	return out
}

func NormalizeBizError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, model.ErrNotFound):
		return wrapModel(err, http.StatusNotFound, response.CodeNotFound, model.ErrNotFound.Error())
	case errors.Is(err, model.ErrFirmwareNotFound):
		return wrapModel(err, http.StatusNotFound, response.CodeNotFound, model.ErrFirmwareNotFound.Error())
	case errors.Is(err, model.ErrDeviceNotFound):
		return wrapModel(err, http.StatusNotFound, response.CodeNotFound, model.ErrDeviceNotFound.Error())
	case errors.Is(err, model.ErrTaskNotFound):
		return wrapModel(err, http.StatusNotFound, response.CodeNotFound, model.ErrTaskNotFound.Error())
	case errors.Is(err, model.ErrModelNotFound):
		return wrapModel(err, http.StatusNotFound, response.CodeNotFound, model.ErrModelNotFound.Error())
	case errors.Is(err, model.ErrConflict):
		return wrapModel(err, http.StatusConflict, response.CodeConflict, model.ErrConflict.Error())
	case errors.Is(err, model.ErrInvalidParam):
		return wrapModel(err, http.StatusBadRequest, response.CodeBadRequest, model.ErrInvalidParam.Error())
	case errors.Is(err, model.ErrUnauthorized):
		return wrapModel(err, http.StatusUnauthorized, response.CodeUnauthorized, model.ErrUnauthorized.Error())
	case errors.Is(err, model.ErrForbidden):
		return wrapModel(err, http.StatusForbidden, response.CodeForbidden, model.ErrForbidden.Error())
	case errors.Is(err, model.ErrFirmwareNotPublished):
		return wrapModel(err, http.StatusBadRequest, response.CodeBadRequest, model.ErrFirmwareNotPublished.Error())
	case errors.Is(err, model.ErrAlreadyRegistered):
		return wrapModel(err, http.StatusConflict, response.CodeConflict, model.ErrAlreadyRegistered.Error())
	case errors.Is(err, model.ErrUploadTooLarge):
		return wrapModel(err, http.StatusRequestEntityTooLarge, response.CodeBadRequest, model.ErrUploadTooLarge.Error())
	case errors.Is(err, model.ErrUploadFileEmpty):
		return wrapModel(err, http.StatusBadRequest, response.CodeBadRequest, model.ErrUploadFileEmpty.Error())
	case errors.Is(err, model.ErrTaskState), errors.Is(err, model.ErrStrategyInvalid):
		return wrapModel(err, http.StatusBadRequest, response.CodeBadRequest, err.Error())
	default:
		return response.SafeWrap(err, http.StatusInternalServerError, response.CodeInternal, err.Error())
	}
}

func wrapModel(cause error, httpCode int, code response.Code, fallbackMsg string) error {
	return response.SafeWrap(cause, httpCode, code, fallbackMsg)
}

func NormalizeMessage(err error) string {
	return response.ExtractMessage(err)
}

func RequireNotNil(err error) error {
	if err == nil {
		return response.SafeWrap(nil, http.StatusInternalServerError, response.CodeInternal, "unexpected nil error")
	}
	return err
}
