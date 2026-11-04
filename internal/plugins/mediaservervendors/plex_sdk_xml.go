package mediaservervendors

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/releasemeta/legacyanime"
)

// plexSDKXML is an owned element tree, never an HTTP or filesystem reference.
// The first decoder slice preserves original parse-first/clean-on-ParseError
// order. DTD/alternate encoding are explicit unfinished domains, not false
// success or approved exclusions from the full Plex SDK contract.
type plexSDKXML struct {
	tag        string
	attributes map[string]string
	children   []*plexSDKXML
	text       string
	tail       string
}

func plexSDKSourceError(class, message string) error {
	return &mediaserverruntime.SourceError{Class: class, Cause: errors.New(message)}
}
func plexSDKXMLName(n xml.Name) string {
	if n.Space != "" {
		return "{" + n.Space + "}" + n.Local
	}
	return n.Local
}
func plexSDKParseXML(text string) (*plexSDKXML, error) {
	if legacyanime.Strip(text) == "" {
		return nil, nil
	}
	root, err := plexSDKParseXMLOnce(text)
	if err == nil {
		return root, nil
	}
	// Do not turn an unimplemented domain, cancellation or unrelated failure
	// into the source ElementTree.ParseError cleanup path.
	source, ok := err.(*mediaserverruntime.SourceError)
	if !ok || source.Class != "ParseError" {
		return nil, err
	}
	cleaned := strings.Map(func(r rune) rune {
		if plexSDKIllegalXMLRune(r) {
			return -1
		}
		return r
	}, text)
	if legacyanime.Strip(cleaned) == "" {
		return nil, nil
	}
	return plexSDKParseXMLOnce(cleaned)
}
func plexSDKIllegalXMLRune(r rune) bool {
	if r >= 0 && r <= 8 || r >= 11 && r <= 12 || r >= 14 && r <= 31 || r >= 0x7f && r <= 0x84 || r >= 0x86 && r <= 0x9f || r >= 0xfdd0 && r <= 0xfddf {
		return true
	}
	// Complete source wide-build ranges: each plane's FFFE/FFFF, including 0.
	return r >= 0 && r <= 0x10ffff && (r&0xffff == 0xfffe || r&0xffff == 0xffff)
}
func plexSDKParseXMLOnce(text string) (*plexSDKXML, error) {
	if !utf8.ValidString(text) {
		return nil, &PlexArgumentBoundary{Field: "SDK XML non-Unicode input"}
	}
	decoder := xml.NewDecoder(strings.NewReader(text))
	decoder.Strict = true
	decoder.CharsetReader = func(charset string, _ io.Reader) (io.Reader, error) {
		return nil, &PlexArgumentBoundary{Field: "SDK XML non-UTF8 declaration"}
	}
	var root *plexSDKXML
	stack := []*plexSDKXML{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			var boundary *PlexArgumentBoundary
			if errors.As(err, &boundary) {
				return nil, boundary
			}
			return nil, plexSDKSourceError("ParseError", err.Error())
		}
		switch t := token.(type) {
		case xml.StartElement:
			if len(stack) == 0 && root != nil {
				return nil, plexSDKSourceError("ParseError", "multiple XML document elements")
			}
			node := &plexSDKXML{tag: plexSDKXMLName(t.Name), attributes: map[string]string{}, children: []*plexSDKXML{}}
			for _, a := range t.Attr {
				// Namespace declarations are not ElementTree attrib members.
				if a.Name.Space == "xmlns" || a.Name.Space == "" && a.Name.Local == "xmlns" {
					continue
				}
				key := plexSDKXMLName(a.Name)
				if _, exists := node.attributes[key]; exists {
					return nil, plexSDKSourceError("ParseError", "duplicate XML attribute")
				}
				node.attributes[key] = a.Value
			}
			if len(stack) == 0 {
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, plexSDKSourceError("ParseError", "unexpected XML end")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				if len(parent.children) == 0 {
					parent.text += string(t)
				} else {
					parent.children[len(parent.children)-1].tail += string(t)
				}
			} else if legacyanime.Strip(string(t)) != "" {
				return nil, plexSDKSourceError("ParseError", "text outside XML document element")
			}
		case xml.Directive:
			return nil, &PlexArgumentBoundary{Field: "SDK XML DTD/directive"}
		case xml.Comment, xml.ProcInst:
			// Standard ElementTree does not retain comments/PI by default.
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, plexSDKSourceError("ParseError", "missing or unfinished XML document element")
	}
	return root, nil
}
