package api

import (
	"strconv"
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
	CloudAccountID int64  `json:"cloudAccountID"` // 必填：用户添加的云账号
	Region         string `json:"region"`
	Zone           string `json:"zone"`
	ImageID        string `json:"imageID"`
	InstanceType   string `json:"instanceType"`
	DurationSec    int64  `json:"durationSec"`
	Name           string `json:"name"`
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
	if req.CloudAccountID <= 0 {
		writeError(w, http.StatusBadRequest, "cloudAccountID is required (add a cloud account first)")
		return
	}
	runner, account, err := h.accountRunner(u, req.CloudAccountID)
	if err != nil {
		if errors.Is(err, errForbiddenAccount) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "cloud account not found")
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
		UserID:         u.ID,
		CloudAccountID: account.ID, // 销毁/调度重试时凭此恢复账号凭据
		Name:           name,
		Provider:       account.Provider, // 取自云账号，客户端不可伪造
		Region:         req.Region,
		Zone:           req.Zone,
		ImageID:        req.ImageID,
		InstanceType:   req.InstanceType,
		Status:         StatusCreating,
		DurationSec:    duration,
		TfWorkspace:    "inst-" + randHex(8),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := h.Store.CreateInstance(inst); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create instance record")
		return
	}
	h.audit(u.ID, "instance.create", fmt.Sprintf("id=%d account=%d(%s) provider=%s region=%s duration=%ds", inst.ID, account.ID, account.Name, inst.Provider, inst.Region, inst.DurationSec))
	go h.applyInstance(runner, inst)
	h.Hub.Broadcast(refreshEvent())
	writeJSON(w, http.StatusAccepted, inst)
}

// applyInstance 异步执行 OpenTofu Apply；成功后写入 IP、置 Running，
// ExpiresAt 从 apply 完成时刻起算；失败置 Failed + ErrorMessage。
func (h *Handler) applyInstance(runner Runner, inst *Instance) {
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
	go func() { done <- runner.Apply(ctx, inst.TfWorkspace, vars) }()
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
	publicIP, privateIP, err := runner.OutputIP(ctx, inst.TfWorkspace)
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
	h.Hub.Broadcast(refreshEvent())
}

func (h *Handler) markFailed(id int64, msg string) {
	if inst, err := h.Store.GetInstance(id); err == nil {
		inst.Status = StatusFailed
		inst.ErrorMessage = msg
		inst.UpdatedAt = h.t()
		_ = h.Store.UpdateInstance(inst)
	}
	h.audit(0, "instance.apply_failed", fmt.Sprintf("id=%d err=%s", id, msg))
	h.Hub.Broadcast(refreshEvent())
}

func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	var insts []*Instance
	if u.Role == auth.RoleAdmin {
		// admin 可看全部：借助 ListUsers 遍历（store 子集无 ListAllInstances）。
		users, err := h.Store.ListUsers()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list instances")
			return
		}
		for _, su := range users {
			mine, err := h.Store.ListInstancesByUser(su.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to list instances")
				return
			}
			insts = append(insts, mine...)
		}
	} else {
		var err error
		if insts, err = h.Store.ListInstancesByUser(u.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list instances")
			return
		}
	}
	filtered := filterInstances(insts, r.URL.Query().Get("status"))
	writeJSON(w, http.StatusOK, paginateInstances(filtered, r))
}

// paginateInstances 内存分页（单用户实例量小；admin 聚合视图同路径复用）。
// 返回 {instances,total,page,pageSize,pages}，默认每页 20、上限 100。
func paginateInstances(in []*Instance, r *http.Request) map[string]any {
	q := r.URL.Query()
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	page, err := strconv.Atoi(q.Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	total := len(in)
	pages := (total + limit - 1) / limit
	if page > pages && pages > 0 {
		page = pages
	}
	start := (page - 1) * limit
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	if in == nil {
		in = []*Instance{}
	}
	return map[string]any{
		"instances": in[start:end], "total": total,
		"page": page, "pageSize": limit, "pages": pages,
	}
}

// filterInstances 按状态过滤：""或"active"只留非终态（创建中/运行中/销毁中），
// "all" 全量，其余值按精确状态匹配（creating/running/destroying/destroyed/failed）。
func filterInstances(in []*Instance, status string) []*Instance {
	switch status {
	case "", "active":
		// “有效” = 创建中 + 运行中（销毁中不算）。
		out := make([]*Instance, 0, len(in))
		for _, i := range in {
			if i.Status == StatusCreating || i.Status == StatusRunning {
				out = append(out, i)
			}
		}
		return out
	case "all":
		if in == nil {
			return []*Instance{}
		}
		return in
	default:
		out := make([]*Instance, 0, len(in))
		for _, i := range in {
			if string(i.Status) == status {
				out = append(out, i)
			}
		}
		return out
	}
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
	h.Hub.Broadcast(refreshEvent())
	writeJSON(w, http.StatusOK, inst)
}

// deleteInstance 提前销毁：所有者或 admin；置 Destroying 并异步 destroy。
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.loadOwnedInstance(w, r)
	if !ok {
		return
	}
	u := auth.UserFromContext(r.Context())
	// 已销毁的终态记录：直接删除记录（云资源已回收，无需再调 tofu）。
	if inst.Status == StatusDestroyed {
		if err := h.Store.DeleteInstance(inst.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete record")
			return
		}
		h.audit(u.ID, "instance.record.delete", fmt.Sprintf("id=%d", inst.ID))
		h.Hub.Broadcast(refreshEvent())
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if inst.Status != StatusRunning && inst.Status != StatusFailed {
		writeError(w, http.StatusBadRequest, "only running or failed instances can be destroyed")
		return
	}
	now := h.t()
	inst.Status = StatusDestroying
	inst.UpdatedAt = now
	if err := h.Store.UpdateInstance(inst); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update instance")
		return
	}
	h.audit(u.ID, "instance.destroy", fmt.Sprintf("id=%d", inst.ID))
	go h.destroyInstance(inst.ID, inst.TfWorkspace)
	h.Hub.Broadcast(refreshEvent())
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) destroyInstance(id int64, workspace string) {
	// 凭实例记录里的云账号恢复凭据（账号被删除时记录明确原因，保持 Destroying）。
	var runner Runner
	if inst, err := h.Store.GetInstance(id); err == nil && inst.CloudAccountID > 0 {
		if r, _, rerr := h.runnerForAccount(inst.CloudAccountID); rerr == nil {
			runner = r
		}
	}
	if runner == nil {
		if inst, err := h.Store.GetInstance(id); err == nil {
			inst.Status = StatusDestroying
			inst.ErrorMessage = "destroy failed: cloud account unavailable (deleted?)"
			inst.UpdatedAt = h.t()
			_ = h.Store.UpdateInstance(inst)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), applyTimeout)
	defer cancel()
	if err := runner.Destroy(ctx, workspace); err != nil {
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
	h.Hub.Broadcast(refreshEvent())
}

func randHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(buf)
}
