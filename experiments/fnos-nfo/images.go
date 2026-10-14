package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const localImageRoute = "/v/api/v1/sys/img/"

var safeImageSegment = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]{0,255}$`)

// Only references returned by an already verified owned item enter this helper.
// No external/remote-proxy URL, query, traversal, encoded separator or arbitrary route.
func imageReference(host, ref string) (string, map[string]any) {
	shape := map[string]any{"field_type": "string", "present": ref != "", "fetch_allowed": false}
	if ref == "" {
		shape["status"] = "empty"
		return "", shape
	}
	if len(ref) > 2048 {
		shape["status"] = "oversized"
		return "", shape
	}
	u, e := url.Parse(ref)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		shape["status"] = "unsafe_reference"
		return "", shape
	}
	base, _ := url.Parse(host)
	p := u.Path
	if u.IsAbs() || u.Host != "" {
		if u.Scheme != base.Scheme || u.Host != base.Host {
			shape["status"] = "external_refused"
			return "", shape
		}
		if !strings.HasPrefix(p, localImageRoute) {
			shape["status"] = "route_refused"
			return "", shape
		}
	} else if !strings.HasPrefix(p, localImageRoute) {
		if strings.HasPrefix(p, "/v/") {
			shape["status"] = "route_refused"
			return "", shape
		}
		p = localImageRoute + strings.TrimPrefix(p, "/")
	}
	tail := strings.TrimPrefix(p, localImageRoute)
	parts := strings.Split(tail, "/")
	if tail == p || len(parts) > 12 {
		shape["status"] = "route_refused"
		return "", shape
	}
	for _, part := range parts {
		if !safeImageSegment.MatchString(part) || part == "." || part == ".." {
			shape["status"] = "unsafe_segment"
			return "", shape
		}
	}
	shape["route_template"] = localImageRoute + "<opaque>"
	shape["segments"] = len(parts)
	ext := strings.ToLower(path.Ext(p))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
		shape["extension"] = ext
	default:
		shape["extension"] = "other"
	}
	shape["fetch_allowed"] = true
	shape["status"] = "same_origin_local_image"
	return base.Scheme + "://" + base.Host + p, shape
}
func shaHex(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func fixtureHashes(id string) (map[string][]string, error) {
	hashes := map[string][]string{}
	for _, suffix := range []string{"-Movie (2024)", "-Series", "-Animation"} {
		for _, name := range []string{"poster.jpg", "fanart.jpg"} {
			f := filepath.Join("work", "nfo-live-fixture-v2", prefix+id+suffix, name)
			info, e := os.Lstat(f)
			if e != nil || !info.Mode().IsRegular() || info.Size() > 2*1024*1024 {
				return nil, errors.New("FIXTURE_UNAVAILABLE")
			}
			actual, e := filepath.EvalSymlinks(f)
			if e != nil {
				return nil, errors.New("FIXTURE_UNAVAILABLE")
			}
			abs, _ := filepath.Abs(f)
			resolved, _ := filepath.Abs(actual)
			if abs != resolved {
				return nil, errors.New("FIXTURE_SYMLINK_REFUSED")
			}
			b, e := os.ReadFile(f)
			if e != nil {
				return nil, errors.New("FIXTURE_UNAVAILABLE")
			}
			if _, format, e := image.DecodeConfig(bytes.NewReader(b)); e != nil || format != "jpeg" {
				return nil, errors.New("INVALID_FIXTURE_JPEG")
			}
			hash := shaHex(b)
			hashes[hash] = append(hashes[hash], strings.TrimPrefix(suffix, "-")+":"+name)
		}
	}
	return hashes, nil
}
func (a *api) fetchImage(ctx context.Context, target string, hashes map[string][]string) (map[string]any, error) {
	out := map[string]any{"bytes_verified": false}
	checked, _ := imageReference(a.host, target)
	if checked == "" || checked != target {
		return out, errors.New("IMAGE_TARGET_REFUSED")
	}
	if a.requests >= 32 {
		return out, errors.New("REQUEST_LIMIT")
	}
	r, e := http.NewRequestWithContext(ctx, "GET", target, nil)
	if e != nil {
		return out, errors.New("INVALID_IMAGE_REQUEST")
	}
	// Installed public client excludes /sys/img/ from authx signing. Authentication
	// is sent only to a strictly checked same-origin local image path.
	r.Header.Set("Authorization", a.token)
	r.Header.Set("Accept", "image/jpeg,image/png")
	r.Header.Set("Referer", a.host)
	a.requests++
	resp, e := a.client.Do(r)
	if e != nil {
		return out, errors.New("IMAGE_TRANSPORT_FAILED")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out, fmt.Errorf("IMAGE_HTTP_%d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if e != nil || len(b) > 2*1024*1024 {
		return out, errors.New("IMAGE_BODY_LIMIT")
	}
	cfg, format, e := image.DecodeConfig(bytes.NewReader(b))
	if e != nil || (format != "jpeg" && format != "png") {
		return out, errors.New("INVALID_IMAGE_BYTES")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 32*1024*1024 {
		return out, errors.New("IMAGE_DIMENSION_LIMIT")
	}
	if _, _, e = image.Decode(bytes.NewReader(b)); e != nil {
		return out, errors.New("INVALID_IMAGE_BYTES")
	}
	hash := shaHex(b)
	out["bytes_verified"] = true
	out["format"] = format
	out["byte_count"] = len(b)
	out["width"] = cfg.Width
	out["height"] = cfg.Height
	out["sha256"] = hash
	matches := hashes[hash]
	out["fixture_exact_match"] = len(matches) > 0
	if len(matches) > 0 {
		out["fixture_matches"] = matches
	}
	return out, nil
}
func (a *api) images(ctx context.Context, r receipt, fetch bool) ([]map[string]any, error) {
	var hashes map[string][]string
	var e error
	if fetch {
		hashes, e = fixtureHashes(r.ExperimentID)
		if e != nil {
			return nil, e
		}
	}
	var page struct {
		List []map[string]json.RawMessage `json:"list"`
	}
	if e = a.call(ctx, "POST", "/item/list", map[string]any{"ancestor_guid": r.OwnedGUID, "tags": map[string]any{"type": []string{"Movie", "TV"}}, "page": 1, "page_size": 3, "sort_type": "DESC", "sort_column": "create_time", "exclude_grouped_video": 1}, &page); e != nil {
		return nil, e
	}
	if page.List == nil || len(page.List) > 3 {
		return nil, errors.New("INVALID_IMAGE_ITEM_LIST")
	}
	out := []map[string]any{}
	gets := 0
	for _, item := range page.List {
		guid := stringField(item, "guid")
		if !guidPattern.MatchString(guid) {
			return out, errors.New("INVALID_ITEM_GUID")
		}
		var d map[string]json.RawMessage
		if e = a.call(ctx, "GET", "/item/"+guid, nil, &d); e != nil {
			return out, e
		}
		if stringField(d, "guid") != guid {
			return out, errors.New("ITEM_ID_MISMATCH")
		}
		entry := selected(d, []string{"guid", "title", "type"})
		fields := map[string]any{}
		entry["images"] = fields
		out = append(out, entry)
		for _, key := range []string{"posters", "backdrops"} {
			raw, exists := d[key]
			if !exists {
				fields[key] = map[string]any{"status": "missing"}
				continue
			}
			var ref string
			if json.Unmarshal(raw, &ref) != nil {
				fields[key] = map[string]any{"status": "unsupported_field_type"}
				continue
			}
			target, shape := imageReference(a.host, ref)
			fields[key] = shape
			if !fetch || target == "" {
				continue
			}
			if gets >= 6 {
				return out, errors.New("IMAGE_REQUEST_LIMIT")
			}
			gets++
			result, err := a.fetchImage(ctx, target, hashes)
			shape["verification"] = result
			if err != nil {
				shape["error_code"] = err.Error()
				return out, err
			}
		}
	}
	return out, nil
}
