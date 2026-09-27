package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// UserStore 是认证中间件需要的 store 子集（Go 隐式接口，隔离并行开发）。
type UserStore interface {
	GetUser(id int64) (*User, error)
	GetPATByHash(tokenHash string) (*PAT, error)
}

type ctxKey int

const userCtxKey ctxKey = 1

// UserFromContext 取出中间件放入的已认证用户；未认证返回 nil。
func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(userCtxKey).(*User)
	return u
}

// Authenticate 返回 HTTP 认证中间件：从 Authorization: Bearer 取令牌，
// PAT 格式先按哈希查库，否则回退 JWT 校验；通过后把 *User 放入 request context。
func Authenticate(store UserStore, mgr Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, err := authenticate(r, store, mgr)
			if err != nil {
				writeAuthError(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCtxKey, u)))
		})
	}
}

func authenticate(r *http.Request, store UserStore, mgr Manager) (*User, error) {
	h := r.Header.Get("Authorization")
	const scheme = "Bearer "
	if !strings.HasPrefix(h, scheme) {
		return nil, errors.New("missing bearer token")
	}
	token := strings.TrimSpace(h[len(scheme):])
	if token == "" {
		return nil, errors.New("empty bearer token")
	}
	now := time.Now()
	if IsPAT(token) {
		pat, err := store.GetPATByHash(HashToken(token))
		if err == nil {
			if pat.Expired(now) {
				return nil, errors.New("pat expired")
			}
			return loadActiveUser(store, pat.UserID)
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, errors.New("pat lookup failed")
		}
		return nil, errors.New("invalid pat")
	}
	claims, err := mgr.ParseJWT(token)
	if err != nil {
		return nil, errors.New("invalid jwt")
	}
	return loadActiveUser(store, claims.UID)
}

func loadActiveUser(store UserStore, id int64) (*User, error) {
	u, err := store.GetUser(id)
	if err != nil {
		return nil, errors.New("user not found")
	}
	if u.Status == StatusDisabled {
		return nil, errors.New("user disabled")
	}
	return u, nil
}

func writeAuthError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized: " + err.Error()})
}
