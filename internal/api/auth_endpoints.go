package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
)

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, auth.UserFromContext(r.Context()))
}

// feishuLogin 返回飞书授权跳转地址（带防 CSRF state）。
func (h *Handler) feishuLogin(w http.ResponseWriter, r *http.Request) {
	if h.Feishu == nil || h.States == nil {
		writeError(w, http.StatusServiceUnavailable, "feishu login not configured")
		return
	}
	state, err := h.States.New()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate state")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorize_url": h.Feishu.AuthCodeURL(state)})
}

// feishuCallback 校验 state，用 code 换取用户信息，查/建用户并签发 JWT。
func (h *Handler) feishuCallback(w http.ResponseWriter, r *http.Request) {
	if h.Feishu == nil || h.States == nil {
		writeError(w, http.StatusServiceUnavailable, "feishu login not configured")
		return
	}
	q := r.URL.Query()
	state, code := q.Get("state"), q.Get("code")
	if state == "" || code == "" || !h.States.Validate(state) {
		writeError(w, http.StatusBadRequest, "invalid state (possible CSRF)")
		return
	}
	info, err := h.Feishu.Callback(r.Context(), code)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	now := h.t()
	u, err := h.Store.GetUserByProvider("feishu", info.OpenID)
	if errors.Is(err, auth.ErrNotFound) {
		u = &auth.User{
			Username:    "feishu_" + info.OpenID,
			DisplayName: info.Name,
			Email:       info.Email,
			Role:        auth.RoleUser,
			Status:      auth.StatusActive,
			Provider:    "feishu",
			ProviderSub: info.OpenID,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if info.Name != "" {
			u.DisplayName = info.Name
		}
		if err := h.Store.CreateUser(u); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create user: "+err.Error())
			return
		}
		h.audit(0, "user.register", "feishu user "+info.OpenID)
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	} else if u.Status == auth.StatusDisabled {
		writeError(w, http.StatusForbidden, "user disabled")
		return
	}
	token, err := h.Auth.IssueJWT(u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": u})
}

type exchangeTokenReq struct {
	Token string `json:"token"`
}

// exchangeToken 用 PAT 换 JWT。
func (h *Handler) exchangeToken(w http.ResponseWriter, r *http.Request) {
	var req exchangeTokenReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !auth.IsPAT(req.Token) {
		writeError(w, http.StatusBadRequest, "token must be a PAT (pat_ocw_...)")
		return
	}
	pat, err := h.Store.GetPATByHash(auth.HashToken(req.Token))
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "pat lookup failed")
		return
	}
	if pat.Expired(h.t()) {
		writeError(w, http.StatusUnauthorized, "token expired")
		return
	}
	u, err := h.Store.GetUser(pat.UserID)
	if err != nil || u.Status == auth.StatusDisabled {
		writeError(w, http.StatusUnauthorized, "user not available")
		return
	}
	jwtToken, err := h.Auth.IssueJWT(u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": jwtToken, "expiresIn": int64(auth.TokenTTL.Seconds()), "user": u})
}

type createPATReq struct {
	Name          string `json:"name"`
	ExpiresInDays int64  `json:"expiresInDays"`
}

func (h *Handler) createPAT(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	var req createPATReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Name) < 1 || len(req.Name) > 64 {
		writeError(w, http.StatusBadRequest, "name must be 1-64 chars")
		return
	}
	expiresAt := time.Time{}
	if req.ExpiresInDays > 0 {
		expiresAt = h.t().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
	}
	plain, hash, err := auth.GeneratePAT()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	id, err := h.Store.CreatePAT(u.ID, req.Name, hash, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store token")
		return
	}
	h.audit(u.ID, "pat.create", req.Name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "name": req.Name, "token": plain,
		"expiresAt": expiresAt,
		"note":      "token 明文仅此一次返回，请妥善保存",
	})
}

func (h *Handler) listPATs(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	pats, err := h.Store.ListPATs(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}
	writeJSON(w, http.StatusOK, pats)
}

func (h *Handler) deletePAT(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	// 只允许删除自己的 PAT。
	for _, p := range mustList(h, u.ID) {
		if p.ID == id {
			if err := h.Store.DeletePAT(id); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to delete token")
				return
			}
			h.audit(u.ID, "pat.delete", "")
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w, http.StatusForbidden, "not your token")
}

func mustList(h *Handler, userID int64) []*auth.PAT {
	pats, err := h.Store.ListPATs(userID)
	if err != nil {
		return nil
	}
	return pats
}
