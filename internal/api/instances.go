package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
)

// maxConcurrentCreating 同一用户并发 Creating 状态实例上限。
const maxConcurrentCreating = 5

// applyTimeout 单次 OpenTofu Apply/Destroy 的超时。
const applyTimeout = 30 * time.Minute

type createInstanceReq struct {
	Provider     string `json:"provider"`
	Region       string `json:"region"`
	Zone         string `json:"zone"`
	ImageID      string `json:"imageID"`
	InstanceType string `json:"instanceType"`
	DurationSec  int64  `json:"durationSec"`
	Name         string `json:"name"`
}

var validProviders = map[string]bool{"alicloud": true, "volcengine": true}

// durationCap 计算用户单次时长上限：min(用户 MaxDurationSec(>0 时), cfg.MaxDurationSec)；<=0 表示不限制。
func (h *Handler) durationCap(u *auth.User) int64 {
	cap := h.Cfg.MaxDurationSec
	if u.MaxDurationSec > 0 && (cap <= 0 || u.MaxDurationSec < cap) {
		cap = u.MaxDurationSec
	}
	return cap
}

// resolveDuration 归一化申请时长：0 用默认值；必须 >0 且 <= 上限（上限 <=0 表示不限制）。
func (h *Handler) resolveDuration(u *auth.User, sec int64) (int64, error) {
	if sec == 0 {
		sec = h.Cfg.DefaultDurationSec
	}
	if sec <= 0 {
		return 0, fmt.Errorf("durationSec must be positive")
	}
	if cap := h.durationCap(u); cap > 0 && sec > cap {
		return 0, fmt.Errorf("durationSec %d exceeds max %d", sec, cap)
	}
	return sec, nil
}

func (h *Handler) createInstance(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	var req createInstanceReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validProviders[req.Provider] {
		writeError(w, http.StatusBadRequest, "provider must be alicloud or volcengine")
		return
	}
	for field, val := range map[string]string{
		"region": req.Region, "zone": req.Zone, "imageID": req.ImageID, "instanceType": req.InstanceType,
	} {
		if val == "" || len(val) > 128 {
			writeError(w, http.StatusBadRequest, field+" is required (max 128 chars)")
			return
		}
	}
	if req.Name != "" && len(req.Name) > 64 {
		writeError(w, http.StatusBadRequest, "name too long (max 64)")
		return
	}
	duration, err := h.resolveDuration(u, req.DurationSec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 同一用户并发 Creating 上限。
	existing, err := h.Store.ListInstancesByUser(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list instances")
		return
	}
	creating := 0
	for _, inst := range existing {
		if inst.Status == StatusCreating {
			creating++
		}
	}
	if creating >= maxConcurrentCreating {
		writeError(w, http.StatusTooManyRequests, "too many instances being created (max 5 concurrent)")
		return
	}

	name := req.Name
	if name == "" {
		name = fmt.Sprintf("ocw-%s", randHex(4))
	}
	now := h.t()
	inst := &Instance{
		UserID:       u.ID,
		Name:         name,
		Provider:     req.Provider,
		Region:       req.Region,
		Zone:         req.Zone,
		ImageID:      req.ImageID,
		InstanceType: req.InstanceType,
		Status:       StatusCreating,
		DurationSec:  duration,
		TfWorkspace:  "inst-" + randHex(8),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.Store.CreateInstance(inst); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create instance record")
		return
	}
	h.audit(u.ID, "instance.create", fmt.Sprintf("id=%d provider=%s region=%s duration=%ds", inst.ID, inst.Provider, inst.Region, inst.DurationSec))
	go h.applyInstance(inst)
	writeJSON(w, http.StatusAccepted, inst)
}

// applyInstance 异步执行 OpenTofu Apply；成功后写入 IP、置 Running，
// ExpiresAt 从 apply 完成时刻起算；失败置 Failed + ErrorMessage。
func (h *Handler) applyInstance(inst *Instance) {
	ctx, cancel := context.WithTimeout(context.Background(), applyTimeout)
	defer cancel()
	vars := map[string]string{
		"provider":         inst.Provider, // runner 依据它选择模板与云凭证（必需）
		"region":           inst.Region,
		"zone":             inst.Zone,
		"image_id":         inst.ImageID,
		"instance_type":    inst.InstanceType,
		"instance_name":    inst.Name,
		"public_bandwidth": "5",
	}
	done := make(chan error, 1)
	go func() { done <- h.Tofu.Apply(ctx, inst.TfWorkspace, vars) }()
	var applyErr error
	select {
	case applyErr = <-done:
	case <-ctx.Done():
		applyErr = ctx.Err()
	}
	if applyErr != nil {
		h.markFailed(inst.ID, "apply failed: "+applyErr.Error())
		return
	}
	publicIP, privateIP, err := h.Tofu.OutputIP(ctx, inst.TfWorkspace)
	if err != nil {
		h.markFailed(inst.ID, "output ip failed: "+err.Error())
		return
	}
	now := h.t()
	// 重新读取以避免覆盖并发更新。
	fresh, err := h.Store.GetInstance(inst.ID)
	if err != nil {
		return
	}
	if fresh.Status != StatusCreating {
		return
	}
	fresh.Status = StatusRunning
	fresh.PublicIP = publicIP
	fresh.PrivateIP = privateIP
	fresh.ExpiresAt = now.Add(time.Duration(fresh.DurationSec) * time.Second)
	fresh.UpdatedAt = now
	_ = h.Store.UpdateInstance(fresh)
}

func (h *Handler) markFailed(id int64, msg string) {
	if inst, err := h.Store.GetInstance(id); err == nil {
		inst.Status = StatusFailed
		inst.ErrorMessage = msg
		inst.UpdatedAt = h.t()
		_ = h.Store.UpdateInstance(inst)
	}
	h.audit(0, "instance.apply_failed", fmt.Sprintf("id=%d err=%s", id, msg))
}

func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u.Role == auth.RoleAdmin {
		// admin 可看全部：借助 ListUsers 遍历（store 子集无 ListAllInstances）。
		users, err := h.Store.ListUsers()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list instances")
			return
		}
		var all []*Instance
		for _, su := range users {
			insts, err := h.Store.ListInstancesByUser(su.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to list instances")
				return
			}
			all = append(all, insts...)
		}
		writeJSON(w, http.StatusOK, all)
		return
	}
	insts, err := h.Store.ListInstancesByUser(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list instances")
		return
	}
	writeJSON(w, http.StatusOK, insts)
}

