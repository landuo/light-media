package noticetemplate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"

	"light-media/internal/jinja/exec"
)

// DecodeContextJSON retains nested dictionary insertion order. An ordinary Go
// map cannot establish the original order for iteration or representation.
func DecodeContextJSON(raw []byte) (Object, error) {
	raw, specials := pythonJSONConstants(raw)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := decodeOrderedConvert(d, func(value any) any {
		if n, ok := value.(json.Number); ok {
			if special, ok := specials[string(n)]; ok {
				return special
			}
		}
		return normalizeNumbers(value)
	})
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing context data")
	}
	obj, ok := v.(*exec.Dict)
	if !ok {
		return nil, fmt.Errorf("context must be an object")
	}
	out := Object{}
	for _, p := range obj.Pairs {
		out[p.Key.String()] = p.Value.Interface()
	}
	return out, nil
}
func decodeOrdered(d *json.Decoder) (any, error) {
	return decodeOrderedConvert(d, normalizeNumbers)
}

func DecodeValueJSON(raw []byte) (any, error) {
	raw, specials := pythonJSONConstants(raw)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := decodeOrderedConvert(d, func(v any) any {
		if n, ok := v.(json.Number); ok {
			if special, ok := specials[string(n)]; ok {
				return special
			}
			if !strings.ContainsAny(string(n), ".eE") {
				if z, ok := new(big.Int).SetString(string(n), 10); ok {
					return z
				}
			}
			f, e := strconv.ParseFloat(string(n), 64)
			if e == nil || math.IsInf(f, 0) {
				return f
			}
		}
		return v
	})
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	return v, nil
}

// Python json.loads accepts these three constants even though RFC JSON does
// not. Replace only lexical values outside strings with collision-free numeric
// placeholders, then let the ordinary decoder validate the complete grammar.
func pythonJSONConstants(raw []byte) ([]byte, map[string]float64) {
	specials := map[string]float64{}
	marker := "987654321012345678901234567890"
	for bytes.Contains(raw, []byte(marker)) {
		marker += "0"
	}
	var out bytes.Buffer
	quoted, escaped := false, false
	boundary := func(b byte) bool {
		return b == ' ' || b == '\n' || b == '\r' || b == '\t' || b == ',' || b == '[' || b == ']' || b == '{' || b == '}' || b == ':'
	}
	for i := 0; i < len(raw); {
		b := raw[i]
		if quoted {
			out.WriteByte(b)
			i++
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		if b == '"' {
			quoted = true
			out.WriteByte(b)
			i++
			continue
		}
		matched := false
		for _, constant := range []struct {
			text  string
			value float64
		}{{"NaN", math.NaN()}, {"Infinity", math.Inf(1)}, {"-Infinity", math.Inf(-1)}} {
			end := i + len(constant.text)
			if end <= len(raw) && string(raw[i:end]) == constant.text && (i == 0 || boundary(raw[i-1])) && (end == len(raw) || boundary(raw[end])) {
				key := marker + strconv.Itoa(len(specials))
				specials[key] = constant.value
				out.WriteString(key)
				i = end
				matched = true
				break
			}
		}
		if !matched {
			out.WriteByte(b)
			i++
		}
	}
	return out.Bytes(), specials
}

func decodeOrderedConvert(d *json.Decoder, convert func(any) any) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return convert(t), nil
	}
	switch delim {
	case '{':
		out := exec.NewDict()
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			value, err := decodeOrderedConvert(d, convert)
			if err != nil {
				return nil, err
			}
			k := exec.AsValue(key)
			found := false
			for _, p := range out.Pairs {
				if p.Key.EqualValueTo(k) {
					p.Value = exec.AsValue(value)
					found = true
					break
				}
			}
			if !found {
				out.Pairs = append(out.Pairs, &exec.Pair{Key: k, Value: exec.AsValue(value)})
			}
		}
		_, err = d.Token()
		return out, err
	case '[':
		out := []any{}
		for d.More() {
			value, err := decodeOrderedConvert(d, convert)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		_, err = d.Token()
		return out, err
	}
	return nil, fmt.Errorf("invalid JSON delimiter")
}
