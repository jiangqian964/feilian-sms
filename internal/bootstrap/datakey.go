package bootstrap

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const dataKeyLen = 32

// ResolveDataKey 解析通道密钥加密用的 32 字节主密钥：
//  1. secrets.data_key（内联 base64，通常来自环境变量）优先；
//  2. 其次读取 secrets.key_path（默认 <DB 同目录>/data.key）；
//  3. 文件不存在时首启自动生成，目录 0750、文件 0600。
func ResolveDataKey(c *Config) ([]byte, error) {
	if c.Secrets.DataKey != "" {
		return decodeKeyMaterial(c.Secrets.DataKey)
	}

	path := c.Secrets.KeyPath
	if path == "" {
		path = filepath.Join(filepath.Dir(c.SQLite.Path), "data.key")
	}

	if raw, err := os.ReadFile(path); err == nil {
		key, derr := decodeKeyMaterial(strings.TrimSpace(string(raw)))
		if derr != nil {
			return nil, fmt.Errorf("数据密钥文件 %s 不可用: %w", path, derr)
		}
		// 权限若被放大，运行中收口到 0600。
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm() != 0o600 {
			_ = os.Chmod(path, 0o600)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取数据密钥文件 %s 失败: %w", path, err)
	}

	key := make([]byte, dataKeyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("生成数据密钥失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("创建密钥目录失败: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("写入数据密钥文件 %s 失败: %w", path, err)
	}
	return key, nil
}

// decodeKeyMaterial 接受 base64 编码的 32 字节密钥；
// 若内容不是合法 base64，则按原始字节处理（仍要求恰好 32 字节）。
func decodeKeyMaterial(s string) ([]byte, error) {
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
		if len(raw) != dataKeyLen {
			return nil, fmt.Errorf("base64 解码后密钥长度 %d，要求 %d 字节", len(raw), dataKeyLen)
		}
		return raw, nil
	}
	if len(s) == dataKeyLen {
		return []byte(s), nil
	}
	return nil, fmt.Errorf("密钥必须是 base64 编码的 %d 字节（或 %d 字节原始字符串）", dataKeyLen, dataKeyLen)
}
