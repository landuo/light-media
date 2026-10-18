package app

// The fallback consumes only XML element/attribute names and element .text.
// In particular, unresolved entities remain child nodes when resolve_entities
// is false: their tails must never be joined into the parent's .text.
import (
	"encoding/xml"
	"strconv"
	"strings"
	"unicode/utf8"
)

func brushRSSSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

func brushRSSQName(name string, namespaces map[string]string, attr bool) xml.Name {
	if before, after, ok := strings.Cut(name, ":"); ok {
		space := namespaces[before]
		if space == "" {
			// An undeclared prefix is retained by libxml recovery. It cannot
			// match an unprefixed XPath name, but local-name() still works.
			space = before
		}
		return xml.Name{Space: space, Local: after}
	}
	space := ""
	if !attr {
		space = namespaces[""]
	}
	return xml.Name{Space: space, Local: name}
}

func brushRSSRefText(raw string, attr bool) (text string, entity, invalid bool) {
	var out strings.Builder
	for len(raw) > 0 {
		before, after, found := strings.Cut(raw, "&")
		out.WriteString(before)
		if !found {
			break
		}
		end := strings.IndexByte(after, ';')
		if end < 0 || strings.ContainsAny(after[:end], " \t\r\n&") {
			// xmlParseEntityRef consumes an invalid reference name. Recovery
			// continues with the following text, without inventing '&'.
			i := 0
			for i < len(after) && !brushRSSSpace(after[i]) && after[i] != '&' {
				i++
			}
			raw = after[i:]
			continue
		}
		ref := after[:end]
		raw = after[end+1:]
		if strings.HasPrefix(ref, "#") {
			digits, base := ref[1:], 10
			if strings.HasPrefix(digits, "x") {
				digits, base = digits[1:], 16
			}
			v, err := strconv.ParseUint(digits, base, 32)
			if err != nil || v == 0 || v > utf8.MaxRune {
				continue
			}
			if v >= 0xd800 && v <= 0xdfff {
				// libxml retains these bytes, and lxml raises UnicodeDecodeError
				// when the consumed .text/.get value is converted to Python str.
				invalid = true
				continue
			}
			out.WriteRune(rune(v))
			continue
		}
		switch ref {
		case "amp":
			out.WriteByte('&')
		case "lt":
			out.WriteByte('<')
		case "gt":
			out.WriteByte('>')
		case "apos":
			out.WriteByte('\'')
		case "quot":
			out.WriteByte('"')
		default:
			if !attr {
				return out.String(), true, invalid
			}
		}
	}
	return out.String(), false, invalid
}

func brushRSSXMLTree(doc string) *brushRSSXMLNode {
	root := &brushRSSXMLNode{}
	stack := []*brushRSSXMLNode{root}
	names := []string{""}
	namespaces := []map[string]string{{"xml": "http://www.w3.org/XML/1998/namespace"}}
	ignoredClose := map[string]int{}
	for pos := 0; pos < len(doc); {
		if doc[pos] != '<' {
			end := strings.IndexByte(doc[pos:], '<')
			if end < 0 {
				end = len(doc) - pos
			}
			node := stack[len(stack)-1]
			if !node.hasChild {
				text, entity, invalid := brushRSSRefText(doc[pos:pos+end], false)
				node.text += strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
				node.textInvalid = node.textInvalid || invalid
				node.hasChild = entity
			}
			pos += end
			continue
		}
		section := false
		for _, s := range []struct {
			start, end string
			cdata      bool
		}{{"<!--", "-->", false}, {"<?", "?>", false}, {"<![CDATA[", "]]>", true}} {
			if strings.HasPrefix(doc[pos:], s.start) {
				at := strings.Index(doc[pos+len(s.start):], s.end)
				if at < 0 {
					return root
				}
				node := stack[len(stack)-1]
				if s.cdata && !node.hasChild {
					node.text += doc[pos+len(s.start) : pos+len(s.start)+at]
				} else if !s.cdata {
					node.hasChild = true
				}
				pos += len(s.start) + at + len(s.end)
				section = true
				break
			}
		}
		if section {
			continue
		}
		quote, bracket, end := byte(0), 0, -1
		for i := pos + 1; i < len(doc); i++ {
			c := doc[i]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
			} else if c == '[' {
				bracket++
			} else if c == ']' {
				bracket--
			} else if c == '>' && bracket <= 0 {
				end = i
				break
			}
		}
		if end < 0 {
			return root
		}
		body := doc[pos+1 : end]
		pos = end + 1
		if strings.HasPrefix(body, "!") {
			continue
		}
		if strings.HasPrefix(body, "/") {
			name := strings.TrimSpace(body[1:])
			if ignoredClose[name] > 0 {
				ignoredClose[name]--
				continue
			}
			if len(stack) > 1 {
				// libxml recovery closes the current element even when its
				// name differs; searching ancestors produces a different tree.
				stack, names, namespaces = stack[:len(stack)-1], names[:len(names)-1], namespaces[:len(namespaces)-1]
			}
			continue
		}
		empty := strings.HasSuffix(body, "/")
		if empty {
			body = body[:len(body)-1]
		}
		i := 0
		for i < len(body) && !brushRSSSpace(body[i]) {
			i++
		}
		name := body[:i]
		if name == "" {
			return root
		}
		if len(stack) == 1 && len(root.children) > 0 {
			// XML has one document element; recovery ignores trailing roots.
			return root
		}
		ns := map[string]string{}
		for key, value := range namespaces[len(namespaces)-1] {
			ns[key] = value
		}
		type pair struct {
			name, value string
			invalid     bool
		}
		attrs := []pair{}
		malformed := false
		for i < len(body) {
			for i < len(body) && brushRSSSpace(body[i]) {
				i++
			}
			if i == len(body) {
				break
			}
			start := i
			for i < len(body) && !brushRSSSpace(body[i]) && body[i] != '=' {
				i++
			}
			key := body[start:i]
			for i < len(body) && brushRSSSpace(body[i]) {
				i++
			}
			if i == len(body) || body[i] != '=' {
				malformed = true
				break
			}
			i++
			for i < len(body) && brushRSSSpace(body[i]) {
				i++
			}
			if i == len(body) || body[i] != '\'' && body[i] != '"' {
				malformed = true
				break
			}
			q := body[i]
			i++
			start = i
			for i < len(body) && body[i] != q {
				i++
			}
			if i == len(body) {
				malformed = true
				break
			}
			text, _, invalid := brushRSSRefText(body[start:i], true)
			i++
			text = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(text)
			if key == "xmlns" {
				ns[""] = text
			} else if strings.HasPrefix(key, "xmlns:") {
				ns[key[len("xmlns:"):]] = text
			} else {
				attrs = append(attrs, pair{key, text, invalid})
			}
		}
		node := &brushRSSXMLNode{name: brushRSSQName(name, ns, false)}
		seen := map[xml.Name]bool{}
		for _, attr := range attrs {
			key := brushRSSQName(attr.name, ns, true)
			if !seen[key] {
				node.attrs = append(node.attrs, xml.Attr{Name: key, Value: attr.value})
				node.attrInvalid = node.attrInvalid || attr.invalid
				seen[key] = true
			}
		}
		parent := stack[len(stack)-1]
		parent.hasChild = true
		parent.children = append(parent.children, node)
		if malformed {
			ignoredClose[name]++
		} else if !empty {
			stack = append(stack, node)
			names = append(names, name)
			namespaces = append(namespaces, ns)
		}
	}
	return root
}
