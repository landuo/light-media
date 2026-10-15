package app

import (
	"mime"
	"net/http"
	"path/filepath"
)

func (a *App) serveBrowserContent(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if _, e := a.browserRoot(r.URL.Query().Get("root_id"), path); e != nil {
		return e
	}
	file, e := importOpen(path)
	if e != nil {
		return e
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() {
		return bad("INVALID_FILE", "只能下载普通文件")
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
	return nil
}
