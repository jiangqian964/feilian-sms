// Package store 提供 SQLite 持久化与通道密钥的信封加密能力。
//
// secretbox 使用 AES-256-GCM：每次加密生成随机 96bit nonce，
// 落库格式为 base64(nonce || ciphertext||tag)，主密钥（data key）
// 不进库、不进 Git。
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// newGCM 以 32 字节密钥构造 AES-256-GCM。
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("构造 AES cipher 失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("构造 GCM 失败: %w", err)
	}
	return gcm, nil
}

// Encrypt 加密明文，返回 base64(nonce + 密文)。
func Encrypt(key, plaintext []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密 Encrypt 产出的密文；密钥错误、密文损坏或被篡改都会失败。
func Decrypt(key []byte, encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("密文 base64 解码失败: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns+1 {
		return nil, errors.New("密文长度不足，缺少 nonce 或正文")
	}
	nonce, ciphertext := raw[:ns], raw[ns:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("GCM 解密失败（密钥不匹配或密文被篡改）: %w", err)
	}
	return plaintext, nil
}
