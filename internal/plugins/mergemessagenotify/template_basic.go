package mergemessagenotify

import (
	"context"
	"encoding/json"
	"strings"
)

// RenderBasic is an explicitly limited dependency candidate, not a replacement
// for the full Mako 1.4.1 language. It accepts literal text, ${identifier}, and
// line-level % if identifier: / % endif used by the original OneBot default.
// Other executable Mako syntax is rejected, never treated as equivalent text.
func RenderBasic(_ context.Context, text any, vars map[string]any) (any, error) {
	s, e := stringValue(text)
	if e != nil {
		return nil, e
	}
	if strings.Contains(s, "<%") || strings.Contains(s, "##") || strings.Contains(s, "\\\n") || strings.Contains(s, "\\\r\n") {
		return nil, ErrDependency
	}
	lines := strings.SplitAfter(s, "\n")
	var b strings.Builder
	active := []bool{true}
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "%%") {
			return nil, ErrDependency
		}
		if strings.HasPrefix(trim, "% ") {
			if strings.HasPrefix(trim, "% if ") && strings.HasSuffix(trim, ":") {
				name := strings.TrimSuffix(strings.TrimPrefix(trim, "% if "), ":")
				if !identifier(name) {
					return nil, ErrDependency
				}
				active = append(active, active[len(active)-1] && truth(vars[name]))
				continue
			}
			if trim == "% endif" {
				if len(active) == 1 {
					return s, nil
				}
				active = active[:len(active)-1]
				continue
			}
			return nil, ErrDependency
		}
		if !active[len(active)-1] {
			continue
		}
		for {
			i := strings.Index(line, "${")
			if i < 0 {
				b.WriteString(line)
				break
			}
			b.WriteString(line[:i])
			line = line[i+2:]
			end := strings.IndexByte(line, '}')
			if end < 0 {
				return s, nil
			}
			name := line[:end]
			if !identifier(name) {
				return nil, ErrDependency
			}
			v, ok := vars[name]
			if !ok {
				return s, nil
			}
			switch v.(type) {
			case nil, string, bool, int, float64, json.Number:
			default:
				return nil, ErrDependency
			}
			b.WriteString(str(v))
			line = line[end+1:]
		}
	}
	if len(active) != 1 {
		return s, nil
	}
	return b.String(), nil
}
func identifier(s string) bool {
	if s == "" {
		return false
	}
	if strings.Contains(" False None True and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield ", " "+s+" ") {
		return false
	}
	for i, c := range s {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
