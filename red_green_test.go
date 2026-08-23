package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"firmware-upgrade/internal/handler"
	"firmware-upgrade/pkg/logger"
)

// spyResponseWriter 用于追踪 WriteHeader 调用次数
type spyResponseWriter struct {
	http.ResponseWriter
	WriteCount int
	LastCode   int
}

func (s *spyResponseWriter) WriteHeader(code int) {
	s.WriteCount++
	s.LastCode = code
	s.ResponseWriter.WriteHeader(code)
}

func TestRedGreen(t *testing.T) {
	// 初始化 logger 防止 panic
	logger.Init("debug")

	// 构造一个 handler，先写 Header 再 panic
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("test panic")
	})

	// 使用 RecoveryMiddleware 包装
	h := handler.RecoveryMiddleware(panicHandler)

	// 使用 spy writer 来追踪 WriteHeader 调用
	recorder := httptest.NewRecorder()
	spy := &spyResponseWriter{ResponseWriter: recorder}

	// 发送请求
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	h.ServeHTTP(spy, req)

	// 验证：如果 WriteHeader 被调用了超过 1 次，说明存在 superfluous write 缺陷
	if spy.WriteCount > 1 {
		t.Logf("RED (红灯，缺陷未修复): WriteHeader 被调用了 %d 次，出现 superfluous response.WriteHeader 问题", spy.WriteCount)
		t.FailNow()
	} else {
		t.Logf("GREEN (绿灯，缺陷已修复): WriteHeader 仅被调用了 %d 次", spy.WriteCount)
	}
}
