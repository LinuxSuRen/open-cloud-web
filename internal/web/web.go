// Package web 提供内嵌的 Web 控制台（Vue 3 + Vite 构建，go:embed 打进二进制）。
// 构建方式：cd webapp && npm run build（产物输出到本包 dist/ 目录）。
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler 返回根路径处理器：非 /api、/healthz 的路径回落到 SPA。
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p != "/" {
			// 资源不存在时回落 index.html（SPA 路由）。
			if _, err := fs.Stat(sub, strings.TrimPrefix(p, "/")); err != nil {
				r.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}
