// Package larkmessager is an isolated, source-referenced Go port candidate of
// installed LarkMessager 0.9.1. It is not registered in the application.
package larkmessager

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/releasemeta/legacyanime"
	"strings"
)

var ErrDependency = errors.New("lark dependency unavailable")
var ErrUnsupported = errors.New("unsupported Lark input")
var ErrDenied = errors.New("Lark capability denied")
var ErrUnknown = errors.New("Lark effect outcome unknown")
var ErrRejected = errors.New("Lark API rejected request")

// Object preserves JSON values with json.Number, including unknown config fields.
type Object = map[string]any

func decode(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrUnsupported
	}
	return nil
}
func clone[T any](v T) T {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	var o T
	if e = decode(b, &o); e != nil {
		panic(e)
	}
	return o
}
func obj(v any) Object {
	if o, ok := v.(*SourceObject); ok {
		return o.Values
	}
	o, _ := v.(map[string]any)
	return o
}
func arr(v any) []any { a, _ := v.([]any); return a }
func str(v any) string {
	s, e := PythonString(v)
	if e != nil {
		panic(sourceFailure{e})
	}
	return s
}

func text(v any) string {
	if !truth(v) {
		return ""
	}
	return str(v)
}
func truth(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		return numberTruthy(v)
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case []any:
		return len(v) > 0
	case Object:
		return len(v) > 0
	case *SourceObject:
		return len(v.Values) > 0
	case []string:
		return len(v) > 0
	}
	return true
}
func first(v ...any) any {
	for _, x := range v {
		if truth(x) {
			return x
		}
	}
	return nil
}
func strip(v string) string { return legacyanime.Strip(v) }
func trim(v any) string     { return strip(text(v)) }
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func joined(v []string) string { return strings.Join(v, "\n") }
func zero(v any) bool {
	switch v := v.(type) {
	case json.Number:
		return !numberTruthy(v)
	case int:
		return v == 0
	case float64:
		return v == 0
	case bool:
		return !v
	}
	return false
}

// Target retains the source's explicit-user precedence and separate defaults.
type Target struct {
	UserID            string `json:"userid,omitempty"`
	ChatID            string `json:"chat_id,omitempty"`
	ReceiveIDType     string `json:"receive_id_type,omitempty"`
	OriginalMessageID string `json:"original_message_id,omitempty"`
	DefaultOpenID     string `json:"default_open_id,omitempty"`
	DefaultChatID     string `json:"default_chat_id,omitempty"`
}
type Button struct {
	Text         any `json:"text"`
	URL          any `json:"url,omitempty"`
	CallbackData any `json:"callback_data,omitempty"`
}
type ImageItem struct {
	Title    string `json:"title"`
	ImageKey string `json:"image_key,omitempty"`
	URL      string `json:"url,omitempty"`
}
type CardInput struct {
	Title          string      `json:"title"`
	Text           string      `json:"text"`
	Link           string      `json:"link"`
	Buttons        [][]Button  `json:"buttons"`
	ImageKey       string      `json:"image_key"`
	HeaderTemplate string      `json:"header_template"`
	ImageItems     []ImageItem `json:"image_items"`
}
type Notification struct {
	Title             string     `json:"title"`
	Text              string     `json:"text"`
	Image             string     `json:"image"`
	Link              string     `json:"link"`
	MType             string     `json:"mtype"`
	Buttons           [][]Button `json:"buttons"`
	UserID            string     `json:"userid"`
	Targets           Object     `json:"targets"`
	OriginalMessageID string     `json:"original_message_id"`
	FilePath          string     `json:"file_path"`
	FileName          string     `json:"file_name"`
	VoicePath         string     `json:"voice_path"`
	VoiceCaption      string     `json:"voice_caption"`
}
type Result struct {
	Success   bool   `json:"success"`
	MessageID any    `json:"message_id"`
	ChatID    any    `json:"chat_id"`
	MsgType   any    `json:"msg_type"`
	RootID    any    `json:"root_id,omitempty"`
	ParentID  any    `json:"parent_id,omitempty"`
	ThreadID  any    `json:"thread_id,omitempty"`
	Metadata  Object `json:"metadata,omitempty"`
}

// MessageResponse is the actual module-level source model, distinct from the
// richer LarkClient result shape.
type MessageResponse struct {
	MessageID any    `json:"message_id"`
	ChatID    any    `json:"chat_id"`
	Channel   string `json:"channel"`
	Source    string `json:"source"`
	Metadata  Object `json:"metadata"`
	Success   bool   `json:"success"`
}

func (b *Button) UnmarshalJSON(raw []byte) error {
	v, e := DecodeSource(raw)
	if e != nil {
		return e
	}
	if v == nil {
		*b = Button{}
		return nil
	}
	o := obj(v)
	if o == nil {
		return ErrUnsupported
	}
	*b = Button{Text: o["text"], URL: o["url"], CallbackData: o["callback_data"]}
	return nil
}

func containsSource(v any, needle string) bool {
	switch v := v.(type) {
	case string:
		return strings.Contains(v, needle)
	case []string:
		return includes(v, needle)
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && s == needle {
				return true
			}
		}
		return false
	case Object:
		_, ok := v[needle]
		return ok
	case *SourceObject:
		_, ok := v.Values[needle]
		return ok
	}
	panic(sourceFailure{ErrUnsupported})
}
