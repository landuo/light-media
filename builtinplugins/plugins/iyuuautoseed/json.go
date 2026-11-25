package iyuuautoseed

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// decodeJSON retains arbitrary integral JSON numbers. Python's non-finite JSON
// extensions and unpaired UTF-16 surrogates are outside this candidate's input
// contract; they are not silently converted into null or replacement characters.
func decodeJSON(raw []byte) (any, error) {
	if !utf8.Valid(raw) || hasUnpairedJSONSurrogate(raw) {
		return nil, errors.New("invalid UTF-8 JSON")
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("multiple JSON values")
	}
	return v, nil
}

func cloneJSON(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return decodeJSON(b)
}

func cloneMap(v map[string]any) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	cp, e := cloneJSON(v)
	if e != nil {
		return nil, e
	}
	return cp.(map[string]any), nil
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
		r, ok := numberRat(x)
		return ok && r.Sign() != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

func numberRat(n json.Number) (*big.Rat, bool) {
	s := n.String()
	if !strings.ContainsAny(s, ".eE") {
		z, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, false
		}
		return new(big.Rat).SetInt(z), true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, false
	}
	return new(big.Rat).SetFloat64(f), true
}

// scalarKey models Python dict equality for the scalar site IDs decoded by
// json.loads: 1, 1.0 and true alias; "1" is a distinct key. The first inserted
// key object is retained when a later equal key replaces its value.
func scalarKey(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "none", nil
	case bool:
		if x {
			return "num:1", nil
		}
		return "num:0", nil
	case json.Number:
		r, ok := numberRat(x)
		if !ok {
			return "", errors.New("non-finite site ID")
		}
		return "num:" + r.RatString(), nil
	case string:
		if !utf8.ValidString(x) {
			return "", errors.New("invalid UTF-8 site ID")
		}
		return "str:" + x, nil
	default:
		return "", errors.New("unhashable site ID")
	}
}

func zero(v any) bool { k, e := scalarKey(v); return e == nil && k == "num:0" }

// compactHashes is json.dumps(..., separators=(',', ':'), ensure_ascii=False),
// including its literal U+2028/U+2029 and lack of HTML escaping.
func compactHashes(values []string) (string, error) {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range values {
		if !utf8.ValidString(s) {
			return "", errors.New("invalid UTF-8 hash")
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		for _, r := range s {
			switch r {
			case '"', '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case '\b':
				b.WriteString("\\b")
			case '\f':
				b.WriteString("\\f")
			case '\n':
				b.WriteString("\\n")
			case '\r':
				b.WriteString("\\r")
			case '\t':
				b.WriteString("\\t")
			default:
				if r < 0x20 {
					const hex = "0123456789abcdef"
					b.WriteString("\\u00")
					b.WriteByte(hex[r>>4])
					b.WriteByte(hex[r&15])
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte('"')
	}
	b.WriteByte(']')
	return b.String(), nil
}

func pyString(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case json.Number:
		return x.String()
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func objectKeys(raw []byte) ([]string, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	token, e := d.Token()
	if e != nil {
		return nil, e
	}
	if token != json.Delim('{') {
		return nil, nil
	}
	keys := []string{}
	seen := map[string]bool{}
	for d.More() {
		token, e := d.Token()
		if e != nil {
			return nil, e
		}
		key := token.(string)
		var value json.RawMessage
		if e = d.Decode(&value); e != nil {
			return nil, e
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	_, e = d.Token()
	return keys, e
}

// encoding/json would replace a lone escaped UTF-16 surrogate with U+FFFD.
// The Python source retains it. Until a lossless Python string representation
// is implemented, reject that explicit unsupported input rather than claiming
// an exact match after replacement.
func hasUnpairedJSONSurrogate(raw []byte) bool {
	inside := false
	hex := func(b byte) (uint16, bool) {
		switch {
		case b >= '0' && b <= '9':
			return uint16(b - '0'), true
		case b >= 'a' && b <= 'f':
			return uint16(b - 'a' + 10), true
		case b >= 'A' && b <= 'F':
			return uint16(b - 'A' + 10), true
		}
		return 0, false
	}
	unit := func(pos int) (uint16, bool) {
		if pos+6 > len(raw) || raw[pos] != '\\' || raw[pos+1] != 'u' {
			return 0, false
		}
		var v uint16
		for _, b := range raw[pos+2 : pos+6] {
			h, ok := hex(b)
			if !ok {
				return 0, false
			}
			v = v*16 + h
		}
		return v, true
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inside = !inside
			continue
		}
		if !inside || raw[i] != '\\' {
			continue
		}
		v, ok := unit(i)
		if !ok {
			i++
			continue
		}
		if v >= 0xd800 && v <= 0xdbff {
			low, ok := unit(i + 6)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return true
			}
			i += 11
		} else if v >= 0xdc00 && v <= 0xdfff {
			return true
		} else {
			i += 5
		}
	}
	return false
}
