// 前端静态文件:构建产物在 www/ 里,挂在网关前缀之下。单页应用,刷新
// 任何路径都回 index.html。

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func serveStatic(mux *http.ServeMux, base, www string) {
	mux.HandleFunc(base+"/", func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, base), "/")
		if rel != "" {
			if full := filepath.Join(www, filepath.Clean("/"+rel)); isRegularFile(full) {
				w.Header().Set("Cache-Control", "no-cache")
				http.ServeFile(w, r, full)
				return
			}
		}
		// 入口,或单页应用的任意深链接。
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(www, "index.html"))
	})
}

func isRegularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
