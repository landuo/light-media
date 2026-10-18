package brushflow

// Consumed pure-Go port of MoviePilot-Rust0.2.6 src/rss/parser.rs and bindings/rss.rs.
// The streaming reader keeps source field/depth/order rules; it does not run a
// Rust/Python library or resolve XML external entities.
import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type rssField struct {
	kind  string
	depth int
}
type rssToken struct {
	kind, name, text string
	attrs            string
}
type rssReader struct {
	text  string
	pos   int
	stack []string
}

func rssSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
func rssLocal(s string) string {
	if at := strings.LastIndexByte(s, ':'); at >= 0 {
		s = s[at+1:]
	}
	return strings.ToLower(s)
}
func rssUnescape(s string, attribute bool) (string, error) {
	var b strings.Builder
	for len(s) > 0 {
		i := strings.IndexByte(s, '&')
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		s = s[i+1:]
		j := strings.IndexByte(s, ';')
		if j < 0 {
			return "", fmt.Errorf("unterminated XML reference")
		}
		ref := s[:j]
		s = s[j+1:]
		if strings.HasPrefix(ref, "#") {
			n, base := ref[1:], 10
			if strings.HasPrefix(n, "x") {
				n, base = n[1:], 16
			}
			v, e := strconv.ParseUint(n, base, 32)
			if e != nil || v == 0 || v > utf8.MaxRune || v >= 0xd800 && v <= 0xdfff {
				return "", fmt.Errorf("invalid XML character reference")
			}
			b.WriteRune(rune(v))
			continue
		}
		switch ref {
		case "amp":
			b.WriteByte('&')
		case "lt":
			b.WriteByte('<')
		case "gt":
			b.WriteByte('>')
		case "apos":
			b.WriteByte('\'')
		case "quot":
			b.WriteByte('"')
		default:
			if attribute {
				return "", fmt.Errorf("unknown XML attribute reference")
			}
			b.WriteString("&" + ref + ";")
		}
	}
	return b.String(), nil
}
func (r *rssReader) next() (rssToken, error) {
	if r.pos >= len(r.text) {
		return rssToken{kind: "eof"}, nil
	}
	s := r.text[r.pos:]
	if s[0] != '<' {
		end := strings.IndexByte(s, '<')
		if end < 0 {
			end = len(s)
		}
		r.pos += end
		return rssToken{kind: "text", text: s[:end]}, nil
	}
	for _, v := range []struct{ prefix, suffix, kind string }{{"<!--", "-->", "skip"}, {"<![CDATA[", "]]>", "cdata"}, {"<?", "?>", "skip"}} {
		if strings.HasPrefix(s, v.prefix) {
			end := strings.Index(s[len(v.prefix):], v.suffix)
			if end < 0 {
				return rssToken{}, fmt.Errorf("unterminated XML section")
			}
			r.pos += len(v.prefix) + end + len(v.suffix)
			return rssToken{kind: v.kind, text: s[len(v.prefix) : len(v.prefix)+end]}, nil
		}
	}
	if strings.HasPrefix(s, "<!") {
		quote := byte(0)
		depth := 0
		for i := 2; i < len(s); i++ {
			c := s[i]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
				continue
			}
			if c == '[' {
				depth++
			}
			if c == ']' {
				depth--
			}
			if c == '>' && depth <= 0 {
				r.pos += i + 1
				return rssToken{kind: "skip"}, nil
			}
		}
		return rssToken{}, fmt.Errorf("unterminated XML declaration")
	}
	quote := byte(0)
	end := -1
	for i := 1; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '>' {
			end = i
			break
		}
	}
	if end < 0 {
		return rssToken{}, fmt.Errorf("unterminated XML tag")
	}
	r.pos += end + 1
	body := s[1:end]
	if strings.HasPrefix(body, "/") {
		name := strings.TrimRight(body[1:], " \t\r\n")
		if len(r.stack) == 0 || r.stack[len(r.stack)-1] != name {
			return rssToken{}, fmt.Errorf("mismatched XML end")
		}
		r.stack = r.stack[:len(r.stack)-1]
		return rssToken{kind: "end", name: name}, nil
	}
	empty := strings.HasSuffix(body, "/")
	if empty {
		body = body[:len(body)-1]
	}
	i := 0
	for i < len(body) && !rssSpace(body[i]) {
		i++
	}
	name := body[:i]
	if name == "" {
		return rssToken{}, fmt.Errorf("missing XML name")
	}
	attrs := body[i:]
	kind := "start"
	if empty {
		kind = "empty"
	} else {
		r.stack = append(r.stack, name)
	}
	return rssToken{kind: kind, name: name, attrs: attrs}, nil
}
func rssAttrValue(body, wanted string) (string, bool, error) {
	i := 0
	for i < len(body) {
		for i < len(body) && rssSpace(body[i]) {
			i++
		}
		if i == len(body) {
			break
		}
		start := i
		for i < len(body) && !rssSpace(body[i]) && body[i] != '=' {
			i++
		}
		key := body[start:i]
		for i < len(body) && rssSpace(body[i]) {
			i++
		}
		if i == len(body) || body[i] != '=' {
			return "", false, fmt.Errorf("missing XML attribute value")
		}
		i++
		for i < len(body) && rssSpace(body[i]) {
			i++
		}
		if i == len(body) || body[i] != '\'' && body[i] != '"' {
			return "", false, fmt.Errorf("unquoted XML attribute")
		}
		q := body[i]
		i++
		start = i
		for i < len(body) && body[i] != q {
			i++
		}
		if i == len(body) {
			return "", false, fmt.Errorf("unterminated XML attribute")
		}
		raw := body[start:i]
		i++
		if strings.EqualFold(key, wanted) {
			value, e := rssUnescape(raw, true)
			return strings.TrimSpace(value), true, e
		}
	}
	return "", false, nil
}

