package larkmessager

import (
	"light-media/internal/releasemeta/legacyanime"
	"regexp"
	"strings"
	"unicode/utf8"
)

func EscapeCardText(s string) string {
	r := strings.NewReplacer("\\", "&#92;", "<", "&#60;", ">", "&#62;")
	return r.Replace(s)
}
func BuildMessageText(title, text, link string) string {
	var p []string
	if title != "" {
		p = append(p, "**"+strip(EscapeCardText(title))+"**")
	}
	if text != "" {
		if t := strip(EscapeCardText(text)); t != "" {
			p = append(p, t)
		}
	}
	if link != "" {
		p = append(p, "[查看详情]("+strip(link)+")")
	}
	return strings.Join(p, "\n\n")
}
func MarkdownSection(text, size, margin string) Object {
	content := strip(EscapeCardText(text))
	if content == "" {
		return nil
	}
	s := Object{"tag": "markdown", "text_size": size, "content": content}
	if margin != "" {
		s["margin"] = margin
	}
	return s
}
func prefixRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
func cardActions(buttons [][]Button, margin string) []any {
	out := []any{}
	for ri, row := range buttons {
		if ri >= 8 {
			break
		}
		cols := []any{}
		for bi, b := range row {
			if bi >= 3 {
				break
			}
			if !truth(b.Text) {
				continue
			}
			beh := []any{}
			if truth(b.CallbackData) {
				beh = append(beh, Object{"type": "callback", "value": Object{"callback_data": str(b.CallbackData)}})
			}
			if truth(b.URL) {
				beh = append(beh, Object{"type": "open_url", "default_url": str(b.URL), "pc_url": str(b.URL), "android_url": str(b.URL), "ios_url": str(b.URL)})
			}
			if len(beh) == 0 {
				beh = append(beh, Object{"type": "callback", "value": Object{"callback_data": str(b.Text)}})
			}
			cols = append(cols, Object{"tag": "column", "width": "weighted", "weight": 1, "elements": []any{Object{"tag": "button", "text": Object{"tag": "plain_text", "content": prefixSourceText(b.Text, 20)}, "type": "default", "behaviors": beh}}})
		}
		if len(cols) > 0 {
			o := Object{"tag": "column_set", "flex_mode": "none", "columns": cols}
			if margin != "" {
				o["margin"] = margin
			}
			out = append(out, o)
		}
	}
	return out
}
func HeaderTemplate(mtype string) string {
	m := map[string]string{"资源下载": "blue", "整理入库": "turquoise", "订阅": "green", "站点": "yellow", "媒体服务器": "purple", "手动处理": "red", "插件": "blue", "智能体": "indigo", "其它": "blue"}
	if v := m[mtype]; v != "" {
		return v
	}
	return "blue"
}
func buildCard(in CardInput) Object {
	elements := []any{}
	hasImage := in.ImageKey != ""
	if len(in.ImageItems) > 0 {
		for _, item := range in.ImageItems {
			if item.ImageKey != "" {
				alt := item.Title
				if alt == "" {
					alt = "图片"
				}
				elements = append(elements, Object{"tag": "column_set", "flex_mode": "none", "columns": []any{Object{"tag": "column", "width": "weighted", "weight": 1, "elements": []any{Object{"tag": "img", "img_key": item.ImageKey, "alt": Object{"tag": "plain_text", "content": alt}, "mode": "fit_horizontal"}}}, Object{"tag": "column", "width": "weighted", "weight": 2, "elements": []any{MarkdownSection(item.Title, "normal", "")}}}})
				hasImage = true
			} else if s := MarkdownSection(item.Title, "normal", ""); s != nil {
				elements = append(elements, s)
			}
			elements = append(elements, Object{"tag": "hr"})
		}
		if len(elements) > 0 {
			elements = elements[:len(elements)-1]
		}
	} else if in.ImageKey != "" {
		alt := in.Title
		if alt == "" {
			alt = "图片"
		}
		elements = append(elements, Object{"tag": "img", "img_key": in.ImageKey, "alt": Object{"tag": "plain_text", "content": alt}, "mode": "fit_horizontal"})
	}
	bodyMargin, actionMargin, padding := "", "", "12px 12px 12px 12px"
	if hasImage {
		bodyMargin = "4px 12px 12px 12px"
		actionMargin = "0px 12px 12px 12px"
		padding = "0px 0px 0px 0px"
	}
	bodyRaw := BuildMessageText("", in.Text, in.Link)
	var body Object
	if bodyRaw != "" {
		body = MarkdownSection(bodyRaw, "normal", bodyMargin)
	}
	if body != nil {
		if hasImage {
			elements = append(elements, Object{"tag": "hr"}, Object{"tag": "column_set", "flex_mode": "none", "background_style": "grey", "columns": []any{Object{"tag": "column", "width": "weighted", "weight": 1, "elements": []any{body}}}})
		} else {
			elements = append(elements, body)
		}
	}
	actions := cardActions(in.Buttons, actionMargin)
	if body != nil && len(actions) > 0 {
		elements = append(elements, Object{"tag": "hr"})
	}
	elements = append(elements, actions...)
	summary := in.Title
	if summary == "" {
		summary = "MoviePilot"
	}
	card := Object{"schema": "2.0", "config": Object{"wide_screen_mode": true, "enable_forward": true, "update_multi": true, "summary": Object{"content": summary}}, "body": Object{"direction": "vertical", "padding": padding, "elements": elements}}
	if in.Title != "" {
		template := in.HeaderTemplate
		if template == "" {
			template = "blue"
		}
		card["header"] = Object{"template": template, "title": Object{"tag": "plain_text", "content": in.Title}}
	}
	return card
}

