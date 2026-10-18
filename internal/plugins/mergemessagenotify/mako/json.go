package mako

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

func DecodeVariables(raw string) (map[string]any, error) {
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	value, err := decodeValue(d)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errors.New("trailing template JSON")
	}
	obj, ok := value.(OrderedObject)
	if !ok {
		return nil, errors.New("template variables must be an object")
	}
	out := map[string]any{}
	for _, f := range obj {
		out[f.Key] = f.Value
	}
	return out, nil
}
func decodeValue(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		obj := OrderedObject{}
		indices := map[string]int{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			k, ok := key.(string)
			if !ok {
				return nil, errors.New("invalid JSON key")
			}
			value, err := decodeValue(d)
			if err != nil {
				return nil, err
			}
			if i, ok := indices[k]; ok {
				obj[i].Value = value
			} else {
				indices[k] = len(obj)
				obj = append(obj, Field{k, value})
			}
		}
		_, err = d.Token()
		return obj, err
	case '[':
		out := []any{}
		for d.More() {
			value, err := decodeValue(d)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		_, err = d.Token()
		return out, err
	}
	return nil, errors.New("invalid JSON delimiter")
}
func RenderJSON(ctx context.Context, text, variables string) (string, error) {
	v, err := DecodeVariables(variables)
	if err != nil {
		return "", err
	}
	return Render(ctx, text, v)
}
