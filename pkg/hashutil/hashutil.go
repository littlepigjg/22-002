// Package hashutil 提供通用哈希与编码工具：SHA1、SHA256、Base64 编解码、CRC32 等。
package hashutil

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"hash/crc32"
)

// SHA1String 计算字符串 SHA1 并返回 hex。
func SHA1String(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SHA256String 计算字符串 SHA256 并返回 hex。
func SHA256String(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// CRC32String 计算 IEEE CRC32。
func CRC32String(s string) uint32 {
	return crc32.ChecksumIEEE([]byte(s))
}

// CRC32Bytes 计算字节切片 CRC32。
func CRC32Bytes(b []byte) uint32 {
	return crc32.ChecksumIEEE(b)
}

// Base64Encode 标准 Base64 编码。
func Base64Encode(src []byte) string {
	return base64.StdEncoding.EncodeToString(src)
}

// Base64Decode 标准 Base64 解码。
func Base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// Base64URLEncode URL 安全 Base64 编码（无 padding）。
func Base64URLEncode(src []byte) string {
	return base64.RawURLEncoding.EncodeToString(src)
}

// Base64URLDecode URL 安全 Base64 解码（无 padding）。
func Base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
