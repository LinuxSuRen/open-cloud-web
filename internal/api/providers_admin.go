// provider 插件缓存管理（admin）：查看下载状态、手动预下载。
package api

import (
	"context"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"net/http"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/tofu"
)

// GET /api/v1/admin/providers：已下载的 provider 插件（版本/平台/大小/时间）。
func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": tofu.ListCachedProviders(h.Cfg.DataDir),
	})
}

type preloadReq struct {
	Provider string `json:"provider"`
}

// POST /api/v1/admin/providers/preload：立即下载指定 provider 到共享缓存。
// init 不调用云 API，无需真实凭据；下载走配置的 tofu 下载代理。
func (h *Handler) preloadProvider(w http.ResponseWriter, r *http.Request) {
	if h.PreloadProvider == nil {
		writeError(w, http.StatusServiceUnavailable, "preload not configured")
		return
	}
	var req preloadReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Provider != "alicloud" && req.Provider != "volcengine" {
		writeError(w, http.StatusBadRequest, "provider must be alicloud or volcengine")
		return
	}
	h.audit(auth.UserFromContext(r.Context()).ID, "provider.preload", req.Provider)
	// 下载可能耗时（provider 二进制大、走代理），给足超时。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := h.PreloadProvider(ctx, req.Provider); err != nil {
		writeError(w, http.StatusBadGateway, "preload failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": tofu.ListCachedProviders(h.Cfg.DataDir),
	})
}
