package larkmessager

import (
	"context"
)

// UnmarshalJSON consumes a trusted snapshot of the source Notification's
// properties actually read by this module. Unused fields remain host-owned.
func (n *Notification) UnmarshalJSON(raw []byte) (_error error) {
	defer sourceErrors(&_error)
	o, e := DecodeSourceObject(raw)
	if e != nil {
		return e
	}
	*n = Notification{}
	fields := []struct {
		key string
		dst *string
	}{{"title", &n.Title}, {"text", &n.Text}, {"image", &n.Image}, {"link", &n.Link}, {"file_path", &n.FilePath}, {"file_name", &n.FileName}, {"voice_path", &n.VoicePath}, {"voice_caption", &n.VoiceCaption}}
	for _, f := range fields {
		if o[f.key] == nil {
			continue
		}
		v, ok := o[f.key].(string)
		if !ok {
			return errInvalidMessage
		}
		*f.dst = v
	}
	if o["mtype"] != nil {
		v, ok := o["mtype"].(string)
		if !ok || !includes([]string{"资源下载", "整理入库", "订阅", "站点", "媒体服务器", "手动处理", "插件", "其它"}, v) {
			return errInvalidMessage
		}
		n.MType = v
	}
	if o["targets"] != nil {
		n.Targets = obj(o["targets"])
		if n.Targets == nil {
			return errInvalidMessage
		}
	}
	if n.UserID, e = notificationID(o["userid"]); e != nil {
		return e
	}
	if n.OriginalMessageID, e = notificationID(o["original_message_id"]); e != nil {
		return e
	}
	if o["buttons"] != nil {
		rows, ok := o["buttons"].([]any)
		if !ok {
			return errInvalidMessage
		}
		n.Buttons = [][]Button{}
		for _, row := range rows {
			buttons, ok := row.([]any)
			if !ok {
				return errInvalidMessage
			}
			out := []Button{}
			for _, v := range buttons {
				b := obj(v)
				if b == nil {
					return errInvalidMessage
				}
				out = append(out, Button{Text: b["text"], URL: b["url"], CallbackData: b["callback_data"]})
			}
			n.Buttons = append(n.Buttons, out)
		}
	}
	return nil
}
func notificationID(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	number, e := pydanticInteger(v)
	if e != nil {
		return "", errInvalidMessage
	}
	if number == "0" {
		return "", nil
	}
	return string(number), nil
}
func (p *Plugin) ParseMessageBody(ctx context.Context, source string, body any) (_out Object, _error error) {
	defer sourceErrors(&_error)
	if source != "Lark" {
		return nil, nil
	}
	var o Object
	switch body := body.(type) {
	case string:
		var e error
		o, e = DecodeSourceObject([]byte(body))
		if e != nil {
			return nil, nil
		}
	case []byte:
		var e error
		o, e = DecodeSourceObject(body)
		if e != nil {
			return nil, nil
		}
	default:
		o = obj(body)
		if o == nil {
			return nil, nil
		}
	}
	return p.ParseMessage(ctx, source, o)
}
