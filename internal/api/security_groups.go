// 命名安全组（端口集合）管理。
package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"github.com/linuxsuren/open-cloud-web/internal/model"
)

// CommonPorts 常见服务端口目录（前端勾选用）。
var CommonPorts = []struct {
	Port int    `json:"port"`
	Name string `json:"name"`
}{
	{22, "SSH"}, {80, "HTTP"}, {443, "HTTPS"}, {1883, "MQTT"}, {8883, "MQTTS"},
	{3306, "MySQL"}, {5432, "PostgreSQL"}, {6379, "Redis"},
	{8080, "HTTP 备用"}, {27017, "MongoDB"}, {9092, "Kafka"}, {15672, "RabbitMQ 管控台"},
	{1935, "RTMP"}, {8554, "RTSP"}, {8888, "HLS"}, {8889, "WebRTC(HTTP)"},
}

// CommonUDPPorts 常见 UDP 端口目录。
var CommonUDPPorts = []struct {
	Port int    `json:"port"`
	Name string `json:"name"`
}{
	{8189, "WebRTC ICE"}, {8890, "SRT"}, {8000, "RTP"}, {8001, "RTCP"}, {161, "SNMP"},
}

// GET /api/v1/security-groups：全局预置 + 自己创建的；附常见端口目录。
func (h *Handler) listSecurityGroups(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	groups, err := h.Store.ListSecurityGroups(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list security groups")
		return
	}
	if groups == nil {
		groups = []*model.SecurityGroup{}
	}
	for _, g := range groups {
		if g.UDPPorts == nil {
			g.UDPPorts = []int{}
		}
		if g.Ports == nil {
			g.Ports = []int{}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"groups": groups, "commonPorts": CommonPorts, "commonUdpPorts": CommonUDPPorts,
	})
}

type createSGReq struct {
	Name     string `json:"name"`
	Ports    []int  `json:"ports"`
	UDPPorts []int  `json:"udpPorts"`
	Remark   string `json:"remark"`
}

// POST /api/v1/security-groups：创建自己的端口集合。
func (h *Handler) createSecurityGroup(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	var req createSGReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Name) < 1 || len(req.Name) > 64 {
		writeError(w, http.StatusBadRequest, "name must be 1-64 chars")
		return
	}
	if (len(req.Ports) == 0 && len(req.UDPPorts) == 0) ||
		len(req.Ports) > 32 || len(req.UDPPorts) > 32 {
		writeError(w, http.StatusBadRequest, "ports/udpPorts must have 1-32 entries (at least one)")
		return
	}
	norm := func(in []int) []int {
		seen := map[int]bool{}
		out := make([]int, 0, len(in))
		for _, p := range in {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
		sort.Ints(out)
		return out
	}
	for _, p := range append(append([]int(nil), req.Ports...), req.UDPPorts...) {
		if p < 1 || p > 65535 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid port %d", p))
			return
		}
	}
	g := &model.SecurityGroup{
		UserID: u.ID, Name: req.Name,
		Ports: norm(req.Ports), UDPPorts: norm(req.UDPPorts), Remark: req.Remark,
	}
	if err := h.Store.CreateSecurityGroup(g); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create security group")
		return
	}
	h.audit(u.ID, "security_group.create", req.Name)
	writeJSON(w, http.StatusCreated, g)
}

// DELETE /api/v1/security-groups/{id}：所有者或 admin；全局预置仅 admin 可删。
func (h *Handler) deleteSecurityGroup(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	g, err := h.Store.GetSecurityGroup(id)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "security group not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load security group")
		return
	}
	if g.UserID != u.ID && u.Role != auth.RoleAdmin {
		writeError(w, http.StatusForbidden, "not your security group")
		return
	}
	if err := h.Store.DeleteSecurityGroup(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete security group")
		return
	}
	h.audit(u.ID, "security_group.delete", g.Name)
	w.WriteHeader(http.StatusNoContent)
}
