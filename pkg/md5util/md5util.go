// Package md5util 封装 MD5 计算：字节、字符串、文件、流式计算等。
// 用于固件文件的 MD5 校验和与下载去重。
package md5util

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

// EmptyMD5 空内容的 MD5 值（用于占位与校验）。
const EmptyMD5 = "d41d8cd98f00b204e9800998ecf8427e"

// SumBytes 对字节切片计算 MD5，返回小写 hex 字符串。
func SumBytes(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// SumString 对字符串计算 MD5。
func SumString(s string) string {
	return SumBytes([]byte(s))
}

// SumReader 对 io.Reader 流式计算 MD5，返回小写 hex 字符串与读取字节数。
// 调用方负责关闭 Reader。
func SumReader(r io.Reader) (string, int64, error) {
	if r == nil {
		return "", 0, errors.New("md5util: nil reader")
	}
	h := md5.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// SumFile 对文件路径计算 MD5，返回小写 hex 字符串与文件大小。
func SumFile(path string) (string, int64, error) {
	if path == "" {
		return "", 0, errors.New("md5util: empty path")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	return SumReader(f)
}

// Validate 校验给定字节切片是否与期望 MD5 匹配。
func Validate(b []byte, expect string) bool {
	return SumBytes(b) == normalize(expect)
}

// ValidateFile 校验文件 MD5 是否匹配。
func ValidateFile(path, expect string) (bool, error) {
	got, _, err := SumFile(path)
	if err != nil {
		return false, err
	}
	return got == normalize(expect), nil
}

// normalize 将期望 MD5 处理为小写（忽略空）。
func normalize(s string) string {
	if len(s) == 32 {
		// 手动转小写避免导入 strings。
		b := []byte(s)
		for i := 0; i < len(b); i++ {
			if b[i] >= 'A' && b[i] <= 'F' {
				b[i] += 32
			}
		}
		return string(b)
	}
	return s
}

// NewHasher 返回一个可增量写入的 MD5 流式计算器。
// 调用者通过 Write 写入数据，完成后调用 Sum() 获取 hex 字符串。
type Hasher struct {
	h [md5.Size]byte
	w interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
		Reset()
	}
}

// NewHasher 创建 MD5 流式计算器。
func NewHasher() *Hasher {
	h := md5.New()
	return &Hasher{w: h}
}

// Write 写入数据。
func (m *Hasher) Write(p []byte) (int, error) {
	return m.w.Write(p)
}

// Sum 返回当前 MD5 hex 字符串。
func (m *Hasher) Sum() string {
	s := m.w.Sum(nil)
	return hex.EncodeToString(s)
}

// Reset 重置内部状态。
func (m *Hasher) Reset() {
	m.w.Reset()
	_ = m.h
}
