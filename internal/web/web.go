// Package web 提供内嵌的 Web 控制台（单页应用，go:embed 打进二进制）。
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static
var staticFS embed.FS

// Handler 返回根路径处理器：非 /api、/healthz 的路径回落到 SPA，
// 支持后续扩展多个静态文件。
func Handler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
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
