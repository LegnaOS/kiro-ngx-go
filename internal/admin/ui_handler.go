// Package admin Admin UI 静态文件服务
package admin

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed ui
var uiFS embed.FS

// RegisterUIHandler 注册 Admin UI 静态文件路由，挂载在 /admin 前缀下
func RegisterUIHandler(mux *http.ServeMux) {
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic(err)
	}

	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		serveIndex(w, sub)
	})

	mux.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/admin/")

		if strings.Contains(path, "..") {
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}

		if path == "" {
			serveIndex(w, sub)
			return
		}

		f, err := sub.Open(path)
		if err != nil {
			// SPA fallback：非资源路径返回 index.html
			if !isAssetPath(path) {
				serveIndex(w, sub)
				return
			}
			http.NotFound(w, r)
			return
		}
		f.Close()

		cacheControl := getCacheControl(path)
		w.Header().Set("Cache-Control", cacheControl)

		ext := filepath.Ext(path)
		if ct := mime.TypeByExtension(ext); ct != "" {
			w.Header().Set("Content-Type", ct)
		}

		http.FileServer(http.FS(sub)).ServeHTTP(w, stripAdminPrefix(r))
	})
}

func serveIndex(w http.ResponseWriter, sub fs.FS) {
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "Admin UI not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

func getCacheControl(path string) string {
	if strings.HasSuffix(path, ".html") {
		return "no-cache"
	}
	if strings.HasPrefix(path, "assets/") {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=3600"
}

func isAssetPath(path string) bool {
	last := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		last = path[idx+1:]
	}
	return strings.Contains(last, ".")
}

// stripAdminPrefix 将请求路径的 /admin/ 前缀去掉，供 FileServer 使用
func stripAdminPrefix(r *http.Request) *http.Request {
	r2 := r.Clone(r.Context())
	newURL := *r.URL
	newURL.Path = strings.TrimPrefix(r.URL.Path, "/admin")
	if newURL.Path == "" {
		newURL.Path = "/"
	}
	r2.URL = &newURL
	return r2
}
