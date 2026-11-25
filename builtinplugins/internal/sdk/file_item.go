package plugins

import "encoding/json"

// ModuleFileItem is the ordinary FileItem field set used by local storage,
// transfer and metadata callbacks. It carries source values without inventing
// a filesystem authority or an object registry. Callers still authorize paths.
type ModuleFileItem struct {
	Fields map[string]json.RawMessage
}

// NewModuleFileItem applies the fixed FileItem constructor's defaults, nullable
// fields and scalar coercions. The local caller still owns filesystem access;
// model metadata cannot grant a path or establish a physical snapshot.
func NewModuleFileItem(value any) (*ModuleFileItem, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	fields, err := moduleTransferObject(raw)
	if err != nil {
		return nil, err
	}
	normalized, err := moduleTransferFileItem(fields)
	if err != nil {
		return nil, err
	}
	raw, err = json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	var item ModuleFileItem
	err = json.Unmarshal(raw, &item)
	return &item, err
}

func (f *ModuleFileItem) Clone() *ModuleFileItem {
	if f == nil {
		return nil
	}
	copy := &ModuleFileItem{Fields: map[string]json.RawMessage{}}
	for key, value := range f.Fields {
		copy.Fields[key] = append(json.RawMessage(nil), value...)
	}
	return copy
}

func (f ModuleFileItem) MarshalJSON() ([]byte, error)    { return json.Marshal(f.Fields) }
func (f *ModuleFileItem) UnmarshalJSON(raw []byte) error { return json.Unmarshal(raw, &f.Fields) }
