package directorysource

import "strings"

type sourcePath struct {
	anchor string
	parts  []string
}

// PathString is the fixed pathlib lexical rendering. It preserves parent
// components; filesystem authority and traversal checks belong to the caller.
func PathString(raw string) string {
	p, _ := parsePath(raw)
	return p.String()
}

func parsePath(raw any) (sourcePath, error) {
	s, ok := raw.(string)
	if !ok {
		return sourcePath{}, sourceType("NoneType")
	}
	p := sourcePath{}
	if strings.HasPrefix(s, "/") {
		p.anchor = "/"
		if strings.HasPrefix(s, "//") && !strings.HasPrefix(s, "///") {
			p.anchor = "//"
		}
	}
	for _, v := range strings.Split(s, "/") {
		if v != "" && v != "." {
			p.parts = append(p.parts, v)
		}
	}
	return p, nil
}
func (p sourcePath) String() string {
	v := p.anchor + strings.Join(p.parts, "/")
	if v == "" {
		return "."
	}
	return v
}
func (p sourcePath) under(q sourcePath) bool {
	if p.anchor != q.anchor || len(p.parts) < len(q.parts) {
		return false
	}
	for i, s := range q.parts {
		if s != p.parts[i] {
			return false
		}
	}
	return true
}
func (p sourcePath) equal(q sourcePath) bool { return len(p.parts) == len(q.parts) && p.under(q) }
func (p sourcePath) parents() []sourcePath {
	out := []sourcePath{}
	for n := len(p.parts) - 1; n >= 0; n-- {
		out = append(out, sourcePath{p.anchor, append([]string(nil), p.parts[:n]...)})
	}
	return out
}
