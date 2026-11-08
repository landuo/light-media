package app

// This finite leaf implements only MTeam's str(raw_token or "").strip(). It
// does not evaluate code, expose a Python runtime, or parse a general workflow.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func sourceSiteTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
}

type sourceSiteTokenValue struct {
	kind  byte
	raw   json.RawMessage
	text  []rune
	items []sourceSiteTokenValue
	pairs []sourceSiteTokenPair
}
type sourceSiteTokenPair struct {
	key   []rune
	value sourceSiteTokenValue
}

func sourceSiteJSONCodepoints(raw []byte) ([]rune, error) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return nil, fmt.Errorf("source string shape")
	}
	var out []rune
	for i := 1; i < len(raw)-1; {
		if raw[i] != '\\' {
			r, n := utf8.DecodeRune(raw[i:])
			out = append(out, r)
			i += n
			continue
		}
		i++
		if i >= len(raw)-1 {
			return nil, fmt.Errorf("source string escape")
		}
		c := raw[i]
		i++
		switch c {
		case '"', '\\', '/':
			out = append(out, rune(c))
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			if i+4 > len(raw)-1 {
				return nil, fmt.Errorf("source Unicode escape")
			}
			v, e := strconv.ParseUint(string(raw[i:i+4]), 16, 16)
			if e != nil {
				return nil, e
			}
			i += 4
			r := rune(v)
			if r >= 0xd800 && r <= 0xdbff && i+6 <= len(raw)-1 && raw[i] == '\\' && raw[i+1] == 'u' {
				w, e := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
				if e == nil && w >= 0xdc00 && w <= 0xdfff {
					r = 0x10000 + (r-0xd800)*0x400 + rune(w) - 0xdc00
					i += 6
				}
			}
			out = append(out, r)
		default:
			return nil, fmt.Errorf("source string escape")
		}
	}
	return out, nil
}
func sourceSiteParseToken(raw json.RawMessage) (sourceSiteTokenValue, error) {
	raw = bytes.TrimSpace(raw)
	out := sourceSiteTokenValue{raw: raw}
	if len(raw) == 0 || !json.Valid(raw) {
		return out, fmt.Errorf("source token JSON")
	}
	out.kind = raw[0]
	switch out.kind {
	case '"':
		var e error
		out.text, e = sourceSiteJSONCodepoints(raw)
		return out, e
	case '[':
		var values []json.RawMessage
		if e := json.Unmarshal(raw, &values); e != nil {
			return out, e
		}
		for _, v := range values {
			x, e := sourceSiteParseToken(v)
			if e != nil {
				return out, e
			}
			out.items = append(out.items, x)
		}
	case '{':
		d := json.NewDecoder(bytes.NewReader(raw))
		if _, e := d.Token(); e != nil {
			return out, e
		}
		for d.More() {
			start := d.InputOffset()
			if _, e := d.Token(); e != nil {
				return out, e
			}
			keyRaw := bytes.TrimLeft(raw[start:d.InputOffset()], ", \t\r\n")
			key, e := sourceSiteJSONCodepoints(keyRaw)
			if e != nil {
				return out, e
			}
			var value json.RawMessage
			if e = d.Decode(&value); e != nil {
				return out, e
			}
			x, e := sourceSiteParseToken(value)
			if e != nil {
				return out, e
			}
			at := -1
			for i, p := range out.pairs {
				if slices.Equal(p.key, key) {
					at = i
					break
				}
			}
			if at < 0 {
				out.pairs = append(out.pairs, sourceSiteTokenPair{key, x})
			} else {
				out.pairs[at].value = x
			}
		}
	}
	return out, nil
}
func sourceSiteCodepointString(points []rune) string {
	var b strings.Builder
	for _, r := range points {
		if r >= 0xd800 && r <= 0xdfff {
			b.WriteByte(byte(0xe0 | r>>12))
			b.WriteByte(byte(0x80 | (r>>6)&63))
			b.WriteByte(byte(0x80 | r&63))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func sourceSiteTokenQuote(points []rune) string {
	q := rune('\'')
	if slices.Contains(points, '\'') && !slices.Contains(points, '"') {
		q = '"'
	}
	var b strings.Builder
	b.WriteRune(q)
	for _, r := range points {
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
			if r == q {
				b.WriteByte('\\')
				b.WriteRune(r)
			} else if unicode.IsPrint(r) {
				b.WriteRune(r)
			} else if r <= 255 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else if r <= 65535 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				fmt.Fprintf(&b, `\U%08x`, r)
			}
		}
	}
	b.WriteRune(q)
	return b.String()
}
func sourceSiteTokenNumber(raw json.RawMessage) (string, error) {
	text := string(raw)
	if !strings.ContainsAny(text, ".eE") {
		v, ok := new(big.Int).SetString(text, 10)
		if !ok {
			return "", fmt.Errorf("source integer")
		}
		return v.String(), nil
	}
	v, e := strconv.ParseFloat(text, 64)
	if e != nil && !math.IsInf(v, 0) {
		return "", e
	}
	if math.IsInf(v, 1) {
		return "inf", nil
	}
	if math.IsInf(v, -1) {
		return "-inf", nil
	}
	if math.IsNaN(v) {
		return "nan", nil
	}
	absolute := math.Abs(v)
	if absolute != 0 && (absolute < 1e-4 || absolute >= 1e16) {
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	}
	out := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.ContainsRune(out, '.') {
		out += ".0"
	}
	return out, nil
}
func (v sourceSiteTokenValue) repr() (string, error) {
	switch v.kind {
	case '"':
		return sourceSiteTokenQuote(v.text), nil
	case 't':
		return "True", nil
	case 'f':
		return "False", nil
	case 'n':
		return "None", nil
	case '[':
		parts := []string{}
		for _, item := range v.items {
			s, e := item.repr()
			if e != nil {
				return "", e
			}
			parts = append(parts, s)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case '{':
		parts := []string{}
		for _, p := range v.pairs {
			s, e := p.value.repr()
			if e != nil {
				return "", e
			}
			parts = append(parts, sourceSiteTokenQuote(p.key)+": "+s)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	default:
		return sourceSiteTokenNumber(v.raw)
	}
}
func originalSourceSiteMTeamToken(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	value, e := sourceSiteParseToken(raw)
	if e != nil {
		return "", e
	}
	truth := true
	switch value.kind {
	case 'n', 'f':
		truth = false
	case '"':
		truth = len(value.text) != 0
	case '[':
		truth = len(value.items) != 0
	case '{':
		truth = len(value.pairs) != 0
	case 't':
	default:
		if strings.ContainsAny(string(value.raw), ".eE") {
			number, _ := strconv.ParseFloat(string(value.raw), 64)
			truth = number != 0
		} else {
			number, ok := new(big.Int).SetString(string(value.raw), 10)
			truth = ok && number.Sign() != 0
		}
	}
	if !truth {
		return "", nil
	}
	if value.kind == '"' {
		return sourceSiteTrim(sourceSiteCodepointString(value.text)), nil
	}
	text, e := value.repr()
	if e != nil {
		return "", e
	}
	return sourceSiteTrim(text), nil
}
