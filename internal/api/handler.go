// Package api 实现 OpenCloudLab 的 REST API（/api/v1）与中间件。
package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"github.com/linuxsuren/open-cloud-web/internal/web"
)

// Config 是装配层注入的 API 配置。
type Config struct {
	DefaultDurationSec int64  // 默认申请时长（秒）
	MaxDurationSec     int64  // 全局单次时长上限（<=0 表示不限制）
	CORSAllowedOrigin  string // 允许的 CORS origin，"*" 或具体 origin；空则不发 CORS 头
	SecretKey          string // 云账号 Secret 的 AES 加密密钥（空则回落 JWTSecret）
	DataDir            string // 数据目录（扫描 provider 插件缓存用）
}

// Handler 是 REST API 的根处理器。
type Handler struct {
	Store           Store
	Auth            auth.Manager
	Feishu          *auth.FeishuOAuth
	States          *auth.StateManager
	NewRunner       RunnerFactory                                                 // 按账号凭据构造 tofu Runner
	PreloadProvider func(ctx context.Context, provider string, w io.Writer) error // 预下载 provider 插件
	Preloads        *PreloadMonitor                                               // 下载任务状态/日志
	ILogs           *instanceLogHub                                               // 实例创建过程日志
	Hub             *Hub                                                          // WebSocket 推送
	Cfg             Config
	Log             *log.Logger

	now func() time.Time
}

// NewHandler 构造 Handler；now 仅测试注入用，生产留空即 time.Now。
// cfg.SecretKey 用于云账号 Secret 的 AES 加密；为空时应由装配层传 JWTSecret。
func NewHandler(store Store, mgr auth.Manager, feishu *auth.FeishuOAuth, states *auth.StateManager, newRunner RunnerFactory, cfg Config) *Handler {
	h := &Handler{
		Store:     store,
		Auth:      mgr,
		Feishu:    feishu,
		States:    states,
		NewRunner: newRunner,
		Hub:       NewHub(15 * time.Second), // 周期兜底推送（覆盖调度器侧变更）
		Preloads:  NewPreloadMonitor(),
		ILogs:     newInstanceLogHub(),
		Cfg:       cfg,
		Log:       log.Default(),
	}
	go h.Hub.Run(make(chan struct{}))
	return h
}

