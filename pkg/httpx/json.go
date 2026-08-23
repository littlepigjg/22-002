// Package httpx JSON 请求/响应辅助。
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// DecodeJSON 解码 JSON，支持最大大小。
func DecodeJSON(r *http.Request, dst interface{}, maxBytes int64) error {
	if r == nil || r.Body == nil {
		return errors.New("httpx: nil request or body")
	}
	if dst == nil {
		return errors.New("httpx: nil dst")
	}
	defer r.Body.Close()
	lr := &io.LimitedReader{R: r.Body, N: maxBytes}
	dec := json.NewDecoder(lr)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if lr.N <= 0 {
			return errors.New("httpx: request body too large")
		}
		return err
	}
	return nil
}

// WriteJSON 输出 JSON 响应。
func WriteJSON(w http.ResponseWriter, statusCode int, payload interface{}) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if payload == nil {
		return nil
	}
	return json.NewEncoder(w).Encode(payload)
}

// NoContent 输出 204。
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// Redirect 输出重定向。
func Redirect(w http.ResponseWriter, r *http.Request, url string, code int) {
	http.Redirect(w, r, url, code)
}
