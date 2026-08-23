// Package timeutil 提供统一的时间工具：时间戳转换、格式化、UTC 时区处理、时间窗口计算等。
// 本项目所有涉及时间的模块统一使用本包，避免时区混乱和重复逻辑。
package timeutil

import (
	"sync"
	"time"
)

// Layout 定义项目内统一使用的时间格式（遵循 RFC3339 变体）。
const (
	Layout       = "2006-01-02 15:04:05"
	LayoutDate   = "2006-01-02"
	LayoutRFC3339 = time.RFC3339
)

var (
	// location 使用东八区（北京时间），可通过 SetTimezone 重新设置。
	location     *time.Location
	locationOnce sync.Once
	locationMu   sync.RWMutex
)

// defaultLocation 返回默认使用的时区（Asia/Shanghai）。
func defaultLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return loc
}

// init 初始化默认时区。
func init() {
	locationOnce.Do(func() {
		location = defaultLocation()
	})
}

// SetTimezone 设置全局时区。名称为 IANA 时区名（如 Asia/Shanghai），或 "UTC"、"Local"。
// 失败时返回 error 但保留旧值。
func SetTimezone(name string) error {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return err
	}
	locationMu.Lock()
	location = loc
	locationMu.Unlock()
	return nil
}

// Location 返回当前全局时区。
func Location() *time.Location {
	locationMu.RLock()
	defer locationMu.RUnlock()
	return location
}

// Now 返回当前带时区的时间。
func Now() time.Time {
	return time.Now().In(Location())
}

// NowSec 返回当前 Unix 时间戳（秒）。
func NowSec() int64 {
	return Now().Unix()
}

// NowMilli 返回当前 Unix 时间戳（毫秒）。
func NowMilli() int64 {
	return Now().UnixMilli()
}

// TodayStart 返回今天 00:00:00 的时间。
func TodayStart() time.Time {
	t := Now()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Location())
}

// TodayEnd 返回今天 23:59:59 的时间。
func TodayEnd() time.Time {
	return TodayStart().Add(24*time.Hour - time.Nanosecond)
}

// Format 按统一 Layout 格式化时间。
func Format(t time.Time) string {
	return t.In(Location()).Format(Layout)
}

// FormatDate 仅格式化日期部分。
func FormatDate(t time.Time) string {
	return t.In(Location()).Format(LayoutDate)
}

// Parse 按统一 Layout 解析字符串为时间（带时区）。
func Parse(s string) (time.Time, error) {
	return time.ParseInLocation(Layout, s, Location())
}

// ParseDate 仅解析日期部分。
func ParseDate(s string) (time.Time, error) {
	return time.ParseInLocation(LayoutDate, s, Location())
}

// FromSec 将秒级时间戳转换为时间。
func FromSec(sec int64) time.Time {
	return time.Unix(sec, 0).In(Location())
}

// FromMilli 将毫秒级时间戳转换为时间。
func FromMilli(ms int64) time.Time {
	return time.UnixMilli(ms).In(Location())
}

// BeginningOfDay 返回给定时间当天起始时间。
func BeginningOfDay(t time.Time) time.Time {
	t = t.In(Location())
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Location())
}

// EndOfDay 返回给定时间当天结束时间。
func EndOfDay(t time.Time) time.Time {
	return BeginningOfDay(t).Add(24*time.Hour - time.Nanosecond)
}

// Between 判断目标时间是否在 [start, end] 区间内（含边界）。
func Between(target, start, end time.Time) bool {
	return !target.Before(start) && !target.After(end)
}

// DurationDays 返回 start 到 end 之间经过的完整天数（按自然日）。
func DurationDays(start, end time.Time) int {
	a := BeginningOfDay(start)
	b := BeginningOfDay(end)
	return int(b.Sub(a).Hours() / 24)
}

// AddDays 返回 t 加 n 天后的时间。
func AddDays(t time.Time, n int) time.Time {
	return t.AddDate(0, 0, n)
}

// DiffSeconds 返回 a 与 b 的秒数差（a-b）。
func DiffSeconds(a, b time.Time) float64 {
	return a.Sub(b).Seconds()
}

// Clock 定义一个可替换的时间源接口，便于单元测试。
type Clock interface {
	Now() time.Time
}

// RealClock 真实时钟。
type RealClock struct{}

// Now 返回当前真实时间。
func (RealClock) Now() time.Time { return Now() }

// FakeClock 可设置任意时间的测试时钟。
type FakeClock struct {
	mu sync.RWMutex
	t  time.Time
}

// NewFakeClock 创建一个基于初始时间的 FakeClock。
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{t: t} }

// Now 返回当前伪造时间。
func (c *FakeClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.t
}

// Set 设置新的伪造时间。
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// Add 为伪造时间增加 duration。
func (c *FakeClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