// loadOwnedInstance 加载实例并校验所有权（admin 放行）。
func (h *Handler) loadOwnedInstance(w http.ResponseWriter, r *http.Request) (*Instance, bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return nil, false
	}
	inst, err := h.Store.GetInstance(id)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "instance not found")
		return nil, false
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load instance")
		return nil, false
	}
	u := auth.UserFromContext(r.Context())
	if u.Role != auth.RoleAdmin && inst.UserID != u.ID {
		writeError(w, http.StatusForbidden, "not the instance owner")
		return nil, false
	}
	return inst, true
}

func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.loadOwnedInstance(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

type renewInstanceReq struct {
	DurationSec int64 `json:"durationSec"`
}

// renewInstance 续用一次：Running、未续用过、未过期；ExpiresAt=min(now+duration, now+上限)。
func (h *Handler) renewInstance(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.loadOwnedInstance(w, r)
	if !ok {
		return
	}
	u := auth.UserFromContext(r.Context())
	var req renewInstanceReq
	if !decodeJSON(w, r, &req) {
		return
	}
	now := h.t()
	if !inst.CanRenew(now) {
		writeError(w, http.StatusBadRequest, "instance cannot be renewed (must be running, not expired, not already renewed)")
		return
	}
	duration, err := h.resolveDuration(u, req.DurationSec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	newExpire := now.Add(time.Duration(duration) * time.Second)
	if cap := h.durationCap(u); cap > 0 {
		if capped := now.Add(time.Duration(cap) * time.Second); capped.Before(newExpire) {
			newExpire = capped
		}
	}
	inst.RenewedAt = &now
	inst.ExpiresAt = newExpire
	inst.UpdatedAt = now
	if err := h.Store.UpdateInstance(inst); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to renew instance")
		return
	}
	h.audit(u.ID, "instance.renew", fmt.Sprintf("id=%d until=%s", inst.ID, newExpire.Format(time.RFC3339)))
	writeJSON(w, http.StatusOK, inst)
}

// deleteInstance 提前销毁：所有者或 admin；置 Destroying 并异步 destroy。
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.loadOwnedInstance(w, r)
	if !ok {
		return
	}
	if inst.Status != StatusRunning && inst.Status != StatusFailed {
		writeError(w, http.StatusBadRequest, "only running or failed instances can be destroyed")
		return
	}
	u := auth.UserFromContext(r.Context())
	now := h.t()
	inst.Status = StatusDestroying
	inst.UpdatedAt = now
	if err := h.Store.UpdateInstance(inst); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update instance")
		return
	}
	h.audit(u.ID, "instance.destroy", fmt.Sprintf("id=%d", inst.ID))
	go h.destroyInstance(inst.ID, inst.TfWorkspace)
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) destroyInstance(id int64, workspace string) {
	ctx, cancel := context.WithTimeout(context.Background(), applyTimeout)
	defer cancel()
	if err := h.Tofu.Destroy(ctx, workspace); err != nil {
		// 销毁失败保持 Destroying 并记录原因，交给调度器退避重试——
		// 绝不置 Failed：Failed 不会被调度器扫描，云资源将泄漏无人回收。
		if inst, gerr := h.Store.GetInstance(id); gerr == nil {
			inst.Status = StatusDestroying
			inst.ErrorMessage = "destroy failed: " + err.Error()
			inst.UpdatedAt = h.t()
			_ = h.Store.UpdateInstance(inst)
		}
		return
	}
	if inst, err := h.Store.GetInstance(id); err == nil {
		now := h.t()
		inst.Status = StatusDestroyed
		inst.UpdatedAt = now
		_ = h.Store.UpdateInstance(inst)
	}
}

func randHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(buf)
}