func (h *Handler) t() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// Routes 返回完整的 http.Handler（Go 1.22 方法+路径模式路由）。
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// v1 公开端点（无需认证）。
	public := http.NewServeMux()
	public.HandleFunc("POST /api/v1/auth/feishu/login", h.feishuLogin)
	public.HandleFunc("GET /api/v1/auth/feishu/callback", h.feishuCallback)
	public.HandleFunc("POST /api/v1/auth/token", h.exchangeToken)
	public.HandleFunc("POST /api/v1/auth/login", h.login)
	public.HandleFunc("GET /api/v1/ws", h.handleWS)

	// v1 认证端点。
	authed := http.NewServeMux()
	authed.HandleFunc("GET /api/v1/me", h.me)
	authed.HandleFunc("POST /api/v1/auth/pats", h.createPAT)
	authed.HandleFunc("GET /api/v1/auth/pats", h.listPATs)
	authed.HandleFunc("DELETE /api/v1/auth/pats/{id}", h.deletePAT)
	authed.HandleFunc("POST /api/v1/instances", h.createInstance)
	authed.HandleFunc("GET /api/v1/instances", h.listInstances)
	authed.HandleFunc("GET /api/v1/instances/{id}", h.getInstance)
	authed.HandleFunc("POST /api/v1/instances/{id}/renew", h.renewInstance)
	authed.HandleFunc("DELETE /api/v1/instances/{id}", h.deleteInstance)
	authed.HandleFunc("GET /api/v1/instances/{id}/logs", h.instanceLogs)

	// 云账号（用户添加的云提供商认证信息）及其目录查询。
	authed.HandleFunc("GET /api/v1/cloud-accounts", h.listAccounts)
	authed.HandleFunc("POST /api/v1/cloud-accounts", h.createAccount)
	authed.HandleFunc("PATCH /api/v1/cloud-accounts/{id}", h.patchAccount)
	authed.HandleFunc("DELETE /api/v1/cloud-accounts/{id}", h.deleteAccount)
	authed.HandleFunc("POST /api/v1/cloud-accounts/{id}/test", h.testAccount)
	authed.HandleFunc("GET /api/v1/cloud-accounts/{id}/regions", h.listAccountRegions)
	authed.HandleFunc("GET /api/v1/cloud-accounts/{id}/zones", h.listAccountZones)
	authed.HandleFunc("GET /api/v1/cloud-accounts/{id}/images", h.listAccountImages)
	authed.HandleFunc("GET /api/v1/cloud-accounts/{id}/instance-types", h.listAccountInstanceTypes)
	public.Handle("/api/v1/", h.requireAuthWrapper()(authed))

	// v1 管理员端点（认证 + admin 角色）。
	admin := http.NewServeMux()
	admin.HandleFunc("GET /api/v1/users", h.listUsers)
	admin.HandleFunc("POST /api/v1/users", h.createUser)
	admin.HandleFunc("PATCH /api/v1/users/{id}", h.patchUser)
	admin.HandleFunc("DELETE /api/v1/users/{id}", h.deleteUser)
	admin.HandleFunc("GET /api/v1/admin/audit-logs", h.listAuditLogs)
	admin.HandleFunc("GET /api/v1/admin/settings", h.getSettings)
	admin.HandleFunc("PUT /api/v1/admin/settings", h.putSettings)
	admin.HandleFunc("GET /api/v1/admin/providers", h.listProviders)
	admin.HandleFunc("POST /api/v1/admin/providers/preload", h.preloadProvider)
	public.Handle("/api/v1/users", h.requireAuthWrapper()(requireAdmin(admin)))
	public.Handle("/api/v1/users/", h.requireAuthWrapper()(requireAdmin(admin)))
	public.Handle("/api/v1/admin/", h.requireAuthWrapper()(requireAdmin(admin)))

	// 非 API 路径（"/" 与静态资源）回落到内嵌 Web 控制台；
	// /api/、/healthz 仍交给 public mux。
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			public.ServeHTTP(w, r)
			return
		}
		web.Handler().ServeHTTP(w, r)
	}))
	return requestIDLog(h.Log)(corsMiddleware(h.Cfg.CORSAllowedOrigin)(mux))
}

func (h *Handler) requireAuthWrapper() func(http.Handler) http.Handler {
	return auth.Authenticate(h.Store, h.Auth)
}

// --- 通用中间件 ---

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Hijack 透传给底层 ResponseWriter：WebSocket 升级（/api/v1/ws）依赖它。
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijack")
	}
	return hj.Hijack()
}

// requestIDLog 为每个请求生成 8 字节随机 hex requestID 并记录访问日志。
func requestIDLog(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			buf := make([]byte, 8)
			if _, err := rand.Read(buf); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			requestID := hex.EncodeToString(buf)
			w.Header().Set("X-Request-ID", requestID)
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r)
			if logger != nil {
				logger.Printf("request_id=%s method=%s path=%s status=%d duration=%s",
					requestID, r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
			}
		})
	}
}

// corsMiddleware 处理可配置 origin 的 CORS（含 OPTIONS 预检直接放行）。
func corsMiddleware(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireAdmin 校验已认证用户角色为 admin，否则 403。
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil || u.Role != auth.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON 严格解码（禁止未知字段与尾随内容），限制请求体 1MB。
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body: "+err.Error())
		return false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "unexpected trailing json content")
		return false
	}
	return true
}

// validUsername 校验用户名：^[a-zA-Z0-9_-]{2,32}$。
func validUsername(s string) bool {
	if len(s) < 2 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	idStr := r.PathValue(name)
	if idStr == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return 0, false
	}
	n, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || n <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return n, true
}
