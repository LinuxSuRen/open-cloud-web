// 实例创建/销毁过程日志（内存 ring buffer，每实例最近 16KB）。
package api

import (
	"net/http"
	"sync"
)

// instanceLogHub 进程内实例日志集合；重启即清（历史过程不再需要）。
type instanceLogHub struct {
	mu   sync.Mutex
	logs map[int64]*ringLog16k
}

func newInstanceLogHub() *instanceLogHub {
	return &instanceLogHub{logs: map[int64]*ringLog16k{}}
}

// Append 追加一行（自动补换行）。
func (h *instanceLogHub) Append(id int64, line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.logs[id]
	if !ok {
		l = &ringLog16k{}
		h.logs[id] = l
	}
	l.buf = append(l.buf, line...)
	if len(l.buf) == 0 || l.buf[len(l.buf)-1] != '\n' {
		l.buf = append(l.buf, '\n')
	}
	if len(l.buf) > 16<<10 {
		l.buf = l.buf[len(l.buf)-16<<10:]
	}
}

// Get 返回该实例的累计日志（可能为空）。
func (h *instanceLogHub) Get(id int64) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return string(h.logs[id].buf)
}

// Forget 删除记录时清理日志。
func (h *instanceLogHub) Forget(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.logs, id)
}

type ringLog16k struct{ buf []byte }

// GET /api/v1/instances/{id}/logs：创建/销毁过程日志（所有者或 admin）。
func (h *Handler) instanceLogs(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.loadOwnedInstance(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": inst.ID, "status": inst.Status, "log": h.ILogs.Get(inst.ID),
	})
}
