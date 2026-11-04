package mediaservervendors

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"light-media/internal/plugins"
)

type ejMembers struct {
	keys   []json.RawMessage
	values map[string]json.RawMessage
}

func ejNull(v json.RawMessage) bool {
	return len(bytes.TrimSpace(v)) == 0 || bytes.Equal(bytes.TrimSpace(v), []byte(`null`))
}
func ejObject(raw json.RawMessage) (ejMembers, error) {
	out := ejMembers{values: map[string]json.RawMessage{}}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' || !json.Valid(raw) {
		return out, errors.New("expected JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if _, err := d.Token(); err != nil {
		return out, err
	}
	for d.More() {
		start := d.InputOffset()
		if _, err := d.Token(); err != nil {
			return out, err
		}
		key := bytes.TrimLeft(raw[start:d.InputOffset()], " \t\r\n,")
		points, err := plugins.DecodeSystemConfigJSONString(key)
		if err != nil {
			return out, err
		}
		canonical, err := plugins.EncodeSystemConfigJSONString(points)
		if err != nil {
			return out, err
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return out, err
		}
		name := string(canonical)
		if _, ok := out.values[name]; !ok {
			out.keys = append(out.keys, canonical)
		}
		out.values[name] = ejCopy(value)
	}
	return out, nil
}
func (o ejMembers) get(k string) json.RawMessage { return o.getDefault(k, json.RawMessage(`null`)) }
func (o ejMembers) getDefault(k string, d json.RawMessage) json.RawMessage {
	key, _ := plugins.EncodeSystemConfigJSONString([]rune(k))
	if v, ok := o.values[string(key)]; ok {
		return v
	}
	return d
}
func ejText(raw json.RawMessage) (string, error) {
	p, err := plugins.DecodeSystemConfigJSONString(raw)
	if err != nil {
		return "", err
	}
	for _, r := range p {
		if r >= 0xd800 && r <= 0xdfff {
			return "", ejSource("UnicodeEncodeError")
		}
	}
	return string(p), nil
}

type ejURL struct {
	text      string
	points    []rune
	encodable bool
}

func ejStandardize(raw json.RawMessage) (ejURL, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return ejURL{}, ejSource("AttributeError")
	}
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	if err != nil {
		return ejURL{}, err
	}
	if len(points) == 0 || points[len(points)-1] != '/' {
		points = append(points, '/')
	}
	text := string(points)
	if !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
		points = append([]rune("http://"), points...)
	}
	valid := true
	for _, r := range points {
		if r >= 0xd800 && r <= 0xdfff {
			valid = false
		}
	}
	return ejURL{text: string(points), points: points, encodable: valid}, nil
}
func ejTruth(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case 'n', 'f':
		return false
	case 't':
		return true
	case '"':
		p, e := plugins.DecodeSystemConfigJSONString(raw)
		return e == nil && len(p) > 0
	case '[':
		var a []json.RawMessage
		return json.Unmarshal(raw, &a) == nil && len(a) > 0
	case '{':
		o, e := ejObject(raw)
		return e == nil && len(o.keys) > 0
	}
	if bytes.ContainsAny(raw, ".eE") {
		f, _ := strconv.ParseFloat(string(raw), 64)
		return f != 0
	}
	n, ok := new(big.Int).SetString(string(raw), 10)
	return ok && n.Sign() != 0
}
func ejIter(raw json.RawMessage) ([]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, ejSource("TypeError")
	}
	switch raw[0] {
	case '[':
		var a []json.RawMessage
		err := json.Unmarshal(raw, &a)
		return a, err
	case '{':
		o, err := ejObject(raw)
		return o.keys, err
	case '"':
		p, err := plugins.DecodeSystemConfigJSONString(raw)
		if err != nil {
			return nil, err
		}
		out := make([]json.RawMessage, 0, len(p))
		for _, r := range p {
			x, e := plugins.EncodeSystemConfigJSONString([]rune{r})
			if e != nil {
				return nil, e
			}
			out = append(out, x)
		}
		return out, nil
	}
	return nil, ejSource("TypeError")
}
func ejLen(raw json.RawMessage) (int, error) { items, err := ejIter(raw); return len(items), err }
func ejNumber(raw json.RawMessage) (*big.Rat, bool) {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte(`true`)) {
		return new(big.Rat).SetInt64(1), true
	}
	if bytes.Equal(raw, []byte(`false`)) {
		return new(big.Rat), true
	}
	if len(raw) == 0 || raw[0] == '"' || raw[0] == '[' || raw[0] == '{' || raw[0] == 'n' {
		return nil, false
	}
	if bytes.ContainsAny(raw, ".eE") {
		v, _ := strconv.ParseFloat(string(raw), 64)
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return nil, false
		}
		return new(big.Rat).SetFloat64(v), true
	}
	i, ok := new(big.Int).SetString(string(raw), 10)
	if !ok {
		return nil, false
	}
	return new(big.Rat).SetInt(i), true
}
func ejEqual(a, b json.RawMessage) bool {
	a = bytes.TrimSpace(a)
	b = bytes.TrimSpace(b)
	if ejNull(a) || ejNull(b) {
		return ejNull(a) && ejNull(b)
	}
	if na, ok := ejNumber(a); ok {
		nb, yes := ejNumber(b)
		return yes && na.Cmp(nb) == 0
	}
	if a[0] != b[0] {
		return false
	}
	switch a[0] {
	case '"':
		x, e := plugins.DecodeSystemConfigJSONString(a)
		y, f := plugins.DecodeSystemConfigJSONString(b)
		if e != nil || f != nil || len(x) != len(y) {
			return false
		}
		for i := range x {
			if x[i] != y[i] {
				return false
			}
		}
		return true
	case '[':
		x, e := ejIter(a)
		y, f := ejIter(b)
		if e != nil || f != nil || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !ejEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case '{':
		x, e := ejObject(a)
		y, f := ejObject(b)
		if e != nil || f != nil || len(x.values) != len(y.values) {
			return false
		}
		for k, v := range x.values {
			w, ok := y.values[k]
			if !ok || !ejEqual(v, w) {
				return false
			}
		}
		return true
	}
	return bytes.Equal(a, b)
}

