package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var catalogImageSlots = make(chan struct{}, 4)

// Metadata images share the application's outbound route and a bounded local
// cache, so browsers do not need their own TMDB connection or credential.
func (a *App) catalogImage(w http.ResponseWriter, r *http.Request) error {
	relative, size := r.URL.Query().Get("path"), r.URL.Query().Get("size")
	if size == "" {
		size = "w342"
	}
	if !assetImagePattern.MatchString(relative) || len(relative) > 128 {
		return bad("INVALID_IMAGE_PATH", "图片路径无效")
	}
	allowed := map[string]bool{"w92": true, "w185": true, "w342": true, "w500": true, "w780": true, "w1280": true}
	if !allowed[size] {
		return bad("INVALID_IMAGE_SIZE", "图片尺寸无效")
	}
	sum := sha256.Sum256([]byte(size + relative))
	name := hex.EncodeToString(sum[:]) + ".img"
	dir := filepath.Join(a.Dir, "catalog-images")
	cache := filepath.Join(dir, name)
	var releaseFlight func()
	serve := func() bool {
		f, e := importOpen(cache)
		if e != nil {
			return false
		}
		defer f.Close()
		st, e := f.Stat()
		if e != nil || st.Size() <= 0 || st.Size() > 8<<20 {
			return false
		}
		contentType, e := catalogImageContentType(f)
		if e != nil {
			return false
		}
		// The cache uses .img names, whose system MIME mapping is not an image
		// type on every host. Trust the bounded cache bytes, not that suffix.
		// The open, validated cache file is stable; slow readers do not hold a flight.
		if releaseFlight != nil {
			releaseFlight()
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, name, st.ModTime(), f)
		return true
	}
	if serve() {
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var flightErr error
	releaseFlight, flightErr = a.acquireImageFlight(ctx, cache)
	if flightErr != nil {
		return flightErr
	}
	defer releaseFlight()
	// A preceding request may have populated the cache while we waited.
	if serve() {
		return nil
	}
	releaseSlot, slotErr := a.acquireImageSlot(ctx, "tmdb")
	if slotErr != nil {
		return slotErr
	}
	defer releaseSlot()
	if serve() {
		return nil
	}
	d, e := importDir(dir, true)
	if e != nil {
		return e
	}
	d.Close()
	u := url.URL{Scheme: "https", Host: "image.tmdb.org", Path: "/t/p/" + size + relative}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return bad("IMAGE_UNAVAILABLE", "无法建立图片请求")
	}
	req.Header.Set("Accept", "image/jpeg,image/png,image/webp")
	response, e := a.metadataImageRequest(req)
	if e != nil {
		return bad("IMAGE_UNAVAILABLE", "TMDB 图片暂时不可用")
	}
	defer response.Body.Close()
	content := strings.Split(response.Header.Get("Content-Type"), ";")[0]
	if response.StatusCode != 200 || (content != "image/jpeg" && content != "image/png" && content != "image/webp") || response.ContentLength > 8<<20 {
		return bad("IMAGE_UNAVAILABLE", "TMDB 图片未返回可用结果")
	}
	tmp, e := os.CreateTemp(dir, ".image-*")
	if e != nil {
		return e
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	n, e := io.Copy(tmp, io.LimitReader(response.Body, 8<<20+1))
	response.Body.Close()
	releaseSlot()
	if e == nil && (n <= 0 || n > 8<<20) {
		e = bad("IMAGE_SIZE", "图片超过大小限制")
	}
	if e == nil {
		_, e = catalogImageContentType(tmp)
	}
	if e == nil {
		e = tmp.Sync()
	}
	closeErr := tmp.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(tmpName, cache); e != nil {
		return e
	}
	a.scheduleCatalogImagePrune(dir)
	if !serve() {
		return bad("IMAGE_UNAVAILABLE", "图片缓存不可访问")
	}
	return nil
}

// Inspect both new downloads and existing cache entries. The upstream header
// alone cannot establish the response type, and ServeContent needs offset zero
// after inspection to preserve full-body, HEAD and Range behavior.
func catalogImageContentType(f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	var header [512]byte
	n, err := io.ReadFull(f, header[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	contentType := http.DetectContentType(header[:n])
	if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp" {
		return "", bad("IMAGE_UNAVAILABLE", "图片服务返回了非图片内容")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return contentType, nil
}

func (a *App) pruneCatalogImages(dir string) {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return
	}
	type cached struct {
		name string
		at   time.Time
		size int64
	}
	var files []cached
	var total int64
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() || len(entry.Name()) != 68 || !strings.HasSuffix(entry.Name(), ".img") {
			continue
		}
		st, e := entry.Info()
		if e != nil {
			continue
		}
		files = append(files, cached{entry.Name(), st.ModTime(), st.Size()})
		total += st.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	remaining := len(files)
	for _, file := range files {
		if total <= 200<<20 && remaining <= 1024 && time.Since(file.at) < 14*24*time.Hour {
			break
		}
		if os.Remove(filepath.Join(dir, file.name)) == nil {
			total -= file.size
			remaining--
		}
	}
}
