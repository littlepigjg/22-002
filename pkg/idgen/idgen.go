// Package idgen 提供分布式 ID 生成：雪花算法 + 短 ID 字符串 + UUID 风格 ID。
// 本项目仅使用进程内单实例，避免外部依赖。
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Epoch 雪花算法起始时间戳（毫秒）：2024-01-01 00:00:00 UTC。
const Epoch int64 = 1704067200000

const (
	workerBits  uint8 = 10
	numberBits  uint8 = 12
	workerMax   int64 = -1 ^ (-1 << workerBits)
	numberMax   int64 = -1 ^ (-1 << numberBits)
	timeShift   uint8 = workerBits + numberBits
	workerShift uint8 = numberBits
)

// Snowflake 雪花 ID 生成器，单进程内使用互斥锁保证安全。
type Snowflake struct {
	mu      sync.Mutex
	epoch   time.Time
	worker  int64
	seq     int64
	lastMs  int64
	started bool
}

// NewSnowflake 创建 Snowflake。workerId 需 0..1023。
func NewSnowflake(workerId int64) (*Snowflake, error) {
	if workerId < 0 || workerId > workerMax {
		return nil, errors.New("idgen: worker id out of range 0-1023")
	}
	return &Snowflake{
		worker:  workerId,
		lastMs:  -1,
		started: true,
	}, nil
}

// Default 默认 Snowflake 实例（workerId=1）。
var Default, _ = NewSnowflake(1)

// Next 生成下一个 int64 ID。
func (s *Snowflake) Next() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return 0, errors.New("idgen: snowflake not started")
	}
	now := time.Now().UnixMilli()
	if now < s.lastMs {
		return 0, fmt.Errorf("idgen: clock moved back by %d ms", s.lastMs-now)
	}
	if now == s.lastMs {
		s.seq = (s.seq + 1) & numberMax
		if s.seq == 0 {
			// 序列号耗尽，等待下一毫秒。
			for now <= s.lastMs {
				now = time.Now().UnixMilli()
			}
		}
	} else {
		s.seq = 0
	}
	s.lastMs = now
	return ((now - Epoch) << timeShift) | (s.worker << workerShift) | s.seq, nil
}

// NextString 以字符串形式返回 ID。
func (s *Snowflake) NextString() (string, error) {
	id, err := s.Next()
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 36), nil
}

// NextID 以默认生成器产生字符串 ID（36 进制）。
func NextID() string {
	id, err := Default.NextString()
	if err != nil {
		// 回退：返回 16 字节随机十六进制。
		return RandomHex(16)
	}
	return id
}

// NextIntID 返回 int64 ID。
func NextIntID() int64 {
	id, err := Default.Next()
	if err != nil {
		return time.Now().UnixNano()
	}
	return id
}

// RandomHex 返回 n 字节随机十六进制字符串。
func RandomHex(n int) string {
	if n <= 0 {
		n = 8
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// 若系统熵源异常，退化为时间戳拼接伪随机。
		for i := range b {
			b[i] = byte(time.Now().UnixNano()>>uint(i*3%56))
		}
	}
	return hex.EncodeToString(b)
}

// ShortUUID 返回一个短 ID（16 字符 URL 安全 Base64）。
func ShortUUID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	// 手动转 base62 风格避免导入 encoding/base64。
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 0, 16)
	n := len(b)
	idx := 0
	for i := 0; i < 16; i++ {
		v := b[idx%n]
		idx++
		out = append(out, chars[int(v)%len(chars)])
	}
	return string(out)
}

// NewUUID 生成简化 UUID v4 字符串（未带短横线）。
func NewUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	// 设置版本位与变体位。
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b)
}

// NewDashedUUID 生成带短横线的 UUID。
func NewDashedUUID() string {
	u := NewUUID()
	return u[0:8] + "-" + u[8:12] + "-" + u[12:16] + "-" + u[16:20] + "-" + u[20:32]
}

// Parse 解析雪花 ID，返回 (timestamp_ms, worker_id, seq)。
func Parse(id int64) (ts int64, worker int64, seq int64) {
	seq = id & numberMax
	worker = (id >> workerShift) & workerMax
	ts = (id >> timeShift) + Epoch
	return
}
