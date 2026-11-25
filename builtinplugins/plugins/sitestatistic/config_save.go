package sitestatistic

import (
	"bytes"
	"encoding/json"
	"slices"
)

// This source writes only config["onlyonce"] before saving the supplied dict.
// Preserve the input's other raw values and first key slots, so a later cold
// init sees the same nested object order for Python configuration text.
func onlyOnceConfig(raw json.RawMessage) (json.RawMessage, error) {
	type field struct {
		name       wireText
		key, value json.RawMessage
	}
	fields := []field{}
	d := json.NewDecoder(bytes.NewReader(raw))
	if _, e := d.Token(); e != nil {
		return nil, e
	}
	for d.More() {
		before := d.InputOffset()
		if _, e := d.Token(); e != nil {
			return nil, e
		}
		key := bytes.TrimLeft(raw[before:d.InputOffset()], ", \t\r\n")
		name, e := decodeWireText(key)
		if e != nil {
			return nil, e
		}
		var value json.RawMessage
		if e = d.Decode(&value); e != nil {
			return nil, e
		}
		found := false
		for i := range fields {
			if slices.Equal(fields[i].name, name) {
				fields[i].value = value
				found = true
				break
			}
		}
		if !found {
			fields = append(fields, field{name, key, value})
		}
	}
	if _, e := d.Token(); e != nil {
		return nil, e
	}
	found := false
	for i := range fields {
		if slices.Equal(fields[i].name, wireText("onlyonce")) {
			fields[i].value = json.RawMessage(`false`)
			found = true
		}
	}
	if !found {
		fields = append(fields, field{wireText("onlyonce"), json.RawMessage(`"onlyonce"`), json.RawMessage(`false`)})
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, f := range fields {
		if i != 0 {
			out.WriteByte(',')
		}
		out.Write(f.key)
		out.WriteByte(':')
		out.Write(f.value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
