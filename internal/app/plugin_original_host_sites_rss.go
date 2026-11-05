package app

// The ordinary RSS helper's bounded recovery path. This is private to RSS
// extraction, not a general XML/DOM compatibility layer or plugin extension.
import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	dateparser "github.com/markusmobius/go-dateparser"
	"light-media/internal/plugins/brushflow"
	pyre "light-media/internal/plugins/iyuuautoseed/compat/pythonre"
	"light-media/internal/releasemeta/legacyanime"
)

type originalRSSNode struct {
	Name       xml.Name
	Text       string
	Attributes map[string]string
	Children   []*originalRSSNode
	ChildSeen  bool
}

var originalRSSInternalEntity = regexp.MustCompile(`<!ENTITY\s+([A-Za-z_:][A-Za-z0-9_.:-]*)\s+(?:"([^"]*)"|'([^']*)')\s*>`)

// The source's recover=True drops malformed/undeclared entity references,
// leaves predefined/numeric references for normal XML decoding, and retains
// declared unresolved entities as child nodes (their tail is not .text).
func originalRSSRecoverInput(text string) string {
	internal := map[string]string{}
	for _, match := range originalRSSInternalEntity.FindAllStringSubmatch(text, -1) {
		value := match[2]
		if value == "" {
			value = match[3]
		}
		internal[match[1]] = value
	}
	var expand func(string, int, map[string]bool) string
	expand = func(value string, depth int, seen map[string]bool) string {
		if depth > 32 {
			return ""
		}
		var result strings.Builder
		for len(value) > 0 {
			at := strings.IndexByte(value, '&')
			if at < 0 {
				result.WriteString(value)
				break
			}
			result.WriteString(value[:at])
			value = value[at+1:]
			end := strings.IndexByte(value, ';')
			if end < 0 {
				break
			}
			name := value[:end]
			value = value[end+1:]
			switch name {
			case "amp":
				result.WriteByte('&')
			case "lt":
				result.WriteByte('<')
			case "gt":
				result.WriteByte('>')
			case "apos":
				result.WriteByte('\'')
			case "quot":
				result.WriteByte('"')
			default:
				if strings.HasPrefix(name, "#") {
					number := name[1:]
					base := 10
					if strings.HasPrefix(number, "x") {
						number = number[1:]
						base = 16
					}
					n, err := strconv.ParseUint(number, base, 32)
					if err == nil && n > 0 && n <= utf8.MaxRune {
						result.WriteRune(rune(n))
					}
				} else if nested, ok := internal[name]; ok && !seen[name] {
					seen[name] = true
					result.WriteString(expand(nested, depth+1, seen))
					delete(seen, name)
				}
			}
			if result.Len() > 50<<20 {
				return ""
			}
		}
		return result.String()
	}
	marker := "_lm_rss_entity_"
	for strings.Contains(text, marker) {
		marker += "x"
	}
	var out strings.Builder
	inTag := false
	quote := byte(0)
	for at := 0; at < len(text); {
		if strings.HasPrefix(text[at:], "<!--") || strings.HasPrefix(text[at:], "<![CDATA[") {
			ending := "-->"
			prefix := 4
			if strings.HasPrefix(text[at:], "<![CDATA[") {
				ending = "]]>"
				prefix = 9
			}
			end := strings.Index(text[at+prefix:], ending)
			if end < 0 {
				out.WriteString(text[at:])
				break
			}
			end += at + prefix + len(ending)
			out.WriteString(text[at:end])
			at = end
			continue
		}
		if strings.HasPrefix(text[at:], "<!DOCTYPE") {
			depth := 0
			delimiter := byte(0)
			end := at + 9
			for end < len(text) {
				c := text[end]
				if delimiter != 0 {
					if c == delimiter {
						delimiter = 0
					}
				} else {
					switch c {
					case '\'', '"':
						delimiter = c
					case '[':
						depth++
					case ']':
						if depth > 0 {
							depth--
						}
					case '>':
						if depth == 0 {
							end++
							goto directiveDone
						}
					}
				}
				end++
			}
		directiveDone:
			at = end
			continue
		}
		c := text[at]
		if c == '<' && quote == 0 {
			inTag = true
		}
		if c == '&' {
			end := at + 1
			for end < len(text) {
				r, n := utf8.DecodeRuneInString(text[end:])
				if !(unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || strings.ContainsRune("_:.-#", r)) {
					break
				}
				end += n
			}
			name := text[at+1 : end]
			terminated := end < len(text) && text[end] == ';'
			if terminated {
				end++
			}
			valid := terminated && (name == "amp" || name == "lt" || name == "gt" || name == "apos" || name == "quot")
			if terminated && strings.HasPrefix(name, "#") {
				number := strings.TrimPrefix(name, "#")
				base := 10
				if strings.HasPrefix(number, "x") {
					base = 16
					number = number[1:]
				}
				r, err := strconv.ParseUint(number, base, 32)
				valid = err == nil && r > 0 && r <= utf8.MaxRune && (r < 0xd800 || r > 0xdfff)
			}
			if valid {
				out.WriteString(text[at:end])
			} else if terminated && inTag && quote != 0 && internal[name] != "" {
				_ = xml.EscapeText(&out, []byte(expand(internal[name], 0, map[string]bool{name: true})))
			} else if terminated && !inTag && name != "" && !strings.HasPrefix(name, "#") {
				out.WriteByte('<')
				out.WriteString(marker)
				out.WriteString("/>")
			}
			// A bare ampersand has no XML name to consume.
			if end == at+1 {
				at++
			} else {
				at = end
			}
			continue
		}
		out.WriteByte(c)
		if inTag {
			if quote != 0 {
				if c == quote {
					quote = 0
				}
			} else if c == '\'' || c == '"' {
				quote = c
			} else if c == '>' {
				inTag = false
			}
		}
		at++
	}
	return out.String()
}

