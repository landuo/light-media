package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"light-media/internal/catalog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// catalogExploreImage only proxies the fixed provider CDN allowlist used by
// the metadata decoder, with bounded reads and no redirects or browser tokens.
func (a *App) catalogExploreImage(w http.ResponseWriter, r *http.Request) error {
	source, raw := r.URL.Query().Get("source"), r.URL.Query().Get("url")
	imageURL := catalog.ExploreImageURL(source, raw)
	if imageURL == "" {
		return bad("INVALID_IMAGE_PATH", "来源图片地址无效")
	}
	sum := sha256.Sum256([]byte(source + ":" + imageURL))
	name := hex.EncodeToString(sum[:]) + ".img"
	dir := filepath.Join(a.Dir, "catalog-explore-images")
	cache := filepath.Join(dir, name)
	var releaseFlight func()
	serve := func() bool {
		f, err := importOpen(cache)
		if err != nil {
			return false
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.Size() <= 0 || st.Size() > 8<<20 {
			return false
		}
		contentType, err := catalogImageContentType(f)
		if err != nil {
			return false
		}
		// Cache filenames are not MIME evidence; inspect and rewind the bytes.
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
	releaseSlot, slotErr := a.acquireImageSlot(ctx, source)
	if slotErr != nil {
		return slotErr
	}
	defer releaseSlot()
	if serve() {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return bad("IMAGE_UNAVAILABLE", "无法建立图片请求")
	}
	req.Header.Set("Accept", "image/jpeg,image/png,image/webp")
	req.Header.Set("User-Agent", "light-media/1.0")
	if source == "douban" {
		req.Header.Set("Referer", "https://movie.douban.com/")
	}
	response, err := a.metadataImageRequest(req)
	if err != nil {
		return bad("IMAGE_UNAVAILABLE", "来源图片暂时不可用")
	}
	defer response.Body.Close()
	content := strings.Split(response.Header.Get("Content-Type"), ";")[0]
	if response.StatusCode != 200 || response.ContentLength > 8<<20 || (content != "image/jpeg" && content != "image/png" && content != "image/webp") {
		return bad("IMAGE_UNAVAILABLE", "来源图片未返回可用结果")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	response.Body.Close()
	releaseSlot()
	if err != nil {
		return bad("IMAGE_UNAVAILABLE", "来源图片读取失败")
	}
	actual := http.DetectContentType(data)
	if len(data) == 0 || len(data) > 8<<20 || (actual != "image/jpeg" && actual != "image/png" && actual != "image/webp") {
		return bad("IMAGE_UNAVAILABLE", "来源图片数据无效")
	}
	d, err := importDir(dir, true)
	if err != nil {
		return err
	}
	d.Close()
	tmp, err := os.CreateTemp(dir, ".image-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmpName, cache); err != nil {
		return err
	}
	a.scheduleCatalogImagePrune(dir)
	if !serve() {
		return bad("IMAGE_UNAVAILABLE", "图片缓存不可访问")
	}
	return nil
}
