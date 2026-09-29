// 系统设置（admin）：目前支持 tofu provider 下载代理。
package api

import (
	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"net/http"
	"net/url"
	"strings"
)

// SettingProxyKey 是系统 HTTP 代理设置的存储键；作用于云 API 调用
// 与 tofu 子进程出网。旧键 tofu_proxy 作为取值回退（升级兼容）。
const (
	SettingProxyKey    = "http_proxy"
	settingProxyKeyOld = "tofu_proxy"
)

func (h *Handler) proxySetting() string {
	v, _ := h.Store.GetSetting(SettingProxyKey)
	if v == "" {
		v, _ = h.Store.GetSetting(settingProxyKeyOld)
	}
	return v
}

type settingsResp struct {
	ProxyURL string `json:"proxyURL"`
}

// GET /api/v1/admin/settings
func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, settingsResp{ProxyURL: h.proxySetting()})
}

type settingsReq struct {
	ProxyURL *string `json:"proxyURL"`
}

// PUT /api/v1/admin/settings：proxyURL 为空表示不使用代理。
// 代理仅作用于 tofu 子进程（provider 下载），并固定对
// registry.opentofu.org 直连（registry 通常无需代理且经代理常更慢）。
func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ProxyURL != nil {
		v := strings.TrimSpace(*req.ProxyURL)
		if v != "" {
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				writeError(w, http.StatusBadRequest, "proxyURL must be like http://host:port")
				return
			}
		}
		if err := h.Store.SetSetting(SettingProxyKey, v); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save setting")
			return
		}
		h.audit(auth.UserFromContext(r.Context()).ID, "settings.update", "http_proxy="+v)
	}
	writeJSON(w, http.StatusOK, settingsResp{ProxyURL: h.proxySetting()})
}
