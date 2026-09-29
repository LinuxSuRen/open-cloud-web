// 系统设置（admin）：provider 下载代理与云 API 代理两条独立链路。
package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
)

// 代理设置分两条独立链路，用户可分别配置（空 = 直连）：
//   - tofu 下载：provider 二进制从 GitHub Releases 下载（tofu 子进程）
//   - 云 API：阿里云/火山引擎 OpenAPI 查询与实例创建
//
// 旧键 http_proxy / tofu_proxy 作为取值回退（升级兼容）。
const (
	SettingTofuProxyKey  = "http_proxy_tofu"
	SettingCloudProxyKey = "http_proxy_cloud"
	settingProxyKeyOld   = "http_proxy"
	settingProxyKeyOld2  = "tofu_proxy"
)

// tofuProxySetting 返回 provider 下载代理。
func (h *Handler) tofuProxySetting() string {
	for _, k := range []string{SettingTofuProxyKey, settingProxyKeyOld, settingProxyKeyOld2} {
		if v, _ := h.Store.GetSetting(k); v != "" {
			return v
		}
	}
	return ""
}

// cloudProxySetting 返回云 API 代理。
func (h *Handler) cloudProxySetting() string {
	for _, k := range []string{SettingCloudProxyKey, settingProxyKeyOld} {
		if v, _ := h.Store.GetSetting(k); v != "" {
			return v
		}
	}
	return ""
}

type settingsResp struct {
	TofuProxyURL  string `json:"tofuProxyURL"`
	CloudProxyURL string `json:"cloudProxyURL"`
}

// GET /api/v1/admin/settings
func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, settingsResp{
		TofuProxyURL:  h.tofuProxySetting(),
		CloudProxyURL: h.cloudProxySetting(),
	})
}

type settingsReq struct {
	TofuProxyURL  *string `json:"tofuProxyURL"`
	CloudProxyURL *string `json:"cloudProxyURL"`
}

// PUT /api/v1/admin/settings：proxyURL 为空表示不使用代理。
// 代理仅作用于 tofu 子进程（provider 下载），并固定对
// registry.opentofu.org 直连（registry 通常无需代理且经代理常更慢）。
func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsReq
	if !decodeJSON(w, r, &req) {
		return
	}
	setOne := func(field, key string, v *string) (string, bool) {
		if v == nil {
			return "", true
		}
		val := strings.TrimSpace(*v)
		if val != "" {
			u, err := url.Parse(val)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				writeError(w, http.StatusBadRequest, field+" must be like http://host:port")
				return "", false
			}
		}
		if err := h.Store.SetSetting(key, val); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save setting")
			return "", false
		}
		h.audit(auth.UserFromContext(r.Context()).ID, "settings.update", key+"="+val)
		return val, true
	}
	tofuV, ok := setOne("tofuProxyURL", SettingTofuProxyKey, req.TofuProxyURL)
	if !ok {
		return
	}
	cloudV, ok := setOne("cloudProxyURL", SettingCloudProxyKey, req.CloudProxyURL)
	if !ok {
		return
	}
	_ = tofuV
	_ = cloudV
	writeJSON(w, http.StatusOK, settingsResp{
		TofuProxyURL:  h.tofuProxySetting(),
		CloudProxyURL: h.cloudProxySetting(),
	})
}
