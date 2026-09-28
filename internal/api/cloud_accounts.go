// 云账号（用户添加的云提供商认证信息）管理与基于账号的目录查询。
package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"github.com/linuxsuren/open-cloud-web/internal/cloud"
	"github.com/linuxsuren/open-cloud-web/internal/model"
)

const maxKeyName = 128

type createAccountReq struct {
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Region    string `json:"region"`
}

// listAccounts GET /api/v1/cloud-accounts：普通用户看自己的，admin 看全部。
func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	var (
		accounts []*model.CloudAccount
		err      error
	)
	if u.Role == auth.RoleAdmin {
		accounts, err = h.Store.ListCloudAccounts()
	} else {
		accounts, err = h.Store.ListCloudAccountsByUser(u.ID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list cloud accounts")
		return
	}
	if accounts == nil {
		accounts = []*model.CloudAccount{}
	}
	writeJSON(w, http.StatusOK, accounts)
}

// createAccount POST /api/v1/cloud-accounts：secret 以 AES-GCM 加密落库。
func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	var req createAccountReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Name) < 1 || len(req.Name) > 64 {
		writeError(w, http.StatusBadRequest, "name must be 1-64 chars")
		return
	}
	if req.Provider != "alicloud" && req.Provider != "volcengine" {
		writeError(w, http.StatusBadRequest, "provider must be alicloud or volcengine")
		return
	}
	if req.AccessKey == "" || len(req.SecretKey) < 8 || len(req.SecretKey) > maxKeyName {
		writeError(w, http.StatusBadRequest, "accessKey required; secretKey must be 8-128 chars")
		return
	}
	enc, err := encryptSecret(req.SecretKey, h.Cfg.SecretKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encrypt secret")
		return
	}
	now := h.t()
	a := &model.CloudAccount{
		UserID:    u.ID,
		Name:      req.Name,
		Provider:  req.Provider,
		AccessKey: req.AccessKey,
		SecretEnc: enc,
		Region:    req.Region,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.Store.CreateCloudAccount(a); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create cloud account")
		return
	}
	h.audit(u.ID, "cloud_account.create", req.Name+" ("+req.Provider+")")
	writeJSON(w, http.StatusCreated, a)
}

type patchAccountReq struct {
	Name       *string `json:"name"`
	AccessKey  *string `json:"accessKey"`
	SecretKey  *string `json:"secretKey"`
	Region     *string `json:"region"`
}

// patchAccount PATCH /api/v1/cloud-accounts/{id}：修改配置（所有者或 admin）。
// provider 不可改（涉及模板与凭据体系）；secret 重新加密落库。
func (h *Handler) patchAccount(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	a, err := h.Store.GetCloudAccount(id)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "cloud account not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cloud account")
		return
	}
	if a.UserID != u.ID && u.Role != auth.RoleAdmin {
		writeError(w, http.StatusForbidden, "not your cloud account")
		return
	}
	var req patchAccountReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		if *req.Name == "" || len(*req.Name) > 64 {
			writeError(w, http.StatusBadRequest, "name must be 1-64 chars")
			return
		}
		a.Name = *req.Name
	}
	if req.AccessKey != nil {
		if *req.AccessKey == "" || len(*req.AccessKey) > maxKeyName {
			writeError(w, http.StatusBadRequest, "accessKey must be 1-128 chars")
			return
		}
		a.AccessKey = *req.AccessKey
	}
	if req.SecretKey != nil {
		if len(*req.SecretKey) < 8 || len(*req.SecretKey) > maxKeyName {
			writeError(w, http.StatusBadRequest, "secretKey must be 8-128 chars")
			return
		}
		enc, err := encryptSecret(*req.SecretKey, h.Cfg.SecretKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encrypt secret")
			return
		}
		a.SecretEnc = enc
	}
	if req.Region != nil {
		a.Region = *req.Region
	}
	if err := h.Store.UpdateCloudAccount(a); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update cloud account")
		return
	}
	h.audit(u.ID, "cloud_account.update", a.Name)
	writeJSON(w, http.StatusOK, a)
}

// deleteAccount DELETE /api/v1/cloud-accounts/{id}：所有者或 admin。
func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	a, err := h.Store.GetCloudAccount(id)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "cloud account not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cloud account")
		return
	}
	if a.UserID != u.ID && u.Role != auth.RoleAdmin {
		writeError(w, http.StatusForbidden, "not your cloud account")
		return
	}
	if err := h.Store.DeleteCloudAccount(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete cloud account")
		return
	}
	h.audit(u.ID, "cloud_account.delete", a.Name)
	w.WriteHeader(http.StatusNoContent)
}

