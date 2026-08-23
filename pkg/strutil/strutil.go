// Package strutil 提供通用字符串辅助：判空、截断、连接、数字/IP/MAC 校验等。
package strutil

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var (
	// ipRegex IPv4 正则。
	ipRegex     *regexp.Regexp
	ipRegexOnce sync.Once
	// macRegex MAC 地址正则（支持冒号/短横线分隔）。
	macRegex     *regexp.Regexp
	macRegexOnce sync.Once
	// versionRegex 语义化版本号正则。
	versionRegex     *regexp.Regexp
	versionRegexOnce sync.Once
)

func getIPRegex() *regexp.Regexp {
	ipRegexOnce.Do(func() {
		ipRegex = regexp.MustCompile(`^((25[0-5]|2[0-4]\d|[01]?\d?\d)\.){3}(25[0-5]|2[0-4]\d|[01]?\d?\d)$`)
	})
	return ipRegex
}

func getMACRegex() *regexp.Regexp {
	macRegexOnce.Do(func() {
		macRegex = regexp.MustCompile(`^([0-9A-Fa-f]{2}[:-]){5}([0-9A-Fa-f]{2})$`)
	})
	return macRegex
}

func getVersionRegex() *regexp.Regexp {
	versionRegexOnce.Do(func() {
		versionRegex = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[\w.]+)?(\+[\w.]+)?$`)
	})
	return versionRegex
}

// IsEmpty 判断字符串是否为空或仅空白字符。
func IsEmpty(s string) bool {
	return strings.TrimSpace(s) == ""
}

// NotEmpty 非空判断。
func NotEmpty(s string) bool { return !IsEmpty(s) }

// DefaultIfEmpty 空字符串返回默认值。
func DefaultIfEmpty(s, def string) string {
	if IsEmpty(s) {
		return def
	}
	return s
}

// Truncate 截断字符串到 n 个 rune；若有截断附加省略号。
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// Join 以 sep 拼接非空字符串段（自动忽略空串）。
func Join(sep string, parts ...string) string {
	w := make([]string, 0, len(parts))
	for _, p := range parts {
		if NotEmpty(p) {
			w = append(w, p)
		}
	}
	return strings.Join(w, sep)
}

// ContainsI 忽略大小写包含判断。
func ContainsI(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// EqualI 忽略大小写相等判断。
func EqualI(a, b string) bool {
	return strings.EqualFold(a, b)
}

// IsIPv4 判断是否为合法 IPv4。
func IsIPv4(s string) bool {
	return getIPRegex().MatchString(s)
}

// IsMAC 判断是否为合法 MAC 地址。
func IsMAC(s string) bool {
	return getMACRegex().MatchString(s)
}

// IsSemVer 判断是否为合法语义化版本号（如 v1.2.3、1.0.0-beta.1）。
func IsSemVer(s string) bool {
	return getVersionRegex().MatchString(s)
}

// CompareVersion 比较两个语义化版本号，返回 -1/0/1。
// 若格式不合法，返回 0 并通过 bool 指示解析失败。
func CompareVersion(a, b string) (int, bool) {
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	if len(aParts) != 3 || len(bParts) != 3 {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		ai, err := strconv.Atoi(aParts[i])
		if err != nil {
			return 0, false
		}
		bi, err := strconv.Atoi(bParts[i])
		if err != nil {
			return 0, false
		}
		if ai < bi {
			return -1, true
		}
		if ai > bi {
			return 1, true
		}
	}
	return 0, true
}

// ToInt 字符串转 int，失败返回 0 与 false。
func ToInt(s string) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return v, true
}

// ToInt64 字符串转 int64，失败返回 0 与 false。
func ToInt64(s string) (int64, bool) {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// ToFloat64 字符串转 float64。
func ToFloat64(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// Itoa 整数转字符串。
func Itoa(n int) string { return strconv.Itoa(n) }

// I64toa int64 转字符串。
func I64toa(n int64) string { return strconv.FormatInt(n, 10) }

// F64toa float64 转字符串，保留 decimals 位小数。
func F64toa(f float64, decimals int) string {
	return strconv.FormatFloat(f, 'f', decimals, 64)
}

// RemoveDuplicates 对字符串切片去重，保持首次出现顺序。
func RemoveDuplicates(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
