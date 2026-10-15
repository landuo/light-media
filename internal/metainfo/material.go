package metainfo

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}
type Material struct {
	Summary
	Hash  string `json:"hash"`
	Name  string `json:"name"`
	Files []File `json:"files"`
}

// ParseMaterial uses the exact original info dictionary for the identity and
// exposes safe relative content paths only. Tracker/passkey bytes are omitted.
func ParseMaterial(data []byte) (Material, error) {
	s, e := ProductInspect(data)
	if e != nil {
		return Material{}, e
	}
	scan := scanner{data: data, maxBody: 32 << 20}
	root, e := scan.value(0)
	if e != nil {
		return Material{}, e
	}
	info := root.get("info")
	name := string(info.get("name").text)
	if n := info.get("name.utf-8"); isString(n) {
		name = string(n.text)
	}
	if !safeComponent(name) {
		return Material{}, invalid("unsafe_torrent_path")
	}
	m := Material{Summary: s, Name: name, Files: []File{}}
	if s.V1 {
		h := sha1.Sum(data[info.start:info.end])
		m.Hash = hex.EncodeToString(h[:])
		if single := info.get("length"); single != nil {
			m.Files = append(m.Files, File{name, single.integer})
		} else {
			for _, f := range info.get("files").list {
				if attr := f.get("attr"); isString(attr) && strings.Contains(string(attr.text), "p") {
					continue
				}
				n := f.get("path.utf-8")
				if n == nil {
					n = f.get("path")
				}
				if n == nil || n.kind != 'l' {
					return Material{}, invalid("unsafe_torrent_path")
				}
				parts := []string{name}
				for _, p := range n.list {
					if !isString(p) || !safeComponent(string(p.text)) {
						return Material{}, invalid("unsafe_torrent_path")
					}
					parts = append(parts, string(p.text))
				}
				m.Files = append(m.Files, File{path.Join(parts...), f.get("length").integer})
			}
		}
	} else {
		h := sha256.Sum256(data[info.start:info.end])
		m.Hash = hex.EncodeToString(h[:])
		var walk func(*node, []string) error
		walk = func(n *node, parts []string) error {
			if leaf := n.get(""); leaf != nil {
				m.Files = append(m.Files, File{path.Join(append([]string{name}, parts...)...), leaf.get("length").integer})
				return nil
			}
			for _, p := range n.dict {
				part := string(p.key)
				if !safeComponent(part) {
					return invalid("unsafe_torrent_path")
				}
				if e := walk(p.value, append(append([]string{}, parts...), part)); e != nil {
					return e
				}
			}
			return nil
		}
		if e = walk(info.get("file tree"), nil); e != nil {
			return Material{}, e
		}
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if seen[f.Path] {
			return Material{}, invalid("duplicate_torrent_path")
		}
		seen[f.Path] = true
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	return m, nil
}
func safeComponent(s string) bool {
	return s != "" && s != "." && s != ".." && len(s) <= 255 && utf8.ValidString(s) && !strings.ContainsAny(s, "/\\\x00\r\n")
}
