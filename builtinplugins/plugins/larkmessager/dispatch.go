package larkmessager

import (
	"context"
	"encoding/json"
	"errors"

	sdk "light-media/builtinplugins/internal/sdk"
)

type moduleCall struct {
	Args   []json.RawMessage          `json:"args"`
	Kwargs map[string]json.RawMessage `json:"kwargs"`
}

func (c moduleCall) value(index int, name string) json.RawMessage {
	if value := c.Kwargs[name]; len(value) != 0 {
		return value
	}
	if index >= 0 && index < len(c.Args) {
		return c.Args[index]
	}
	return nil
}

func (c moduleCall) sourceValue(index int, name string) any {
	raw := c.value(index, name)
	if len(raw) == 0 {
		return nil
	}
	value, _ := DecodeSource(raw)
	return value
}

func (p *Plugin) invokeModule(ctx context.Context, method string, raw json.RawMessage) (_out json.RawMessage, _error error) {
	defer sourceErrors(&_error)
	if method == "message_parser" {
		input, err := sdk.DecodeMessageParserInput(raw)
		if err != nil || input.Source == nil {
			return nil, err
		}
		return marshalResult(p.ParseMessageBody(ctx, *input.Source, input.Body))
	}
	var call moduleCall
	if err := json.Unmarshal(raw, &call); err != nil {
		return nil, err
	}
	value := call.sourceValue
	field := func(index int, name string) string { return text(value(index, name)) }
	switch method {
	case "post_message", "post_medias_message", "post_torrents_message":
		var message Notification
		if err := json.Unmarshal(call.value(0, "message"), &message); err != nil {
			return nil, err
		}
		switch method {
		case "post_message":
			return nil, p.Post(ctx, message)
		case "post_medias_message":
			var items []MediaView
			if err := json.Unmarshal(call.value(1, "medias"), &items); err != nil {
				return nil, err
			}
			return nil, p.PostMedias(ctx, message, items)
		case "post_torrents_message":
			var items []TorrentView
			if err := json.Unmarshal(call.value(1, "torrents"), &items); err != nil {
				return nil, err
			}
			return nil, p.PostTorrents(ctx, message, items)
		}
	case "edit_message":
		var buttons [][]Button
		if raw := call.value(6, "buttons"); len(raw) != 0 {
			if err := json.Unmarshal(raw, &buttons); err != nil {
				return nil, err
			}
		}
		return marshalResult(p.Edit(ctx, field(0, "channel"), field(1, "source"), field(2, "message_id"),
			field(3, "chat_id"), field(5, "title"), field(4, "text"), buttons))
	case "mark_message_processing_started":
		return marshalResult(p.ProcessingStarted(ctx, field(0, "channel"), field(1, "source"), value(2, "userid"),
			field(3, "message_id"), field(4, "chat_id"), field(5, "text")))
	case "mark_message_processing_finished":
		return marshalResult(p.ProcessingFinished(ctx, field(0, "channel"), field(1, "source"),
			field(3, "message_id"), obj(value(5, "status"))))
	}
	return nil, errors.New("unsupported Lark module")
}
