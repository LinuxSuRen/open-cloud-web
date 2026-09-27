package store

import (
	"crypto/sha256"
	"encoding/hex"
)

// 测试辅助：计算 SHA-256 hex（与生产 token 哈希方式一致）。
func sha256Sum(b []byte) []byte { h := sha256.Sum256(b); return h[:] }
func hexEncode(b []byte) string { return hex.EncodeToString(b) }
