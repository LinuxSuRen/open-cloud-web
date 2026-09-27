package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// PATPrefix 是 PAT 明文前缀，用于中间件识别令牌类型。
const PATPrefix = "pat_ocw_"

// GeneratePAT 生成明文 PAT：pat_ocw_ + 32 字节随机数的 base64url（无填充）。
// 存储时只落 SHA-256 哈希，明文仅在创建时返回一次。
func GeneratePAT() (plaintext, tokenHash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth: generate pat: %w", err)
	}
	plaintext = PATPrefix + base64.RawURLEncoding.EncodeToString(buf)
	return plaintext, HashToken(plaintext), nil
}

// HashToken 计算 PAT 明文的 SHA-256 十六进制哈希。
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// IsPAT 判断令牌是否为 PAT 格式。
func IsPAT(token string) bool {
	return len(token) > len(PATPrefix) && token[:len(PATPrefix)] == PATPrefix
}
