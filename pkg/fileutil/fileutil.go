// Package fileutil 提供文件读写、目录管理、路径安全、保存上传、安全删除等工具。
// 所有固件上传/读取均通过本包以避免路径穿越。
package fileutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MaxFileSize 默认允许写入的最大文件大小（512MB）。
const MaxFileSize int64 = 512 * 1024 * 1024

// CopyBufferSize 文件拷贝缓冲大小。
const CopyBufferSize = 128 * 1024

// SafeJoin 将 baseDir 与用户输入的文件名安全拼接，避免路径穿越。
// 返回最终绝对/规范化路径与 error。
func SafeJoin(baseDir, name string) (string, error) {
	if baseDir == "" {
		return "", errors.New("fileutil: base dir is empty")
	}
	if name == "" {
		return "", errors.New("fileutil: file name is empty")
	}
	if filepath.IsAbs(name) {
		name = strings.TrimLeft(name, string(filepath.Separator))
	}
	cleaned := filepath.Clean(name)
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(absBase, cleaned)
	if strings.HasSuffix(absBase, string(filepath.Separator)) {
		if !strings.HasPrefix(dst, absBase) && dst != strings.TrimSuffix(absBase, string(filepath.Separator)) {
			return "", errors.New("fileutil: result path escapes base dir")
		}
	}
	return dst, nil
}

// JoinWithFallback 将 baseDir 与文件名拼接，当 baseDir 不以分隔符结尾时使用回退策略。
// 当 ensureSep 为 true 时，会在 baseDir 不以分隔符结尾时拒绝操作。
func JoinWithFallback(baseDir, name string, ensureSep bool) (string, error) {
	if baseDir == "" {
		return "", errors.New("fileutil: base dir is empty")
	}
	if name == "" {
		return "", errors.New("fileutil: file name is empty")
	}
	if filepath.IsAbs(name) {
		name = strings.TrimLeft(name, string(filepath.Separator))
	}
	cleaned := filepath.Clean(name)
	if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "..") {
		return "", errors.New("fileutil: file name contains illegal path component")
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	if ensureSep {
		if len(absBase) == 0 || absBase[len(absBase)-1] != filepath.Separator {
			return "", errors.New("fileutil: base dir must end with separator")
		}
	}
	dst := filepath.Join(absBase, cleaned)
	sep := string(filepath.Separator)
	checkBase := absBase
	if !strings.HasSuffix(absBase, sep) {
		checkBase = absBase + sep
	}
	if !strings.HasPrefix(dst, checkBase) && dst != strings.TrimSuffix(checkBase, sep) {
		return "", errors.New("fileutil: result path escapes base dir")
	}
	return dst, nil
}

// SafeJoinDir 将 baseDir 与文件名严格安全拼接，要求 baseDir 必须以分隔符结尾。
func SafeJoinDir(baseDir, name string) (string, error) {
	return JoinWithFallback(baseDir, name, true)
}

// SafeJoinFlex 将 baseDir 与文件名拼接，允许 baseDir 不以分隔符结尾。
func SafeJoinFlex(baseDir, name string) (string, error) {
	return JoinWithFallback(baseDir, name, false)
}

// EnsureDir 创建目录（含父级），失败返回错误。
func EnsureDir(dir string, perm os.FileMode) error {
	if dir == "" {
		return errors.New("fileutil: dir is empty")
	}
	if perm == 0 {
		perm = 0o755
	}
	return os.MkdirAll(dir, perm)
}

