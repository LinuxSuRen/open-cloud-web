// 用户名/密码登录（本地用户；飞书用户请走 OAuth）。
// 带按用户名维度的失败锁定：5 次失败后锁定 5 分钟，缓解暴力破解。
package api

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

const (
	loginMaxFailures = 5
	loginLockout     = 5 * time.Minute
)

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginAttempt struct {
	failures  int
	lockedTil time.Time
}

// loginLimiter 进程内失败计数器（多副本部署时应换成共享存储）。
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
}

var limiter = &loginLimiter{attempts: map[string]*loginAttempt{}}

func (l *loginLimiter) locked(username string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.attempts[username]
	return ok && now.Before(a.lockedTil)
}

func (l *loginLimiter) recordFailure(username string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.attempts[username]
	if a == nil {
		a = &loginAttempt{}
		l.attempts[username] = a
	} else if !a.lockedTil.IsZero() && now.After(a.lockedTil) {
		// 锁定期已过：清零重新计数。
		a = &loginAttempt{}
		l.attempts[username] = a
	}
	a.failures++
	if a.failures >= loginMaxFailures {
		a.lockedTil = now.Add(loginLockout)
		a.failures = 0
	}
}

func (l *loginLimiter) reset(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, username)
}

// login 处理 POST /api/v1/auth/login {username, password} → JWT。
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	now := h.t()
	if limiter.locked(req.Username, now) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, retry later")
		return
	}
	u, err := h.Store.GetUserByUsername(req.Username)
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			// 与密码错误保持同一响应，避免用户名枚举。
			limiter.recordFailure(req.Username, now)
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	if u.Status != auth.StatusActive || u.Provider != "local" || u.PasswordHash == "" {
		limiter.recordFailure(req.Username, now)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		limiter.recordFailure(req.Username, now)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	limiter.reset(req.Username)
	token, err := h.Auth.IssueJWT(u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	h.audit(u.ID, "user.login", "password")
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": u})
}
