// Package secrets 提供云账号 Secret 的 AES-256-GCM 加解密。
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

func gcm(key string) (cipher.AEAD, error) {
	block, err := aes.NewCipher(deriveKey(key))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// deriveKey 把任意长度的配置密钥归一为 32 字节 AES-256 密钥。
func deriveKey(key string) []byte {
	h := sha256.Sum256([]byte(key))
	return h[:]
}

// Encrypt 加密明文，返回 base64(nonce+ciphertext)。
func Encrypt(plain, key string) (string, error) {
	a, err := gcm(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(a.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Decrypt 解密 Encrypt 的输出。
func Decrypt(enc, key string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("secrets: %w", err)
	}
	a, err := gcm(key)
	if err != nil {
		return "", err
	}
	if len(raw) < a.NonceSize() {
		return "", fmt.Errorf("secrets: ciphertext too short")
	}
	nonce, ct := raw[:a.NonceSize()], raw[a.NonceSize():]
	pt, err := a.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("secrets: %w", err)
	}
	return string(pt), nil
}
