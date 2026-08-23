// Package retry 提供指数退避 + 抖动重试工具。
package retry

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"time"
)

// Config 重试配置。
type Config struct {
	MaxAttempts int           // 最大尝试次数（含首次）
	Base        time.Duration // 首次退避基础时长
	MaxBackoff  time.Duration // 最大退避时长
	Factor      float64       // 退避系数
	Jitter      float64       // 抖动比例 0-1
	RetryIf     func(error) bool
	OnAttempt   func(attempt int, err error) // 每次尝试失败时的回调，用于收集错误历史
}

// DefaultConfig 返回合理默认配置。
func DefaultConfig() *Config {
	return &Config{
		MaxAttempts: 5,
		Base:        50 * time.Millisecond,
		MaxBackoff:  3 * time.Second,
		Factor:      2.0,
		Jitter:      0.2,
		RetryIf:     AlwaysRetry,
	}
}

// AlwaysRetry 默认永远重试（直到达到最大次数）。
func AlwaysRetry(err error) bool {
	return err != nil
}

// Retryable 标记错误可重试。
type Retryable interface {
	error
	Retryable() bool
}

// IsRetryable 判断错误是否可重试。
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var r Retryable
	if errors.As(err, &r) {
		return r.Retryable()
	}
	return true
}

// Do 执行 fn，若返回错误则按指数退避重试，直到成功、达到最大次数或 ctx 取消。
func Do(ctx context.Context, cfg *Config, fn func(ctx context.Context, attempt int) error) error {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.Base <= 0 {
		cfg.Base = 50 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 3 * time.Second
	}
	if cfg.Factor <= 0 {
		cfg.Factor = 2.0
	}
	if cfg.RetryIf == nil {
		cfg.RetryIf = AlwaysRetry
	}
	var lastErr error
	for i := 0; i < cfg.MaxAttempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn(ctx, i)
		if err == nil {
			return nil
		}
		lastErr = err
		if !cfg.RetryIf(err) {
			if cfg.OnAttempt != nil {
				cfg.OnAttempt(i, err)
			}
			return err
		}
		if i == cfg.MaxAttempts-1 {
			if cfg.OnAttempt != nil {
				cfg.OnAttempt(i, err)
			}
			break
		}
		backoff := nextBackoff(i, cfg)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr == nil {
		return errors.New("retry: unknown error")
	}
	return lastErr
}

func nextBackoff(attempt int, c *Config) time.Duration {
	mult := math.Pow(c.Factor, float64(attempt))
	d := time.Duration(float64(c.Base) * mult)
	if d <= 0 {
		d = c.Base
	}
	if d > c.MaxBackoff {
		d = c.MaxBackoff
	}
	if c.Jitter > 0 {
		amp := time.Duration(float64(d) * c.Jitter)
		if amp == 0 {
			return d
		}
		// rand usage: safe for non-cryptographic jitter.
		// nolint:gosec
		jitter := time.Duration(rand.Int63n(int64(amp)*2) - int64(amp))
		d += jitter
		if d <= 0 {
			d = 1 * time.Millisecond
		}
	}
	return d
}

// NewErr 构造一个可重试的错误。
func NewErr(msg string) error { return &retryErr{msg: msg, retry: true} }

// NewPermanentErr 构造不可重试的错误。
func NewPermanentErr(msg string) error { return &retryErr{msg: msg, retry: false} }

type retryErr struct {
	msg   string
	retry bool
	cause error
}

func (e *retryErr) Error() string  { return e.msg }
func (e *retryErr) Retryable() bool { return e.retry }
func (e *retryErr) Unwrap() error  { return e.cause }

// Wrap 将原错误转为可重试。
func Wrap(err error, retryable bool) error {
	if err == nil {
		return nil
	}
	return &retryErr{msg: err.Error(), retry: retryable, cause: err}
}