func originalRSSRecoveryTree(text string) (*originalRSSNode, error) {
	if strings.TrimSpace(text) == "" || !strings.HasPrefix(strings.TrimSpace(text), "<") {
		return nil, errors.New("RSS response is not XML")
	}
	decoder := xml.NewDecoder(strings.NewReader(originalRSSRecoverInput(text)))
	decoder.Strict = false
	var root *originalRSSNode
	stack := []*originalRSSNode{}
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if root == nil {
				return nil, err
			}
			break
		}
		switch value := token.(type) {
		case xml.StartElement:
			if len(stack) >= 256 {
				return root, nil
			} // Same non-huge XML depth boundary.
			node := &originalRSSNode{Name: value.Name, Attributes: map[string]string{}}
			for _, attribute := range value.Attr {
				if attribute.Name.Space == "" {
					node.Attributes[attribute.Name.Local] = attribute.Value
				}
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.ChildSeen = true
				parent.Children = append(parent.Children, node)
			} else if root == nil {
				root = node
			} else {
				return root, nil
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					return root, nil
				}
			}
		case xml.CharData:
			if len(stack) > 0 {
				node := stack[len(stack)-1]
				if !node.ChildSeen {
					node.Text += string(value)
				}
			}
		case xml.Comment, xml.ProcInst:
			if len(stack) > 0 {
				stack[len(stack)-1].ChildSeen = true
			}
		}
	}
	if root == nil {
		return nil, errors.New("RSS XML has no root")
	}
	return root, nil
}

func originalRSSDescendants(node *originalRSSNode, match func(*originalRSSNode) bool) []*originalRSSNode {
	var found []*originalRSSNode
	var walk func(*originalRSSNode)
	walk = func(current *originalRSSNode) {
		for _, child := range current.Children {
			if match(child) {
				found = append(found, child)
			}
			walk(child)
		}
	}
	walk(node)
	return found
}

func originalRSSRecoverRows(text string, now time.Time) ([]brushflow.Row, error) {
	root, err := originalRSSRecoveryTree(text)
	if err != nil {
		return nil, err
	}
	items := originalRSSDescendants(root, func(n *originalRSSNode) bool {
		return n.Name.Space == "" && (n.Name.Local == "item" || n.Name.Local == "entry")
	})
	if len(items) > 1000 {
		items = items[:1000]
	}
	out := []brushflow.Row{}
	dateParser := dateparser.Parser{ParserTypes: []dateparser.ParserType{dateparser.CustomFormat, dateparser.AbsoluteTime, dateparser.NoSpacesTime}}
	for _, item := range items {
		first := func(names ...string) *originalRSSNode {
			for _, n := range originalRSSDescendants(item, func(n *originalRSSNode) bool {
				if n.Name.Space != "" {
					return false
				}
				for _, name := range names {
					if n.Name.Local == name {
						return true
					}
				}
				return false
			}) {
				return n
			}
			return nil
		}
		title := first("title")
		if title == nil || title.Text == "" {
			continue
		}
		description := ""
		if n := first("description", "summary"); n != nil {
			description = n.Text
		}
		link := ""
		if n := first("link"); n != nil {
			link = n.Text
			if link == "" {
				link = n.Attributes["href"]
			}
		}
		enclosure := ""
		size := json.Number("0")
		if n := first("enclosure"); n != nil {
			enclosure = n.Attributes["url"]
			raw := n.Attributes["length"]
			if legacyanime.IsDigit(raw) {
				var normalized strings.Builder
				valid := true
				for _, r := range raw {
					value, ok := pyre.DecimalValue(r)
					if !ok {
						valid = false
						break
					}
					normalized.WriteByte(byte('0' + value))
				}
				if !valid {
					continue
				}
				if normalized.Len() > 4300 {
					continue
				}
				integer, ok := new(big.Int).SetString(normalized.String(), 10)
				if !ok {
					continue
				}
				size = json.Number(integer.String())
			}
		}
		if enclosure == "" {
			enclosure = link
		}
		if enclosure == "" {
			continue
		}
		var dateNode *originalRSSNode
		for _, n := range item.Children {
			if n.Name.Space == "" && (n.Name.Local == "pubDate" || n.Name.Local == "published" || n.Name.Local == "updated") {
				dateNode = n
				break
			}
		}
		if dateNode == nil {
			nodes := originalRSSDescendants(item, func(n *originalRSSNode) bool {
				return n.Name.Local == "pubDate" || n.Name.Local == "published" || n.Name.Local == "updated"
			})
			if len(nodes) > 0 {
				dateNode = nodes[0]
			}
		}
		var published any = ""
		if dateNode != nil && dateNode.Text != "" {
			parsed, e := dateParser.Parse(&dateparser.Configuration{CurrentTime: now, DefaultTimezone: shanghai(), Languages: []string{"en"}, DateOrder: dateparser.MDY}, dateNode.Text)
			if e == nil {
				published = parsed.Time.In(shanghai()).Format("2006-01-02 15:04:05")
			} else {
				published = nil
			}
		}
		row := brushflow.Row{"title": title.Text, "enclosure": enclosure, "size": size, "description": description, "link": link, "pubdate": published}
		creator := originalRSSDescendants(item, func(n *originalRSSNode) bool { return n.Name.Local == "creator" })
		if len(creator) > 0 && creator[0].Text != "" {
			row["nickname"] = creator[0].Text
		}
		out = append(out, row)
	}
	return out, nil
}
