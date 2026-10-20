package app

import (
	gonja "light-media/internal/jinja"
	"light-media/internal/jinja/exec"
	"light-media/internal/jinja/tokens"
	"light-media/internal/notice"
	"light-media/internal/noticetemplate"
)

// The business adapter has source attribute projections, not live Python
// instances with their class descriptors, methods and repr. Standard template
// variables are supported. A template naming a raw object remains an explicit
// dependency until its native class implementation is available. Tokenization
// avoids treating a literal string, comment or raw block as an object access.
func noticeV2RequireSourceObjectImplementation(message *notice.Envelope, templates any) error {
	if noticeTruth(message.Get("title")) || noticeTruth(message.Get("text")) || !noticeTruth(message.Get("ctype")) {
		return nil
	}
	key, e := message.TemplateEnumValue("ctype")
	if e != nil {
		return nil
	} // helper owns caught source errors
	var content any
	switch d := templates.(type) {
	case map[string]any:
		content = d[key]
	case *exec.Dict:
		for _, p := range d.Pairs {
			if p.Key.IsString() && p.Key.String() == key {
				content = p.Value.Interface()
				break
			}
		}
	}
	text, e := noticetemplate.ParseTemplateContent(content, "literal")
	if e != nil || text == nil {
		return nil
	}
	stream := tokens.LexAll(*text, gonja.DefaultConfig)
	for !stream.End() {
		token := stream.Current()
		if token.Type == tokens.Name {
			switch token.Val {
			case "__meta__", "__mediainfo__", "__torrentinfo__", "__transferinfo__", "__episodes_info__":
				return notice.ErrTemplateDependency
			}
		}
		stream.Next()
	}
	return nil
}
