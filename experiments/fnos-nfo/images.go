package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"light-media/experiments/internal/fixturelayout"
	"light-media/internal/logging"
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

type expectedImage struct {
	Label, Hash, Format string
	Width, Height       int
}
type expectedWork struct {
	Kind, Title string
	Fields      map[string]expectedImage
}

func fixtureRead(f string) ([]byte, error) {
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
	return b, nil
}
func fixtureCatalog(id, root string) (map[string]expectedWork, error) {
	if !uuidPattern.MatchString(id) {
		return nil, errors.New("INVALID_EXPERIMENT_ID")
	}
	if root == "" {
		root = filepath.Join("work", "nfo-live-fixture-v2")
	}
	abs, e := filepath.Abs(root)
	if e != nil {
		return nil, errors.New("INVALID_FIXTURE_ROOT")
	}
	work, _ := filepath.Abs("work")
	rel, e := filepath.Rel(work, abs)
	if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("INVALID_FIXTURE_ROOT")
	}
	works, e := fixturelayout.Detect(root, id)
	if e != nil {
		return nil, e
	}
	out := map[string]expectedWork{}
	seenHashes := map[string]bool{}
	for _, work := range works {
		dirname := work.Dir
		nfo := "tvshow.nfo"
		wantXML, wantType := "tvshow", "TV"
		if work.Group == "movie" {
			nfo = dirname + ".nfo"
			wantXML, wantType = "movie", "Movie"
		}
		b, e := fixtureRead(filepath.Join(root, dirname, nfo))
		if e != nil {
			return nil, e
		}
		var m struct {
			XMLName xml.Name
			Title   string `xml:"title"`
		}
		if xml.Unmarshal(b, &m) != nil || m.XMLName.Local != wantXML || !strings.HasPrefix(m.Title, prefix+id+" ") {
			return nil, errors.New("FIXTURE_IDENTITY_INVALID")
		}
		v := expectedWork{Kind: wantType, Title: m.Title, Fields: map[string]expectedImage{}}
		for _, role := range []struct{ field, base string }{{"posters", "poster"}, {"backdrops", "fanart"}} {
			var file string
			for _, ext := range []string{".jpg", ".png"} {
				candidate := filepath.Join(root, dirname, role.base+ext)
				if _, e := os.Lstat(candidate); e == nil {
					if file != "" {
						return nil, errors.New("AMBIGUOUS_FIXTURE_FILE")
					}
					file = candidate
				} else if !os.IsNotExist(e) {
					return nil, errors.New("FIXTURE_UNAVAILABLE")
				}
			}
			b, e = fixtureRead(file)
			if e != nil {
				return nil, e
			}
			cfg, format, e := image.DecodeConfig(bytes.NewReader(b))
			if e != nil || (format != "jpeg" && format != "png") || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 32*1024*1024 {
				return nil, errors.New("INVALID_FIXTURE_IMAGE")
			}
			if _, _, e = image.Decode(bytes.NewReader(b)); e != nil {
				return nil, errors.New("INVALID_FIXTURE_IMAGE")
			}
			hash := shaHex(b)
			if seenHashes[hash] {
				return nil, errors.New("AMBIGUOUS_FIXTURE_IMAGE_ROLES")
			}
			seenHashes[hash] = true
			v.Fields[role.field] = expectedImage{Label: work.Label + ":" + filepath.Base(file), Hash: hash, Format: format, Width: cfg.Width, Height: cfg.Height}
		}
		key := wantType + "\x00" + m.Title
		if _, ok := out[key]; ok {
			return nil, errors.New("DUPLICATE_FIXTURE_IDENTITY")
		}
		out[key] = v
	}
	return out, nil
}
func (a *api) fetchImage(ctx context.Context, target string, expected expectedImage) (map[string]any, error) {
	out := map[string]any{"bytes_verified": false}
	checked, _ := imageReference(a.host, target)
	if checked == "" || checked != target {
		return out, errors.New("IMAGE_TARGET_REFUSED")
	}
	if a.requests >= 32 {
		return out, errors.New("REQUEST_LIMIT")
	}
	r, e := http.NewRequestWithContext(logging.EndpointContext(ctx, "fnos", target), "GET", target, nil)
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
	out["fixture_expected"] = expected.Label
	out["dimensions_match"] = cfg.Width == expected.Width && cfg.Height == expected.Height
	out["format_match"] = format == expected.Format
	exact := hash == expected.Hash && cfg.Width == expected.Width && cfg.Height == expected.Height && format == expected.Format
	out["fixture_exact_match"] = exact
	if !exact {
		return out, errors.New("IMAGE_FIXTURE_ROLE_MISMATCH")
	}

	return out, nil
}
func (a *api) images(ctx context.Context, r receipt, fetch bool, fixtureDir string) ([]map[string]any, error) {
	var catalog map[string]expectedWork
	var e error
	if fetch {
		catalog, e = fixtureCatalog(r.ExperimentID, fixtureDir)
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
	seen := map[string]bool{}
	if fetch && len(page.List) != 3 {
		return out, errors.New("IMAGE_ITEMS_INCOMPLETE")
	}
	for _, item := range page.List {
		guid := stringField(item, "guid")
		if !guidPattern.MatchString(guid) {
			return out, errors.New("INVALID_ITEM_GUID")
		}
		var d map[string]json.RawMessage
		if e = a.call(ctx, "GET", "/item/"+guid, nil, &d); e != nil {
			return out, e
		}
		n := observedNode{guid: guid, parent: r.OwnedGUID, kind: stringField(item, "type")}
		if err := verifyItemScope(d, r, n); err != nil {
			return append(out, scopeDiagnostic(d, n)), err
		}
		kind := stringField(d, "type")
		if kind != "Movie" && kind != "TV" {
			return out, errors.New("ITEM_TYPE_MISMATCH")
		}
		entry := selected(d, []string{"guid", "title", "type", "ancestor_guid", "parent_guid"})
		entry["scope_verified"] = true
		var expected expectedWork
		if fetch {
			key := kind + "\x00" + stringField(d, "title")
			var ok bool
			expected, ok = catalog[key]
			if !ok {
				return out, errors.New("UNKNOWN_IMAGE_FIXTURE_IDENTITY")
			}
			if seen[key] {
				return out, errors.New("DUPLICATE_IMAGE_FIXTURE_IDENTITY")
			}
			seen[key] = true
		}
		fields := map[string]any{}
		entry["images"] = fields
		out = append(out, entry)
		for _, key := range []string{"posters", "backdrops"} {
			raw, exists := d[key]
			if !exists {
				fields[key] = map[string]any{"status": "missing"}
				if fetch {
					return out, errors.New("IMAGE_FIELD_MISSING")
				}
				continue
			}
			var ref string
			if json.Unmarshal(raw, &ref) != nil {
				fields[key] = map[string]any{"status": "unsupported_field_type"}
				if fetch {
					return out, errors.New("IMAGE_FIELD_INVALID")
				}
				continue
			}
			target, shape := imageReference(a.host, ref)
			fields[key] = shape
			if !fetch {
				continue
			}
			if target == "" {
				return out, errors.New("IMAGE_REFERENCE_REFUSED")
			}
			if gets >= 6 {
				return out, errors.New("IMAGE_REQUEST_LIMIT")
			}
			gets++
			result, err := a.fetchImage(ctx, target, expected.Fields[key])
			shape["verification"] = result
			if err != nil {
				shape["error_code"] = err.Error()
				return out, err
			}
		}
	}
	return out, nil
}