const StreamTitleID = "mp_stream_title"
const StreamBodyID = "mp_stream_body"

var markdownImage = regexp.MustCompile(`!\[([^\]\n]*)\]\(([^)\n]*)\)`)

func StripTrailingIncompleteImage(s string) string {
	start := strings.LastIndex(s, "![")
	if start < 0 {
		return s
	}
	fragment := s[start:]
	if strings.ContainsAny(fragment, "\n\r") || markdownImage.FindString(fragment) == fragment {
		return s
	}
	if !strings.Contains(fragment, ")") {
		return strings.TrimRightFunc(s[:start], func(r rune) bool { return strip(string(r)) == "" })
	}
	return s
}
func StripStreamingImages(s string) string {
	s = StripTrailingIncompleteImage(s)
	return markdownImage.ReplaceAllStringFunc(s, func(m string) string { return strip(markdownImage.FindStringSubmatch(m)[1]) })
}
func IsExternalImageURL(s string) bool {
	s = legacyanime.LowerCase(strip(s))
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "feishu://image/")
}
func ExtractImageURLs(s string) []string {
	o := []string{}
	for _, m := range markdownImage.FindAllStringSubmatch(s, -1) {
		if v := strip(m[2]); v != "" && IsExternalImageURL(v) {
			o = append(o, v)
		}
	}
	return o
}
func DedupeImageURLs(v []string) []string {
	o := []string{}
	seen := map[string]bool{}
	for _, s := range v {
		s = strip(s)
		if s != "" && !seen[s] {
			seen[s] = true
			o = append(o, s)
		}
	}
	return o
}
func BuildStreamingCard(title, text string) Object {
	elements := []any{}
	if t := strip(EscapeCardText(title)); title != "" && t != "" {
		elements = append(elements, Object{"tag": "markdown", "element_id": StreamTitleID, "content": "**" + t + "**"})
	}
	body := strip(EscapeCardText(StripStreamingImages(text)))
	if body == "" {
		body = " "
	}
	elements = append(elements, Object{"tag": "markdown", "element_id": StreamBodyID, "content": body})
	summary := title
	if summary == "" {
		summary = "MoviePilot助手"
	}
	return Object{"schema": "2.0", "config": Object{"wide_screen_mode": true, "enable_forward": true, "update_multi": true, "streaming_mode": true, "summary": Object{"content": summary}, "streaming_config": Object{"print_frequency_ms": Object{"default": 70}, "print_step": Object{"default": 1}, "print_strategy": "fast"}}, "body": Object{"direction": "vertical", "padding": "12px 12px 12px 12px", "elements": elements}}
}
func extractCallback(value any, name string) *string {
	var v any
	if d := obj(value); d != nil {
		v = first(d["callback_data"], d["data"], d["value"])
	} else if s, ok := value.(string); ok {
		v = s
	}
	if !truth(v) {
		v = name
	}
	if !truth(v) {
		return nil
	}
	s := strip(str(v))
	return &s
}
func SafeCardKit(data []byte) (Object, error) {
	cleaned := strings.Map(func(r rune) rune {
		if r >= ' ' || r == '\t' || r == '\n' || r == '\r' {
			return r
		}
		return -1
	}, string(data))
	if pythonNonFiniteJSON(cleaned) {
		return nil, ErrUnsupported
	}
	var o Object
	if decode([]byte(cleaned), &o) != nil {
		return Object{}, nil
	}
	if o == nil {
		return Object{}, nil
	}
	return o, nil
}

// CPython accepts bare non-finite constants. Their full JSON representation is
// not ported; never mistake them for invalid/empty data and accept a 2xx write.
func pythonNonFiniteJSON(s string) bool {
	quoted, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			continue
		}
		if strings.ContainsRune(" \t\r\n{}[],:", rune(c)) {
			continue
		}
		start := i
		for i+1 < len(s) && !strings.ContainsRune(" \t\r\n{}[],:\"", rune(s[i+1])) {
			i++
		}
		token := s[start : i+1]
		if token == "NaN" || token == "Infinity" || token == "-Infinity" {
			return true
		}
	}
	return false
}
func CardKitOK(status int, data Object) bool {
	if v, ok := data["code"]; ok {
		return zero(v)
	}
	return status >= 200 && status < 300
}

func BuildCard(in CardInput) (_out Object, _error error) {
	defer sourceErrors(&_error)
	return buildCard(in), nil
}
func CardActions(buttons [][]Button, margin string) (_out []any, _error error) {
	defer sourceErrors(&_error)
	return cardActions(buttons, margin), nil
}
func ExtractCallback(value any, name string) (_out *string, _error error) {
	defer sourceErrors(&_error)
	return extractCallback(value, name), nil
}

func prefixSourceText(v any, n int) any {
	switch v := v.(type) {
	case string:
		return prefixRunes(v, n)
	case []any:
		if len(v) > n {
			return v[:n]
		}
		return v
	default:
		panic(sourceFailure{ErrUnsupported})
	}
}
