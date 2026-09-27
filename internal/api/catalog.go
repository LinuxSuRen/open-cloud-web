package api

import (
	"net/http"
)

// catalogHandler 提供云厂商目录查询（regions/images/instance-types）。
func (h *Handler) cloudProvider(w http.ResponseWriter, name string) (CloudProvider, bool) {
	if h.Registry == nil {
		writeError(w, http.StatusServiceUnavailable, "cloud registry not configured")
		return nil, false
	}
	p, ok := h.Registry.Provider(name)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown provider: "+name)
		return nil, false
	}
	return p, true
}

func (h *Handler) listRegions(w http.ResponseWriter, r *http.Request) {
	p, ok := h.cloudProvider(w, r.PathValue("p"))
	if !ok {
		return
	}
	regions, err := p.ListRegions(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list regions: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, regions)
}

func (h *Handler) listImages(w http.ResponseWriter, r *http.Request) {
	p, ok := h.cloudProvider(w, r.PathValue("p"))
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

func (h *Handler) listInstanceTypes(w http.ResponseWriter, r *http.Request) {
	p, ok := h.cloudProvider(w, r.PathValue("p"))
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