// Only JSON values are represented: no arbitrary Python object protocol.
func ejPythonText(raw json.RawMessage, repr bool) (string, error) {
	raw = bytes.TrimSpace(raw)
	if ejNull(raw) {
		return "None", nil
	}
	switch raw[0] {
	case 't':
		return "True", nil
	case 'f':
		return "False", nil
	case '"':
		s, err := ejText(raw)
		if err != nil {
			return "", err
		}
		if !repr {
			return s, nil
		}
		quote := byte('\'')
		if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
			quote = '"'
		}
		var out strings.Builder
		out.WriteByte(quote)
		for _, r := range s {
			switch r {
			case '\\':
				out.WriteString(`\\`)
			case '\n':
				out.WriteString(`\n`)
			case '\r':
				out.WriteString(`\r`)
			case '\t':
				out.WriteString(`\t`)
			default:
				if r == rune(quote) {
					out.WriteByte('\\')
					out.WriteRune(r)
				} else if r < 32 || r == 127 {
					out.WriteString(`\x`)
					out.WriteString(strconv.FormatInt(int64(r>>4), 16))
					out.WriteString(strconv.FormatInt(int64(r&15), 16))
				} else {
					out.WriteRune(r)
				}
			}
		}
		out.WriteByte(quote)
		return out.String(), nil
	case '[':
		values, err := ejIter(raw)
		if err != nil {
			return "", err
		}
		parts := make([]string, 0, len(values))
		for _, v := range values {
			s, e := ejPythonText(v, true)
			if e != nil {
				return "", e
			}
			parts = append(parts, s)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case '{':
		o, err := ejObject(raw)
		if err != nil {
			return "", err
		}
		parts := make([]string, 0, len(o.keys))
		for _, key := range o.keys {
			k, e := ejText(key)
			if e != nil {
				return "", e
			}
			ks, e := ejPythonText(key, true)
			if e != nil {
				return "", e
			}
			vs, e := ejPythonText(o.get(k), true)
			if e != nil {
				return "", e
			}
			parts = append(parts, ks+": "+vs)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	if !bytes.ContainsAny(raw, ".eE") {
		n, ok := new(big.Int).SetString(string(raw), 10)
		if !ok {
			return "", ejSource("ValueError")
		}
		return n.String(), nil
	}
	f, _ := strconv.ParseFloat(string(raw), 64)
	if math.IsInf(f, 1) {
		return "inf", nil
	}
	if math.IsInf(f, -1) {
		return "-inf", nil
	}
	format := byte('g')
	abs := math.Abs(f)
	if abs == 0 || (abs >= 1e-4 && abs < 1e16) {
		format = 'f'
	}
	s := strconv.FormatFloat(f, format, -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s, nil
}
