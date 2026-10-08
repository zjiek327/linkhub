package api

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// 前端构建产物嵌入（go:embed，构建前需把 frontend/dist 拷到 web/dist/）
//go:embed all:dist
var frontendFS embed.FS

// FrontendHandler 返回内嵌前端的 handler（SPA 回退 index.html）
func FrontendHandler() http.Handler {
	distFS, err := fs.Sub(frontendFS, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		// 文件不存在则回退 index.html（SPA 路由）
		if _, err := fs.Stat(distFS, path); err != nil {
			path = "index.html"
		}
		http.ServeFileFS(w, r, distFS, path)
	})
}
