package mergemessagesmtp

// Source: actual CPython 3.12.13 email._parseaddr.AddrlistClass and strict
// email.utils.parseaddr. Retains the original PSF license under reference/.
import (
	"strings"
	"unicode/utf8"
)

type address struct{ name, addr string }
type addressParser struct {
	field      []rune
	pos, depth int
	comments   *[]string
	err        error
}

const specials = "()<>@,:;.\"[]"
const atomEnds = specials + " \t\r\n"
const phraseEnds = "()<>@,:;\"[] \t\r\n"

func (a *addressParser) valid() bool { return a.pos < len(a.field) }
func (a *addressParser) has(chars string) bool {
	return a.valid() && strings.ContainsRune(chars, a.field[a.pos])
}
func (a *addressParser) enter() bool {
	a.depth++
	// CPython's global recursion threshold depends on the caller's active
	// Python frames. Do not accept deeper input as equivalent just because Go
	// can recurse further; expose a bounded unsupported branch explicitly.
	if a.depth > 400 {
		a.err = ErrUnsupported
		a.pos = len(a.field)
		return false
	}
	return true
}
func (a *addressParser) next() string {
	var ws strings.Builder
	for a.valid() {
		if a.has(" \t\r\n") {
			if !a.has("\r\n") {
				ws.WriteRune(a.field[a.pos])
			}
			a.pos++
		} else if a.has("(") {
			*a.comments = append(*a.comments, a.delimited('(', ")\r", true))
		} else {
			break
		}
	}
	return ws.String()
}
func (a *addressParser) list() []address {
	out := []address{}
	for a.valid() {
		items := a.address()
		if len(items) > 0 {
			out = append(out, items...)
		} else {
			out = append(out, address{})
		}
	}
	return out
}
func (a *addressParser) address() []address {
	if !a.enter() {
		return nil
	}
	defer func() { a.depth-- }()
	cl := []string{}
	a.comments = &cl
	a.next()
	oldpos, oldcl := a.pos, a.comments
	phrases := a.phrases()
	a.next()
	out := []address{}
	if !a.valid() {
		if len(phrases) > 0 {
			out = append(out, address{strings.Join(*a.comments, " "), phrases[0]})
		}
	} else if a.has(".@") {
		a.pos = oldpos
		a.comments = oldcl
		spec := a.spec()
		out = append(out, address{strings.Join(*a.comments, " "), spec})
	} else if a.has(":") {
		a.pos++
		for a.valid() {
			a.next()
			if a.has(";") {
				a.pos++
				break
			}
			out = append(out, a.address()...)
		}
	} else if a.has("<") {
		route := a.route()
		name := strings.Join(phrases, " ")
		if len(*a.comments) > 0 {
			name += " (" + strings.Join(*a.comments, " ") + ")"
		}
		out = append(out, address{name, route})
	} else {
		if len(phrases) > 0 {
			out = append(out, address{strings.Join(*a.comments, " "), phrases[0]})
		} else if a.has(specials) {
			a.pos++
		}
	}
	a.next()
	if a.has(",") {
		a.pos++
	}
	return out
}
func (a *addressParser) route() string {
	if !a.has("<") {
		return ""
	}
	expect := false
	a.pos++
	a.next()
	addr := ""
	for a.valid() {
		if expect {
			a.domain()
			expect = false
		} else if a.has(">") {
			a.pos++
			break
		} else if a.has("@") {
			a.pos++
			expect = true
		} else if a.has(":") {
			a.pos++
		} else {
			addr = a.spec()
			a.pos++
			break
		}
		a.next()
	}
	return addr
}
func (a *addressParser) spec() string {
	parts := []string{}
	a.next()
	for a.valid() {
		preserve := true
		if a.has(".") {
			if len(parts) > 0 && pythonStrip(parts[len(parts)-1]) == "" {
				parts = parts[:len(parts)-1]
			}
			parts = append(parts, ".")
			a.pos++
			preserve = false
		} else if a.has("\"") {
			s := a.delimited('"', "\"\r", false)
			parts = append(parts, "\""+strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(s)+"\"")
		} else if a.has(atomEnds) {
			if len(parts) > 0 && pythonStrip(parts[len(parts)-1]) == "" {
				parts = parts[:len(parts)-1]
			}
			break
		} else {
			parts = append(parts, a.atom(atomEnds))
		}
		ws := a.next()
		if preserve && ws != "" {
			parts = append(parts, ws)
		}
	}
	if !a.has("@") {
		return strings.Join(parts, "")
	}
	parts = append(parts, "@")
	a.pos++
	a.next()
	domain := a.domain()
	if domain == "" {
		return ""
	}
	return strings.Join(parts, "") + domain
}
func (a *addressParser) domain() string {
	parts := []string{}
	for a.valid() {
		if a.has(" \t") {
			a.pos++
		} else if a.has("(") {
			*a.comments = append(*a.comments, a.delimited('(', ")\r", true))
		} else if a.has("[") {
			parts = append(parts, "["+a.delimited('[', "]\r", false)+"]")
		} else if a.has(".") {
			a.pos++
			parts = append(parts, ".")
		} else if a.has("@") {
			return ""
		} else if a.has(atomEnds) {
			break
		} else {
			parts = append(parts, a.atom(atomEnds))
		}
	}
	return strings.Join(parts, "")
}
func (a *addressParser) delimited(begin rune, ends string, comments bool) string {
	if !a.valid() || a.field[a.pos] != begin {
		return ""
	}
	if !a.enter() {
		return ""
	}
	defer func() { a.depth-- }()
	var out strings.Builder
	escaped := false
	a.pos++
	for a.valid() {
		r := a.field[a.pos]
		if escaped {
			out.WriteRune(r)
			escaped = false
		} else if strings.ContainsRune(ends, r) {
			a.pos++
			break
		} else if comments && r == '(' {
			out.WriteString(a.delimited('(', ")\r", true))
			continue
		} else if r == '\\' {
			escaped = true
		} else {
			out.WriteRune(r)
		}
		a.pos++
	}
	return out.String()
}
func (a *addressParser) atom(ends string) string {
	start := a.pos
	for a.valid() && !a.has(ends) {
		a.pos++
	}
	return string(a.field[start:a.pos])
}
func (a *addressParser) phrases() []string {
	out := []string{}
	for a.valid() {
		if a.has(" \t\r\n") {
			a.pos++
		} else if a.has("\"") {
			out = append(out, a.delimited('"', "\"\r", false))
		} else if a.has("(") {
			*a.comments = append(*a.comments, a.delimited('(', ")\r", true))
		} else if a.has(phraseEnds) {
			break
		} else {
			out = append(out, a.atom(phraseEnds))
		}
	}
	return out
}
func escapedChars(s []rune, fn func(int, string)) {
	escaped := false
	pos := 0
	for i, r := range s {
		pos = i
		if escaped {
			fn(i, "\\"+string(r))
			escaped = false
		} else if r == '\\' {
			escaped = true
		} else {
			fn(i, string(r))
		}
	}
	if escaped {
		fn(pos, "\\")
	}
}
func stripQuoted(s string) string {
	if !strings.ContainsRune(s, '"') {
		return s
	}
	r := []rune(s)
	start, open := 0, -1
	var out strings.Builder
	escapedChars(r, func(pos int, ch string) {
		if ch != "\"" {
			return
		}
		if open < 0 {
			open = pos
		} else {
			if start != open {
				out.WriteString(string(r[start:open]))
			}
			start = pos + 1
			open = -1
		}
	})
	if start < len(r) {
		out.WriteString(string(r[start:]))
	}
	return out.String()
}
func balancedComments(s string) bool {
	count := 0
	valid := true
	escapedChars([]rune(stripQuoted(s)), func(_ int, ch string) {
		if ch == "(" {
			count++
		} else if ch == ")" {
			count--
			if count < 0 {
				valid = false
			}
		}
	})
	return valid && count == 0
}
func parseAddress(s string) (address, error) {
	if !utf8.ValidString(s) {
		return address{}, failure("UnicodeEncodeError", "address")
	}
	if !balancedComments(s) {
		s = "('', '')"
	}
	comments := []string{}
	p := addressParser{field: []rune(s), comments: &comments}
	items := p.list()
	if p.err != nil {
		return address{}, p.err
	}
	for i, v := range items {
		if strings.Contains(v.addr, "[") {
			items[i] = address{}
		}
	}
	if len(items) != 1 {
		return address{}, nil
	}
	return items[0], nil
}
func quoteAddr(s string) (string, error) {
	a, e := parseAddress(s)
	if e != nil {
		return "", e
	}
	if a.name == "" && a.addr == "" {
		if strings.HasPrefix(pythonStrip(s), "<") {
			return s, nil
		}
		return "<" + s + ">", nil
	}
	return "<" + a.addr + ">", nil
}
