// Package notice ports the JSON-shaped Notification and routing/time-window core.
// It contains no real channel sender, recipient database, template engine or daemon.
package notice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

type Object = map[string]any

type Issue struct {
	Type string `json:"type"`
	Loc  []any  `json:"loc"`
}
type ValidationError struct{ Issues []Issue }

func (e *ValidationError) Error() string {
	return fmt.Sprintf("notification validation: %d issue(s)", len(e.Issues))
}

type Envelope struct{ fields Object }

var fieldOrder = []string{"channel", "source", "mtype", "ctype", "title", "text", "image", "voice_path", "file_path", "file_name", "voice_caption", "link", "userid", "username", "date", "action", "targets", "buttons", "force_reply", "original_message_id", "original_chat_id", "disable_web_page_preview", "parse_mode", "save_history"}
var enumValues = map[string][]string{
	"channel": {"微信", "飞书", "微信ClawBot", "Telegram", "Slack", "Discord", "SynologyChat", "VoceChat", "Web", "WebAgent", "WebPush", "QQ"},
	"mtype":   {"资源下载", "整理入库", "订阅", "站点", "媒体服务器", "手动处理", "插件", "智能体", "其它"},
	"ctype":   {"subscribeAdded", "subscribeComplete", "organizeSuccess", "downloadAdded"},
}

// NewEnvelope accepts only JSON values. Unknown fields are ignored, like the
// installed unconfigured Pydantic BaseModel. Returned values are owned copies.
// It reproduces validated JSON coercions/error locations, not Python objects,
// Pydantic exception messages or arbitrary post-construction assignment rules.
func NewEnvelope(input Object) (*Envelope, error) {
	clean, err := jsonCopy(input)
	if err != nil {
		return nil, err
	}
	if clean == nil {
		clean = Object{}
	}
	in := clean.(map[string]any)
	out := Object{}
	issues := []Issue{}
	for _, field := range fieldOrder {
		value, ok := in[field]
		if !ok {
			switch field {
			case "action":
				value = json.Number("1")
			case "force_reply":
				value = false
			case "save_history":
				value = true
			default:
				value = nil
			}
			out[field] = value
			continue
		}
		add := func(kind string, path ...any) { issues = append(issues, Issue{kind, append([]any{field}, path...)}) }
		switch field {
		case "force_reply", "save_history", "disable_web_page_preview":
			if value == nil && field == "disable_web_page_preview" {
				out[field] = nil
				continue
			}
			v, kind := boolean(value)
			if kind != "" {
				add(kind)
			} else {
				out[field] = v
			}
		case "action":
			if value == nil {
				out[field] = nil
				continue
			}
			v, kind := integer(value)
			if kind != "" {
				add(kind)
			} else {
				out[field] = v
			}
		case "userid", "username", "original_message_id":
			if value == nil {
				out[field] = nil
				continue
			}
			if _, ok := value.(string); ok {
				out[field] = value
				continue
			}
			v, kind := integer(value)
			if kind != "" {
				add("string_type", "str")
				add(kind, "int")
			} else {
				out[field] = v
			}
		case "channel", "mtype", "ctype":
			if value == nil {
				out[field] = nil
				continue
			}
			valid := false
			if s, ok := value.(string); ok {
				for _, item := range enumValues[field] {
					if s == item {
						valid = true
						break
					}
				}
			}
			if !valid {
				add("enum")
			} else {
				out[field] = value
			}
		case "targets":
			if value == nil {
				out[field] = nil
				continue
			}
			if _, ok := value.(map[string]any); !ok {
				add("dict_type")
			} else {
				out[field] = value
			}
		case "buttons":
			if value == nil {
				out[field] = nil
				continue
			}
			rows, ok := value.([]any)
			if !ok {
				add("list_type")
				continue
			}
			for i, row := range rows {
				cols, ok := row.([]any)
				if !ok {
					add("list_type", i)
					continue
				}
				for j, col := range cols {
					if _, ok := col.(map[string]any); !ok {
						add("dict_type", i, j)
					}
				}
			}
			out[field] = value
		default:
			if value == nil {
				out[field] = nil
				continue
			}
			if _, ok := value.(string); !ok {
				add("string_type")
			} else {
				out[field] = value
			}
		}
	}
	if len(issues) > 0 {
		return nil, &ValidationError{issues}
	}
	return &Envelope{out}, nil
}
func (e *Envelope) Dump() Object {
	if e == nil || e.fields == nil {
		return nil
	}
	v, _ := jsonCopy(e.fields)
	return v.(map[string]any)
}
func (e *Envelope) Get(field string) any {
	if e == nil {
		return nil
	}
	v, _ := jsonCopy(e.fields[field])
	return v
}
func (e *Envelope) clone() *Envelope {
	if e == nil {
		return nil
	}
	return &Envelope{e.Dump()}
}
func jsonCopy(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	err = d.Decode(&out)
	return out, err
}
func owned(v any) any {
	out, err := jsonCopy(v)
	if err != nil {
		panic("non-JSON trusted dependency value")
	}
	return out
}
func truth(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			f, _ := strconv.ParseFloat(string(x), 64)
			return f != 0
		}
		r, ok := new(big.Rat).SetString(string(x))
		return !ok || r.Sign() != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}
func boolean(v any) (any, string) {
	switch x := v.(type) {
	case bool:
		return x, ""
	case json.Number:
		converted, kind := integer(x)
		if kind != "" {
			return nil, "bool_type"
		}
		if _, err := strconv.ParseInt(string(converted.(json.Number)), 10, 64); err != nil {
			return nil, "bool_type"
		}
		switch converted.(json.Number) {
		case "0":
			return false, ""
		case "1":
			return true, ""
		}
		return nil, "bool_parsing"
	case string:
		switch asciiLower(x) {
		case "1", "true", "on", "yes", "y", "t":
			return true, ""
		case "0", "false", "off", "no", "n", "f":
			return false, ""
		}
		return nil, "bool_parsing"
	default:
		return nil, "bool_type"
	}
}

func integer(v any) (any, string) {
	switch x := v.(type) {
	case bool:
		if x {
			return json.Number("1"), ""
		}
		return json.Number("0"), ""
	case string:
		return pydanticStringInt(x)
	case json.Number:
		s := string(x)
		if !strings.ContainsAny(s, ".eE") {
			n, ok := new(big.Int).SetString(s, 10)
			if ok {
				return json.Number(n.String()), ""
			}
			return nil, "int_parsing"
		}
		// JSON floats pass through CPython's IEEE754 value, including
		// subnormal rounding. Both +/-2^63 are rejected by this Pydantic path.
		f, err := strconv.ParseFloat(s, 64)
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, "finite_number"
		}
		if err != nil && f != 0 {
			return nil, "finite_number"
		}
		if f != math.Trunc(f) {
			return nil, "int_from_float"
		}
		if f <= -math.Exp2(63) || f >= math.Exp2(63) {
			return nil, "int_parsing_size"
		}
		return json.Number(strconv.FormatInt(int64(f), 10)), ""
	default:
		return nil, "int_type"
	}
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