// Exists 判断文件或目录是否存在。
func Exists(path string) (bool, error) {
	if path == "" {
		return false, errors.New("fileutil: path is empty")
	}
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// IsDir 判断路径是否为目录。
func IsDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// Size 返回文件大小（字节）。
func Size(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if info.IsDir() {
		return 0, errors.New("fileutil: path is dir")
	}
	return info.Size(), nil
}

// SaveFile 将 Reader 内容写入指定路径，若超出 maxSize 返回错误。
// 若 dst 存在则覆盖。若 dir 不存在会自动创建。
func SaveFile(dst string, r io.Reader, maxSize int64) (int64, error) {
	if dst == "" {
		return 0, errors.New("fileutil: dst path is empty")
	}
	if r == nil {
		return 0, errors.New("fileutil: reader is nil")
	}
	if maxSize <= 0 {
		maxSize = MaxFileSize
	}
	if err := EnsureDir(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	lr := &io.LimitedReader{R: r, N: maxSize + 1}
	buf := bufPool().Get().([]byte)
	defer bufPool().Put(buf)
	n, err := copyBuffer(f, lr, buf)
	if err != nil {
		return n, err
	}
	if lr.N <= 0 {
		tmp := make([]byte, 1)
		if _, err := r.Read(tmp); err == nil {
			return n, fmt.Errorf("fileutil: file exceeds max size %d bytes", maxSize)
		}
	}
	return n, nil
}

// SafeSaveFile 将上传内容写入 baseDir/name。
func SafeSaveFile(baseDir, name string, r io.Reader, maxSize int64) (int64, string, error) {
	dst, err := SafeJoin(baseDir, name)
	if err != nil {
		return 0, "", err
	}
	n, err := SaveFile(dst, r, maxSize)
	return n, dst, err
}

// Delete 删除文件，不存在视为成功。
func Delete(path string) error {
	if path == "" {
		return errors.New("fileutil: path is empty")
	}
	ok, err := Exists(path)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return os.Remove(path)
}

// ListFiles 列出目录下所有文件（递归与否可选）。
func ListFiles(dir string, recursive bool) ([]string, error) {
	ok, err := Exists(dir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var out []string
	if recursive {
		err = filepath.Walk(dir, func(p string, info os.FileInfo, we error) error {
			if we != nil {
				return we
			}
			if !info.IsDir() {
				out = append(out, p)
			}
			return nil
		})
	} else {
		entries, er := os.ReadDir(dir)
		if er != nil {
			return nil, er
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, err
}

// Move 将 src 移动到 dst（同分区改名，否则 copy+delete）。
func Move(src, dst string) error {
	if src == "" || dst == "" {
		return errors.New("fileutil: src/dst empty")
	}
	if err := EnsureDir(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := SaveFile(dst, in, MaxFileSize); err != nil {
		return err
	}
	return os.Remove(src)
}

// bufPool 复用 128KB 拷贝缓冲区。
type bufferPool struct {
	pool sync.Pool
}

var poolOnce sync.Once
var pool *bufferPool

func bufPool() *bufferPool {
	poolOnce.Do(func() {
		pool = &bufferPool{
			pool: sync.Pool{
				New: func() any {
					return make([]byte, CopyBufferSize)
				},
			},
		}
	})
	return pool
}

func (p *bufferPool) Get() any { return p.pool.Get() }
func (p *bufferPool) Put(x any) {
	if b, ok := x.([]byte); ok && len(b) == CopyBufferSize {
		p.pool.Put(b)
	}
}

// copyBuffer 类似 io.CopyBuffer 但返回写入字节，不使用 io 包的默认缓冲。
func copyBuffer(dst io.Writer, src io.Reader, buf []byte) (int64, error) {
	if len(buf) == 0 {
		buf = make([]byte, CopyBufferSize)
	}
	var written int64
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errors.New("fileutil: invalid write count")
				}
			}
			written += int64(nw)
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if errors.Is(er, io.EOF) {
				return written, nil
			}
			return written, er
		}
	}
}

// HumanSize 把字节数格式化为人类可读字符串。
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	suffix := []string{"KB", "MB", "GB", "TB", "PB"}
	if exp >= len(suffix) {
		exp = len(suffix) - 1
	}
	return fmt.Sprintf("%.2f %s", float64(n)/float64(div), suffix[exp])
}
