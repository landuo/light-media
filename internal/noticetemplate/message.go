package noticetemplate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"light-media/internal/jinja/exec"
	"light-media/internal/notice"
)

// TemplateLookup performs the host's NotificationTemplates lookup, returning
// the original JSON dictionary (including nil or non-dictionary configuration).
type TemplateLookup func(context.Context) (any, error)

type MessageResult struct {
	Message     *notice.Envelope
	SourceError string       // caught by the source _apply_template_data, not delivery
	SourceCache *SourceCache // this call only, including later caught assignment failures
}

// RenderMessage implements MessageTemplateHelper's source conditions and its
// caught-exception/partial-mutation semantics. Unsupported native dependencies
// and cancellation are kept separate from a source exception caught normally.
func (h *Helper) RenderMessage(ctx context.Context, message *notice.Envelope, business ContextInput, lookup TemplateLookup) (MessageResult, error) {
	result := MessageResult{Message: message}
	if message == nil || templateTruth(message.Get("title")) || templateTruth(message.Get("text")) || !templateTruth(message.Get("ctype")) {
		return result, nil
	}
	// In actual Router.Post current_time is present in kwargs. Direct source
	// calls with no positional or keyword business data do not render.
	if business.Meta == nil && business.Media == nil && business.Torrent == nil && business.Transfer == nil && len(business.Extra) == 0 && business.FileExtension == nil && business.Episodes == nil && business.IncludeRawObjects == nil {
		return result, nil
	}
	caught := func(err error) (MessageResult, error) {
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil || errors.Is(err, exec.ErrUnsupported) || errors.Is(err, notice.ErrTemplateDependency) || errors.Is(err, notice.ErrDependency) {
			return result, err
		}
		var n notice.SourceError
		var s SourceError
		if errors.As(err, &n) {
			result.SourceError = n.Class
		} else if errors.As(err, &s) {
			result.SourceError = s.Class
		} else {
			// Host storage/protocol failures are not Python source exceptions.
			return result, err
		}
		return result, nil
	}
	if lookup == nil {
		return result, notice.ErrTemplateDependency
	}
	templates, err := lookup(ctx)
	if err != nil {
		return caught(err)
	}
	var content any
	switch x := templates.(type) {
	case map[string]any:
		key, err := message.TemplateEnumValue("ctype")
		if err != nil {
			return caught(err)
		}
		content = x[key]
	case *exec.Dict:
		key, err := message.TemplateEnumValue("ctype")
		if err != nil {
			return caught(err)
		}
		for _, p := range x.Pairs {
			if p.Key.IsString() && p.Key.String() == key {
				content = p.Value.Interface()
				break
			}
		}
	default:
		return caught(SourceError{"AttributeError"})
	}
	if !templateTruth(content) {
		return result, nil
	}
	rendered, cache, err := h.RenderWithTrace(ctx, content, "literal", business)
	result.SourceCache = cache
	if err != nil {
		return caught(err)
	}
	dict, ok := asDict(rendered)
	if !ok {
		return caught(SourceError{"AttributeError"})
	}
	fields := make([]notice.TemplateField, 0, len(dict.Pairs))
	for _, p := range dict.Pairs {
		key, ok := p.Key.Interface().(string)
		if !ok {
			return caught(SourceError{"TypeError"})
		}
		// Keep unbounded integers and JSON objects as original JSON values,
		// rather than serializing the native ordered dictionary's Go fields.
		raw, err := exec.PythonDumps(p.Value.Interface(), exec.JSONOptions{})
		if err != nil {
			return caught(err)
		}
		v, err := noticeJSONValue([]byte(raw))
		if err != nil {
			return result, err
		}
		fields = append(fields, notice.TemplateField{Name: key, Value: v})
	}
	return caught(message.ApplyTemplateFields(fields))
}

func noticeJSONValue(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, exec.ErrUnsupported
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, exec.ErrUnsupported
	}
	return value, nil
}
