package larkmessager

import (
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
	m := map[string]string{"资源下载": "blue", "整理入库": "turquoise", "订阅": "green", "站点": "yellow", "媒体服务器": "purple", "手动处理": "red", "插件": "blue", "其它": "blue"}
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
