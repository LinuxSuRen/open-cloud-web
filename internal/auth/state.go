package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// StateManager 签发并校验 OAuth state（随机数 + HMAC-SHA256），防 CSRF。
// 校验使用常量时间比较。
type StateManager struct {
	secret []byte
}

// NewStateManager 用给定 secret 构造（secret 至少 16 字节）。
func NewStateManager(secret string) *StateManager {
	return &StateManager{secret: []byte(secret)}
}

// New 生成 state：随机 16 字节 hex + "." + HMAC hex。
func (s *StateManager) New() (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	n := hex.EncodeToString(nonce)
	return n + "." + s.mac(n), nil
}

// Validate 常量时间校验 state 是否为本实例签发。
func (s *StateManager) Validate(state string) bool {
	for i := 0; i < len(state); i++ {
		if state[i] == '.' {
			nonce, mac := state[:i], state[i+1:]
			return hmac.Equal([]byte(s.mac(nonce)), []byte(mac))
		}
	}
	return false
}

func (s *StateManager) mac(nonce string) string {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(nonce))
	return hex.EncodeToString(h.Sum(nil))
}