// ParseRustRSS is the installed0.2.6 stream parser's consumed RSS/Atom result.
// Dates are projected through the source local timezone to chain.rss wall text.
func ParseRustRSS(text string, maxItems int, loc *time.Location) ([]Row, error) {
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("invalid UTF-8 XML")
	}
	if loc == nil {
		loc = time.Local
	}
	if maxItems < 1 {
		maxItems = 1
	}
	out := []Row{}
	reader := rssReader{text: text}
	var item Row
	depth := 0
	field := rssField{}
	for {
		token, e := reader.next()
		if e != nil {
			return nil, e
		}
		local := rssLocal(token.name)
		switch token.kind {
		case "eof":
			return out, nil
		case "start":
			if item == nil && (local == "item" || local == "entry") {
				item = Row{"title": "", "description": "", "link": "", "enclosure": "", "size": int64(0), "pubdate": "", "nickname": ""}
				depth = 1
				field = rssField{}
				continue
			}
			if item == nil {
				continue
			}
			depth++
			if e := rssFillAttrs(item, local, token.attrs); e != nil {
				return nil, e
			}
			if local == "enclosure" {
				continue
			}
			if field.kind == "" {
				key := local
				switch local {
				case "summary":
					key = "description"
				case "published", "updated":
					key = "pubdate"
				case "creator":
					key = "nickname"
				case "pubdate":
					key = "pubdate"
				}
				if contains([]string{"title", "description", "link", "pubdate", "nickname"}, key) && textValue(item[key]) == "" {
					field = rssField{key, depth}
				}
			}
		case "empty":
			if item != nil {
				if e := rssFillAttrs(item, local, token.attrs); e != nil {
					return nil, e
				}
			}
		case "text", "cdata":
			if item != nil && field.kind != "" {
				v := token.text
				if token.kind == "text" {
					v, e = rssUnescape(v, false)
					if e != nil {
						return nil, e
					}
				}
				item[field.kind] = textValue(item[field.kind]) + v
			}
		case "end":
			if item != nil && depth == 1 && (local == "item" || local == "entry") {
				for _, k := range []string{"title", "description", "link", "enclosure", "pubdate", "nickname"} {
					item[k] = strings.TrimSpace(textValue(item[k]))
				}
				if item["title"] != "" && (item["enclosure"] != "" || item["link"] != "") {
					if item["enclosure"] == "" {
						item["enclosure"] = item["link"]
					}
					item["pubdate"] = rssDate(textValue(item["pubdate"]), loc)
					if item["nickname"] == "" {
						delete(item, "nickname")
					}
					out = append(out, item)
					if len(out) >= maxItems {
						return out, nil
					}
				}
				item = nil
				depth = 0
				field = rssField{}
				continue
			}
			if item != nil && depth > 0 {
				if field.depth == depth {
					field = rssField{}
				}
				depth--
			}
		}
	}
}
func textValue(v any) string { s, _ := v.(string); return s }
func rssFillAttrs(item Row, local string, attrs string) error {
	if local == "enclosure" && item["enclosure"] == "" {
		if v, ok, e := rssAttrValue(attrs, "url"); e != nil {
			return e
		} else if ok {
			item["enclosure"] = v
		}
		if v, ok, e := rssAttrValue(attrs, "length"); e != nil {
			return e
		} else if ok {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				n = 0
			}
			item["size"] = n
		}
	}
	if local == "link" && item["link"] == "" {
		if v, ok, e := rssAttrValue(attrs, "href"); e != nil {
			return e
		} else if ok {
			item["link"] = v
		}
	}
	return nil
}

func rssDate(value string, loc *time.Location) string {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC3339Nano} {
		if at, e := rssParseDate(layout, value, time.UTC); e == nil {
			return at.In(loc).Format("2006-01-02 15:04:05")
		}
	}
	for _, suffix := range []string{" UTC", " GMT"} {
		if strings.HasSuffix(value, suffix) {
			v := strings.TrimSpace(strings.TrimSuffix(value, suffix))
			for _, layout := range []string{"Mon, 02 Jan 2006 15:04:05", "02 Jan 2006 15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
				if at, e := rssParseDate(layout, v, time.UTC); e == nil {
					return at.In(loc).Format("2006-01-02 15:04:05")
				}
			}
		}
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006/01/02 15:04:05", "2006/01/02 15:04", "02 Jan 2006 15:04:05", "Mon, 02 Jan 2006 15:04:05", "2006-01-02", "2006/01/02", "02 Jan 2006"} {
		if at, e := rssParseDate(layout, value, loc); e == nil {
			return at.Format("2006-01-02 15:04:05")
		}
	}
	return ""
}

var rssLeapSecond = regexp.MustCompile(`(:[0-9]{2}):60([.,][0-9]+)?(Z|[+-][0-9:]+| UTC| GMT|)$`)

func rssParseDate(layout, value string, loc *time.Location) (time.Time, error) {
	// Chrono represents a leap second as second59 plus overflowing nanoseconds;
	// the original binding exports its whole Unix second and discards fractions.
	value = rssLeapSecond.ReplaceAllString(value, "${1}:59${2}${3}")
	layouts := []string{layout, strings.NewReplacer("01", "1", "02", "2", "04", "4", "05", "5").Replace(layout)}
	var last error
	for _, format := range layouts {
		at, e := time.ParseInLocation(format, value, loc)
		if e != nil {
			last = e
			continue
		}
		if len(value) > 4 && value[3] == ',' && !strings.EqualFold(value[:3], at.Weekday().String()[:3]) {
			last = fmt.Errorf("weekday mismatch")
			continue
		}
		return at, nil
	}
	return time.Time{}, last
}
