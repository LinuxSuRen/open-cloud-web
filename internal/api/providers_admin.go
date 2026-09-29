// provider 插件缓存管理（admin）：查看下载状态、异步预下载（带日志/进度）。
package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"github.com/linuxsuren/open-cloud-web/internal/tofu"
)

// preloadJob 一次（进行中或已结束的）provider 下载任务。
type preloadJob struct {
	Status    string     `json:"status"` // running | success | failed
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	Error     string     `json:"error,omitempty"`

	mu  sync.Mutex
	log ringLog
}

// ringLog 只保留最近 8KB 输出（tofu init 的进度流）。
type ringLog struct{ buf []byte }

func (r *ringLog) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	if len(r.buf) > 8192 {
		r.buf = r.buf[len(r.buf)-8192:]
	}
	return len(p), nil
}

// Write 把 tofu init 输出写入任务日志（并发安全）。
func (j *preloadJob) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.log.Write(p)
}

func (j *preloadJob) Log() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return string(j.log.buf)
}

// PreloadMonitor 记录各 provider 的下载任务状态（内存态，重启即清）。
type PreloadMonitor struct {
	mu   sync.Mutex
	jobs map[string]*preloadJob
}

func NewPreloadMonitor() *PreloadMonitor {
	return &PreloadMonitor{jobs: map[string]*preloadJob{}}
}

func (m *PreloadMonitor) snapshot() map[string]map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]map[string]any{}
	for p, j := range m.jobs {
		out[p] = map[string]any{
			"status":    j.Status,
			"startedAt": j.StartedAt,
			"endedAt":   j.EndedAt,
			"error":     j.Error,
			"log":       j.Log(),
		}
	}
	return out
}

// GET /api/v1/admin/providers：已下载的 provider 插件 + 各下载任务状态。
func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": tofu.ListCachedProviders(h.Cfg.DataDir),
		"jobs":      h.Preloads.snapshot(),
	})
}

type preloadReq struct {
	Provider string `json:"provider"`
}

// POST /api/v1/admin/providers/preload：异步启动下载，立即返回；
// 前端轮询 GET /admin/providers 获取日志与进度。重复点击幂等（复用在跑任务）。
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

	h.Preloads.mu.Lock()
	job := h.Preloads.jobs[req.Provider]
	if job != nil && job.Status == "running" {
		h.Preloads.mu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]any{"provider": req.Provider, "status": "running", "note": "already running"})
		return
	}
	job = &preloadJob{Status: "running", StartedAt: h.t()}
	h.Preloads.jobs[req.Provider] = job
	h.Preloads.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		err := h.PreloadProvider(ctx, req.Provider, job) // job 实现 io.Writer
		end := time.Now()
		job.mu.Lock()
		job.EndedAt = &end
		if err != nil {
			job.Status = "failed"
			job.Error = err.Error()
		} else {
			job.Status = "success"
		}
		job.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"provider": req.Provider, "status": "running"})
}
