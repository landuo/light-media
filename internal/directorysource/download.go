package directorysource

import (
	"regexp"
	"strings"
)

var drivePrefix = regexp.MustCompile(`^[A-Za-z]:`)
var driveAbsolute = regexp.MustCompile(`^[A-Za-z]:/`)
var driveSegment = regexp.MustCompile(`^[A-Za-z]:$`)
var storageNames = []string{"local", "alipan", "u115", "rclone", "alist", "alistgo", "smb"}

type downloadPath struct{ storage, style, path string }

func normalizeDownload(storage, raw string) (downloadPath, bool) {
	raw = strings.TrimSpace(raw)
	p := downloadPath{storage: storage, style: "posix"}
	if raw == "" || strings.Contains(raw, `\`) || strings.HasPrefix(raw, "//") {
		return p, false
	}
	windows := storage == "local" && drivePrefix.MatchString(raw)
	if windows {
		if !driveAbsolute.MatchString(raw) {
			return p, false
		}
		p.style = "windows"
	} else if !strings.HasPrefix(raw, "/") || driveAbsolute.MatchString(raw) {
		return p, false
	}
	parsed, _ := parsePath(raw)
	for _, s := range parsed.parts {
		if s == ".." {
			return p, false
		}
	}
	if !windows && len(parsed.parts) > 0 && driveSegment.MatchString(parsed.parts[0]) {
		return p, false
	}
	p.path = parsed.String()
	return p, true
}
func splitStorage(raw string) (string, string) {
	for _, s := range storageNames {
		if strings.HasPrefix(raw, s+":") {
			return s, strings.TrimPrefix(raw, s+":")
		}
	}
	return "local", raw
}
func DownloadDirBySavePath(known bool, rules []Rule, m *Media, save *string) (*Rule, error) {
	if !known {
		return nil, ErrDependency
	}
	raw := ""
	if save != nil {
		raw = strings.TrimSpace(*save)
	}
	storage, path := splitStorage(raw)
	want, ok := normalizeDownload(storage, path)
	if !ok {
		return nil, nil
	}
	list, e := List(true, rules, false, false)
	if e != nil {
		return nil, e
	}
	for _, r := range list {
		if !truth(r.Values["download_path"]) {
			continue
		}
		s := text(r.Values["storage"])
		if s == "" {
			s = "local"
		}
		root, valid := normalizeDownload(s, text(r.Values["download_path"]))
		if !valid || root.storage != want.storage || root.style != want.style {
			continue
		}
		equal := root.path == want.path
		if root.style == "windows" {
			equal = strings.EqualFold(root.path, want.path)
		}
		if !equal {
			continue
		}
		yes, e := matchMedia(r, m)
		if e != nil {
			return nil, e
		}
		if yes {
			v := r
			return &v, nil
		}
	}
	return nil, nil
}
