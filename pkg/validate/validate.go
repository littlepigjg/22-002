// Package validate 提供通用参数校验器：非空、长度、范围、枚举、正则等。
package validate

import (
	"errors"
	"regexp"
	"strings"
	"sync"
)

// Validator 校验器函数，失败返回 error。
type Validator func() error

// Run 依次执行校验器，返回首个错误。
func Run(validators ...Validator) error {
	for _, v := range validators {
		if v == nil {
			continue
		}
		if err := v(); err != nil {
			return err
		}
	}
	return nil
}

// Required 要求字符串非空。
func Required(name, value string) Validator {
	return func() error {
		if strings.TrimSpace(value) == "" {
			return errors.New(name + " is required")
		}
		return nil
	}
}

// RequiredID 要求 ID 字符串非空且具备最小长度。
func RequiredID(name, value string) Validator {
	return func() error {
		v := strings.TrimSpace(value)
		if v == "" {
			return errors.New(name + " is required")
		}
		if len(v) < 2 {
			return errors.New(name + " too short, min length is 2")
		}
		if len(v) > 64 {
			return errors.New(name + " too long, max length is 64")
		}
		return nil
	}
}

// MinLen 要求字符串最小长度。
func MinLen(name, value string, min int) Validator {
	return func() error {
		if len(value) < min {
			return errors.New(name + " min length is " + itoa(min))
		}
		return nil
	}
}

// MaxLen 要求字符串最大长度。
func MaxLen(name, value string, max int) Validator {
	return func() error {
		if len(value) > max {
			return errors.New(name + " max length is " + itoa(max))
		}
		return nil
	}
}

// BetweenLen 要求字符串长度区间。
func BetweenLen(name, value string, min, max int) Validator {
	return func() error {
		if l := len(value); l < min || l > max {
			return errors.New(name + " length must between " + itoa(min) + " and " + itoa(max))
		}
		return nil
	}
}

// InRange 要求整数在区间内（含边界）。
func InRange(name string, value, min, max int) Validator {
	return func() error {
		if value < min || value > max {
			return errors.New(name + " must between " + itoa(min) + " and " + itoa(max))
		}
		return nil
	}
}

// InRange64 要求 int64 在区间内。
func InRange64(name string, value, min, max int64) Validator {
	return func() error {
		if value < min || value > max {
			return errors.New(name + " must between " + i64toa(min) + " and " + i64toa(max))
		}
		return nil
	}
}

// InEnum 要求值属于枚举列表。
func InEnum(name, value string, enum []string) Validator {
	return func() error {
		for _, e := range enum {
			if e == value {
				return nil
			}
		}
		return errors.New(name + " must be one of " + strings.Join(enum, ","))
	}
}

// MatchRegex 要求值匹配正则。
func MatchRegex(name, value string, re *regexp.Regexp) Validator {
	return func() error {
		if !re.MatchString(value) {
			return errors.New(name + " format invalid")
		}
		return nil
	}
}

// GreaterThan 要求 value > min。
func GreaterThan(name string, value, min int64) Validator {
	return func() error {
		if value <= min {
			return errors.New(name + " must greater than " + i64toa(min))
		}
		return nil
	}
}

// LessThanOrEqual 要求 value <= max。
func LessThanOrEqual(name string, value, max int64) Validator {
	return func() error {
		if value > max {
			return errors.New(name + " must be less than or equal to " + i64toa(max))
		}
		return nil
	}
}

// NotNilSlice 要求切片非 nil 且非空。
func NotNilSlice[T any](name string, v []T) Validator {
	return func() error {
		if len(v) == 0 {
			return errors.New(name + " is required and can not be empty")
		}
		return nil
	}
}

// itoa 小型整数转字符串。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var sb strings.Builder
	for n > 0 {
		sb.WriteByte(byte('0' + n%10))
		n /= 10
	}
	if neg {
		sb.WriteByte('-')
	}
	s := sb.String()
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func i64toa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var sb strings.Builder
	for n > 0 {
		sb.WriteByte(byte('0' + n%10))
		n /= 10
	}
	if neg {
		sb.WriteByte('-')
	}
	s := sb.String()
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// 防止引入未使用变量。
var _ = sync.Once{}
