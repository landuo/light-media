package larkmessager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SourceObject preserves JSON dictionary order where Python str(dict) exposes it.
// Ordinary Go maps have no insertion order; multi-key repr must not guess.
type SourceObject struct {
	Values Object
	Keys   []string
}

func (o *SourceObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.Keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(uuidJSON(k))
		b.WriteByte(':')
		v, e := json.Marshal(o.Values[k])
		if e != nil {
			return nil, e
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
func DecodeSource(raw []byte) (any, error) {
	if !utf8.Valid(raw) || !validSurrogatePairs(raw) {
		return nil, ErrUnsupported
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var read func() (any, error)
	read = func() (any, error) {
		token, e := d.Token()
		if e != nil {
			return nil, e
		}
		switch token {
		case json.Delim('{'):
			o := &SourceObject{Values: Object{}, Keys: []string{}}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				key, ok := k.(string)
				if !ok {
					return nil, ErrUnsupported
				}
				v, e := read()
				if e != nil {
					return nil, e
				}
				if _, exists := o.Values[key]; !exists {
					o.Keys = append(o.Keys, key)
				}
				o.Values[key] = v
			}
			_, e = d.Token()
			return o, e
		case json.Delim('['):
			a := []any{}
			for d.More() {
				v, e := read()
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			_, e = d.Token()
			return a, e
		case json.Delim('}'), json.Delim(']'):
			return nil, ErrUnsupported
		default:
			if n, ok := token.(json.Number); ok {
				if !strings.ContainsAny(string(n), ".eE") {
					if len(strings.TrimPrefix(string(n), "-")) > 4300 {
						return nil, ErrUnsupported
					}
				} else if f, _ := strconv.ParseFloat(string(n), 64); math.IsInf(f, 0) {
					// Python accepts this as infinity; its complete non-finite JSON
					// round trip is not implemented. Reject explicitly, not silently.
					return nil, ErrUnsupported
				}
			}
			return token, nil
		}
	}
	v, e := read()
	if e != nil {
		return nil, e
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, ErrUnsupported
	}
	return v, nil
}
func DecodeSourceObject(raw []byte) (Object, error) {
	v, e := DecodeSource(raw)
	if e != nil {
		return nil, e
	}
	o := obj(v)
	if o == nil {
		return nil, ErrUnsupported
	}
	return o, nil
}
func pythonFloat(f float64) string {
	if math.IsInf(f, 1) {
		return "inf"
	}
	if math.IsInf(f, -1) {
		return "-inf"
	}
	if math.IsNaN(f) {
		return "nan"
	}
	format := byte('g')
	a := math.Abs(f)
	if a == 0 || a >= 1e-4 && a < 1e16 {
		format = 'f'
	}
	s := strconv.FormatFloat(f, format, -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}
func numberString(n json.Number) string {
	if strings.ContainsAny(string(n), ".eE") {
		f, _ := strconv.ParseFloat(string(n), 64)
		return pythonFloat(f)
	}
	i, ok := new(big.Int).SetString(string(n), 10)
	if !ok {
		return string(n)
	}
	return i.String()
}
func numberTruthy(n json.Number) bool {
	if strings.ContainsAny(string(n), ".eE") {
		f, _ := strconv.ParseFloat(string(n), 64)
		return f != 0
	}
	i, ok := new(big.Int).SetString(string(n), 10)
	return ok && i.Sign() != 0
}

type IntegerError struct{ Class string }

func (e *IntegerError) Error() string { return "Python " + e.Class + " during integer conversion" }
func (e *IntegerError) Unwrap() error { return ErrUnsupported }
func integerErrorClass(e error) string {
	var typed *IntegerError
	if errors.As(e, &typed) {
		return typed.Class
	}
	return "ValueError"
}
func pythonInt(v any) (*big.Int, error) {
	switch v := v.(type) {
	case int:
		return big.NewInt(int64(v)), nil
	case int64:
		return big.NewInt(v), nil
	case bool:
		if v {
			return big.NewInt(1), nil
		}
		return big.NewInt(0), nil
	case string:
		n, e := legacyInteger(v)
		if e != nil {
			return nil, &IntegerError{"ValueError"}
		}
		i, _ := new(big.Int).SetString(n, 10)
		return i, nil
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			return pythonInt(string(v))
		}
		f, _ := strconv.ParseFloat(string(v), 64)
		return pythonInt(f)
	case float64:
		if math.IsInf(v, 0) {
			return nil, &IntegerError{"OverflowError"}
		}
		if math.IsNaN(v) {
			return nil, &IntegerError{"ValueError"}
		}
		i, _ := big.NewFloat(v).Int(nil)
		return i, nil
	}
	return nil, &IntegerError{"TypeError"}
}

func PythonString(v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "None", nil
	case string:
		if !utf8.ValidString(v) {
			return "", ErrUnsupported
		}
		return v, nil
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case json.Number:
		return numberString(v), nil
	case float64:
		return pythonFloat(v), nil
	case int, int64:
		return fmt.Sprint(v), nil
	case *SourceObject:
		return pythonRepr(v)
	case Object:
		if len(v) > 1 {
			return "", errors.Join(ErrUnsupported, errors.New("Python dictionary repr requires original order"))
		}
		return pythonRepr(v)
	case []any:
		return pythonRepr(v)
	case []string:
		a := []any{}
		for _, s := range v {
			a = append(a, s)
		}
		return pythonRepr(a)
	}
	return "", ErrUnsupported
}
func pythonRepr(v any) (string, error) {
	switch v := v.(type) {
	case string:
		quote := byte('\'')
		if strings.Contains(v, "'") && !strings.Contains(v, "\"") {
			quote = '"'
		}
		var b strings.Builder
		b.WriteByte(quote)
		for _, r := range v {
			switch r {
			case '\\':
				b.WriteString(`\\`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r == rune(quote) {
					b.WriteByte('\\')
					b.WriteRune(r)
				} else if pythonPrintable(r) {
					b.WriteRune(r)
				} else if r < 256 {
					fmt.Fprintf(&b, `\x%02x`, r)
				} else if r <= 65535 {
					fmt.Fprintf(&b, `\u%04x`, r)
				} else {
					fmt.Fprintf(&b, `\U%08x`, r)
				}
			}
		}
		b.WriteByte(quote)
		return b.String(), nil
	case *SourceObject:
		parts := []string{}
		for _, k := range v.Keys {
			ks, _ := pythonRepr(k)
			vs, e := pythonRepr(v.Values[k])
			if e != nil {
				return "", e
			}
			parts = append(parts, ks+": "+vs)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	case Object:
		if len(v) > 1 {
			return "", ErrUnsupported
		}
		keys := []string{}
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return pythonRepr(&SourceObject{Values: v, Keys: keys})
	case []any:
		parts := []string{}
		for _, x := range v {
			s, e := pythonRepr(x)
			if e != nil {
				return "", e
			}
			parts = append(parts, s)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	default:
		return PythonString(v)
	}
}

// Printability must use the actual CPython Unicode15 table, not Go's version.
// The complete 711-range table is generated from the installed runtime.
func pythonPrintable(r rune) bool { return pythonPrintable15(r) }

// Go strings cannot losslessly store isolated UTF16 surrogates. Reject them
// explicitly instead of encoding/json's silent replacement with U+FFFD.
func validSurrogatePairs(raw []byte) bool {
	inString := false
	hex4 := func(p []byte) (int, bool) {
		if len(p) < 4 {
			return 0, false
		}
		v := 0
		for _, c := range p[:4] {
			v *= 16
			switch {
			case c >= '0' && c <= '9':
				v += int(c - '0')
			case c >= 'a' && c <= 'f':
				v += int(c-'a') + 10
			case c >= 'A' && c <= 'F':
				v += int(c-'A') + 10
			default:
				return 0, false
			}
		}
		return v, true
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '"' {
			inString = !inString
			continue
		}
		if !inString || c != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		v, ok := hex4(raw[i+1:])
		if !ok {
			return false
		}
		i += 4
		if v >= 0xdc00 && v <= 0xdfff {
			return false
		}
		if v >= 0xd800 && v <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, ok := hex4(raw[i+3:])
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