// accountProvider 解析账号归属并构造对应云 Provider（用账号自己的凭据）。
func (h *Handler) accountProvider(w http.ResponseWriter, r *http.Request) (cloud.Provider, bool) {
	u := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return nil, false
	}
	a, err := h.Store.GetCloudAccount(id)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "cloud account not found")
		return nil, false
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cloud account")
		return nil, false
	}
	if a.UserID != u.ID && u.Role != auth.RoleAdmin {
		writeError(w, http.StatusForbidden, "not your cloud account")
		return nil, false
	}
	secret, err := decryptSecret(a.SecretEnc, h.Cfg.SecretKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decrypt secret")
		return nil, false
	}
	switch a.Provider {
	case "alicloud":
		return cloud.NewAlicloudProvider(a.AccessKey, secret), true
	case "volcengine":
		return cloud.NewVolcengineProvider(a.AccessKey, secret), true
	}
	writeError(w, http.StatusBadRequest, "unknown provider: "+a.Provider)
	return nil, false
}

// runnerForAccount 按账号 ID 解密凭据并构造 tofu Runner（无归属校验，
// 供销毁等系统侧路径使用；用户侧入口用 accountRunner）。
func (h *Handler) runnerForAccount(accountID int64) (Runner, *model.CloudAccount, error) {
	a, err := h.Store.GetCloudAccount(accountID)
	if err != nil {
		return nil, nil, err
	}
	secret, err := decryptSecret(a.SecretEnc, h.Cfg.SecretKey)
	if err != nil {
		return nil, nil, err
	}
	return h.NewRunner(a.Provider, a.AccessKey, secret), a, nil
}

// loadAccountRunner 为实例创建构造 Runner（含归属校验）。
func (h *Handler) accountRunner(u *auth.User, accountID int64) (Runner, *model.CloudAccount, error) {
	runner, a, err := h.runnerForAccount(accountID)
	if err != nil {
		return nil, nil, err
	}
	if a.UserID != u.ID && u.Role != auth.RoleAdmin {
		return nil, nil, errForbiddenAccount
	}
	return runner, a, nil
}

var errForbiddenAccount = errors.New("not your cloud account")

// POST /api/v1/cloud-accounts/{id}/test：用账号凭据实际调用一次云 API
// （列可用区，读操作、无副作用），返回可用性结论与具体错误信息。
func (h *Handler) testAccount(w http.ResponseWriter, r *http.Request) {
	p, ok := h.accountProvider(w, r)
	if !ok {
		return
	}
	regions, err := p.ListRegions(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"message": "账号不可用：" + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"message": fmt.Sprintf("账号可用，凭据有效（成功获取 %d 个可用区/地域）", len(regions)),
	})
}

// GET /api/v1/cloud-accounts/{id}/regions
func (h *Handler) listAccountRegions(w http.ResponseWriter, r *http.Request) {
	p, ok := h.accountProvider(w, r)
	if !ok {
		return
	}
	regions, err := p.ListRegions(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list regions: "+err.Error())
		return
	}
	if regions == nil {
		regions = []string{}
	}
	writeJSON(w, http.StatusOK, regions)
}

// GET /api/v1/cloud-accounts/{id}/zones?region=
func (h *Handler) listAccountZones(w http.ResponseWriter, r *http.Request) {
	p, ok := h.accountProvider(w, r)
	if !ok {
		return
	}
	region := r.URL.Query().Get("region")
	if region == "" {
		writeError(w, http.StatusBadRequest, "region query parameter is required")
		return
	}
	zones, err := p.ListZones(r.Context(), region)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list zones: "+err.Error())
		return
	}
	if zones == nil {
		zones = []string{}
	}
	writeJSON(w, http.StatusOK, zones)
}

// GET /api/v1/cloud-accounts/{id}/images?region=
func (h *Handler) listAccountImages(w http.ResponseWriter, r *http.Request) {
	p, ok := h.accountProvider(w, r)
	if !ok {
		return
	}
	region := r.URL.Query().Get("region")
	if region == "" {
		writeError(w, http.StatusBadRequest, "region query parameter is required")
		return
	}
	images, err := p.ListImages(r.Context(), region)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list images: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, images)
}

// GET /api/v1/cloud-accounts/{id}/instance-types?region=
func (h *Handler) listAccountInstanceTypes(w http.ResponseWriter, r *http.Request) {
	p, ok := h.accountProvider(w, r)
	if !ok {
		return
	}
	region := r.URL.Query().Get("region")
	if region == "" {
		writeError(w, http.StatusBadRequest, "region query parameter is required")
		return
	}
	specs, err := p.ListInstanceTypes(r.Context(), region)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list instance types: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, specs)
}
