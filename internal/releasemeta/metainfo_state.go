package releasemeta

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
)

func (o *MetaObject) Has(key string) bool { _, ok := o.Instance[key]; return ok }
func cloneNumber(value any) any {
	if n, ok := value.(*big.Int); ok && n != nil {
		return new(big.Int).Set(n)
	}
	return value
}

// ShallowCopy corresponds to copy.copy(MetaBase): a new instance dictionary,
// while list/dictionary references remain shared. Integers retain value semantics.
func (o *MetaObject) ShallowCopy() *MetaObject {
	if o == nil {
		return nil
	}
	out := &MetaObject{Kind: o.Kind, Instance: make(map[string]any, len(o.Instance))}
	for k, v := range o.Instance {
		out.Instance[k] = cloneNumber(v)
	}
	return out
}
func deepValue(v any) any {
	switch x := v.(type) {
	case []string:
		return append(make([]string, 0, len(x)), x...)
	case []any:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = deepValue(v)
		}
		return r
	case map[string]any:
		r := make(map[string]any, len(x))
		for k, v := range x {
			r[k] = deepValue(v)
		}
		return r
	default:
		return cloneNumber(v)
	}
}
func (o *MetaObject) DeepCopy() *MetaObject {
	if o == nil {
		return nil
	}
	return &MetaObject{Kind: o.Kind, Instance: deepValue(o.Instance).(map[string]any)}
}

type objectState struct {
	Version  int            `json:"version"`
	Kind     string         `json:"kind"`
	Instance map[string]any `json:"instance"`
}

// EncodeState is an internal persistence format, NOT MetaBase.to_dict. It keeps
// missing/null/empty/zero and private instance fields, without derived additions.
func (o *MetaObject) EncodeState() ([]byte, error) {
	if o == nil {
		return nil, errors.New("metainfo_object_nil")
	}
	if o.Kind != "video" && o.Kind != "anime" {
		return nil, errors.New("metainfo_state_invalid")
	}
	return json.Marshal(objectState{1, o.Kind, o.Instance})
}
func DecodeMetaObjectState(data []byte) (*MetaObject, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	var s objectState
	if e := d.Decode(&s); e != nil {
		return nil, e
	}
	if s.Version != 1 || s.Instance == nil || (s.Kind != "video" && s.Kind != "anime") {
		return nil, errors.New("metainfo_state_invalid")
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, errors.New("metainfo_state_trailing_data")
	}
	if !validStateValue(s.Instance) {
		return nil, errors.New("metainfo_state_noninteger")
	}
	return &MetaObject{Kind: s.Kind, Instance: s.Instance}, nil
}
func validStateValue(v any) bool {
	switch x := v.(type) {
	case json.Number:
		_, ok := new(big.Int).SetString(string(x), 10)
		return ok
	case []any:
		for _, v := range x {
			if !validStateValue(v) {
				return false
			}
		}
	case map[string]any:
		for _, v := range x {
			if !validStateValue(v) {
				return false
			}
		}
	}
	return true
}

// InitSubtitle exposes the source object's mutation without rebuilding its title.
func (o *MetaObject) InitSubtitle(text string) error { return fullSubtitle(o, text, "legacy") }
