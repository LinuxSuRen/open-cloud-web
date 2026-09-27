package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

// minPasswordLen 本地用户密码最短长度（创建时校验，密码只以 bcrypt 哈希落库）。
const minPasswordLen = 8

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.Store.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

type createUserReq struct {
	Username       string `json:"username"`
	DisplayName    string `json:"displayName"`
	Email          string `json:"email"`
	Role           string `json:"role"`
	MaxDurationSec int64  `json:"maxDurationSec"`
	Password       string `json:"password"`
}

// createUser 管理员创建本地用户；密码仅以 bcrypt 哈希存储，绝不回显。
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validUsername(req.Username) {
		writeError(w, http.StatusBadRequest, "username must match ^[a-zA-Z0-9_-]{2,32}$")
		return
	}
	if len(req.Password) < minPasswordLen || len(req.Password) > 72 {
		writeError(w, http.StatusBadRequest, "password must be 8-72 chars")
		return
	}
	if len(req.DisplayName) > 64 || len(req.Email) > 254 {
		writeError(w, http.StatusBadRequest, "displayName/email too long")
		return
	}
	role := auth.RoleUser
	if req.Role != "" {
		if req.Role != string(auth.RoleAdmin) && req.Role != string(auth.RoleUser) {
			writeError(w, http.StatusBadRequest, "invalid role")
			return
		}
		role = auth.Role(req.Role)
	}
	if _, err := h.Store.GetUserByUsername(req.Username); err == nil {
		writeError(w, http.StatusConflict, "username already exists")
		return
	} else if !errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "failed to check username")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}
	now := h.t()
	u := &auth.User{
		Username:       req.Username,
		DisplayName:    req.DisplayName,
		Email:          req.Email,
		Role:           role,
		Status:         auth.StatusActive,
		Provider:       "local",
		MaxDurationSec: req.MaxDurationSec,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := h.Store.CreateUser(u); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}
	// _ = hash 由 store 层持久化（PasswordHash 字段由装配层映射；此处不落地明文）。
	h.storePasswordHash(u, string(hash))
	h.audit(auth.UserFromContext(r.Context()).ID, "user.create", req.Username)
	writeJSON(w, http.StatusCreated, u)
}

// PasswordHashSetter 是支持保存 bcrypt 哈希的可选 store 能力。
type PasswordHashSetter interface {
	SetUserPasswordHash(userID int64, hash string) error
}

func (h *Handler) storePasswordHash(u *auth.User, hash string) {
	if s, ok := h.Store.(PasswordHashSetter); ok {
		_ = s.SetUserPasswordHash(u.ID, hash)
	}
}

type patchUserReq struct {
	DisplayName    *string `json:"displayName"`
	Email          *string `json:"email"`
	Role           *string `json:"role"`
	Status         *string `json:"status"`
	MaxDurationSec *int64  `json:"maxDurationSec"`
}

func (h *Handler) patchUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req patchUserReq
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := h.Store.GetUser(id)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	if req.DisplayName != nil {
		if len(*req.DisplayName) > 64 {
			writeError(w, http.StatusBadRequest, "displayName too long")
			return
		}
		u.DisplayName = *req.DisplayName
	}
	if req.Email != nil {
		if len(*req.Email) > 254 {
			writeError(w, http.StatusBadRequest, "email too long")
			return
		}
		u.Email = *req.Email
	}
	if req.Role != nil {
		if *req.Role != string(auth.RoleAdmin) && *req.Role != string(auth.RoleUser) {
			writeError(w, http.StatusBadRequest, "invalid role")
			return
		}
		u.Role = auth.Role(*req.Role)
	}
	if req.Status != nil {
		if *req.Status != string(auth.StatusActive) && *req.Status != string(auth.StatusDisabled) {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		u.Status = auth.UserStatus(*req.Status)
	}
	if req.MaxDurationSec != nil {
		if *req.MaxDurationSec < 0 {
			writeError(w, http.StatusBadRequest, "maxDurationSec must be >= 0")
			return
		}
		u.MaxDurationSec = *req.MaxDurationSec
	}
	u.UpdatedAt = h.t()
	if err := h.Store.UpdateUser(u); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	h.audit(auth.UserFromContext(r.Context()).ID, "user.update", u.Username)
	writeJSON(w, http.StatusOK, u)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	caller := auth.UserFromContext(r.Context())
	if caller.ID == id {
		writeError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	if err := h.Store.DeleteUser(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}
	h.audit(caller.ID, "user.delete", "")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := h.Store.ListAuditLogs(500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list audit logs")
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (h *Handler) audit(userID int64, action, detail string) {
	if strings.Contains(action, "password") {
		detail = "[redacted]"
	}
	_ = h.Store.CreateAuditLog(&AuditLog{UserID: userID, Action: action, Detail: detail, CreatedAt: h.t()})
}
