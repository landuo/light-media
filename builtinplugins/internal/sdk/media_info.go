package plugins

import "encoding/json"

// ModuleMediaInfo is the ordinary source MediaInfo's explicit JSON field set.
// It is shared by the image and torrent-filter callback DTOs; pointer reuse
// means the same input model, while Clone creates a separate equal model.
// No reflection or cross-operation object registry is involved.
type ModuleMediaInfo struct {
	Fields map[string]json.RawMessage
}

func (m *ModuleMediaInfo) Clone() *ModuleMediaInfo {
	if m == nil {
		return nil
	}
	out := &ModuleMediaInfo{Fields: map[string]json.RawMessage{}}
	for key, value := range m.Fields {
		out.Fields[key] = append(json.RawMessage(nil), value...)
	}
	return out
}
