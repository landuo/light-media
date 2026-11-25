package htmlextract

import (
	"errors"
	"golang.org/x/net/html"
	"strings"
	"unicode/utf8"
)

var ErrUnicodeEncode = errors.New("UnicodeEncodeError")

type Anchor struct{ Href, Class string }
type Links struct {
	Document bool
	Forms    []string
	Anchors  []Anchor
}

func Extract(text string) (Links, error) {
	if !utf8.ValidString(text) {
		return Links{}, ErrUnicodeEncode
	}
	// The four required XPath attribute predicates are evaluated in document order.
	// Tokenization uses the pinned Go HTML5 tokenizer, with source-specific
	// libxml tree construction and script escaped-state recovery below.
	z := html.NewTokenizer(strings.NewReader(text))
	out := Links{}
	state := treeState{}
	offset := 0
	for {
		kind := z.Next()
		offset += len(z.Raw())
		if kind == html.ErrorToken {
			return out, nil
		}
		if kind == html.TextToken {
			if strings.Trim(string(z.Text()), "\t\n\f\r ") != "" {
				out.Document = true
				state.text()
			}
			continue
		}
		if kind == html.EndTagToken {
			state.end(z.Token().Data)
			continue
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		token := z.Token()
		if kind == html.SelfClosingTagToken || token.Data == "noscript" {
			z.NextIsNotRawText()
		}
		accepted := state.start(token.Data)
		out.Document = true
		if state.stopped {
			return out, nil
		}
		if kind == html.SelfClosingTagToken || emptyTags[token.Data] {
			if len(state.stack) > 0 {
				state.stack = state.stack[:len(state.stack)-1]
			}
		}
		if !accepted {
			continue
		}
		if token.Data == "script" && kind != html.SelfClosingTagToken {
			end := scriptEnd(text[offset:])
			offset += end
			z = html.NewTokenizer(strings.NewReader(text[offset:]))
		}
		attrs := map[string]string{}
		for _, a := range token.Attr {
			if _, exists := attrs[a.Key]; !exists {
				attrs[a.Key] = a.Val
			}
		}
		if token.Data == "form" && strings.Contains(attrs["action"], "download.php?id=") {
			out.Forms = append(out.Forms, attrs["action"])
		}
		if token.Data == "a" {
			out.Anchors = append(out.Anchors, Anchor{attrs["href"], attrs["class"]})
		}
	}
}
