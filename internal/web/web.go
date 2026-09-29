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

// Handler 返回根路径处理器：静态资源与 SPA 路由。
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			_, statErr := fs.Stat(sub, p)
			if statErr != nil {
				// 带哈希指纹的构建产物不存在时必须 404：
				// 若回落 index.html，浏览器会把 HTML 当 JS 加载导致整页空白
				// （典型于浏览器缓存了旧 index.html 引用旧哈希资源）。
				if strings.HasPrefix(p, "assets/") {
					http.NotFound(w, r)
					return
				}
				// 其余未知路径回落 SPA 入口（前端路由）。
				r.URL.Path = "/"
			}
		}
		// index.html 不缓存，保证发版后立即拿到新资源引用。
		if r.URL.Path == "/" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}
