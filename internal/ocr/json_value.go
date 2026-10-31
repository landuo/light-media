package ocr

import (
	"light-media/internal/plugins"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// The source json decoder permits NaN/Infinity and lone escaped surrogates;
// encoding/json into interface{} would lose those and large integer values.
type jsonParser struct {
	raw       []byte
	at, depth int
}

func jsonError() error { return &SourceError{Class: "JSONDecodeError"} }
func parseSourceJSON(raw []byte) (plugins.OCRValue, error) {
	p := jsonParser{raw: raw}
	value, err := p.value()
	if err != nil {
		return plugins.OCRValue{}, err
	}
	p.space()
	if p.at != len(raw) {
		return plugins.OCRValue{}, jsonError()
	}
	return value, nil
}
func (p *jsonParser) space() {
	for p.at < len(p.raw) {
		switch p.raw[p.at] {
		case ' ', '\r', '\n', '\t':
			p.at++
		default:
			return
		}
	}
}
func (p *jsonParser) take(token string) bool {
	if strings.HasPrefix(string(p.raw[p.at:]), token) {
		p.at += len(token)
		return true
	}
	return false
}
func (p *jsonParser) value() (plugins.OCRValue, error) {
	p.space()
	if p.at >= len(p.raw) {
		return plugins.OCRValue{}, jsonError()
	}
	switch p.raw[p.at] {
	case '"':
		v, err := p.stringValue()
		return plugins.OCRValue{Kind: "string", Points: v}, err
	case 'n':
		if p.take("null") {
			return plugins.OCRValue{Kind: "null"}, nil
		}
	case 't':
		if p.take("true") {
			return plugins.OCRValue{Kind: "bool", Bool: true}, nil
		}
	case 'f':
		if p.take("false") {
			return plugins.OCRValue{Kind: "bool"}, nil
		}
	case 'N':
		if p.take("NaN") {
			return plugins.OCRValue{Kind: "float", Number: "nan"}, nil
		}
	case 'I':
		if p.take("Infinity") {
			return plugins.OCRValue{Kind: "float", Number: "+inf"}, nil
		}
	case '[', '{':
		return p.collection()
	default:
		if p.take("-Infinity") {
			return plugins.OCRValue{Kind: "float", Number: "-inf"}, nil
		}
		if p.raw[p.at] == '-' || p.raw[p.at] >= '0' && p.raw[p.at] <= '9' {
			return p.number()
		}
	}
	return plugins.OCRValue{}, jsonError()
}
func (p *jsonParser) hex4(at int) (rune, bool) {
	if at+4 > len(p.raw) {
		return 0, false
	}
	var value rune
	for _, b := range p.raw[at : at+4] {
		value *= 16
		switch {
		case b >= '0' && b <= '9':
			value += rune(b - '0')
		case b >= 'a' && b <= 'f':
			value += rune(b - 'a' + 10)
		case b >= 'A' && b <= 'F':
			value += rune(b - 'A' + 10)
		default:
			return 0, false
		}
	}
	return value, true
}
func (p *jsonParser) stringValue() ([]rune, error) {
	if p.at >= len(p.raw) || p.raw[p.at] != '"' {
		return nil, jsonError()
	}
	p.at++
	out := []rune{}
	for p.at < len(p.raw) {
		b := p.raw[p.at]
		p.at++
		if b == '"' {
			return out, nil
		}
		if b < 0x20 {
			return nil, jsonError()
		}
		if b != '\\' {
			p.at--
			point, size := utf8.DecodeRune(p.raw[p.at:])
			out = append(out, point)
			p.at += size
			continue
		}
		if p.at >= len(p.raw) {
			return nil, jsonError()
		}
		escape := p.raw[p.at]
		p.at++
		switch escape {
		case '"', '\\', '/':
			out = append(out, rune(escape))
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
			point, ok := p.hex4(p.at)
			if !ok {
				return nil, jsonError()
			}
			p.at += 4
			if point >= 0xd800 && point <= 0xdbff && p.at+6 <= len(p.raw) && p.raw[p.at] == '\\' && p.raw[p.at+1] == 'u' {
				low, valid := p.hex4(p.at + 2)
				if valid && low >= 0xdc00 && low <= 0xdfff {
					point = utf16.DecodeRune(point, low)
					p.at += 6
				}
			}
			out = append(out, point)
		default:
			return nil, jsonError()
		}
	}
	return nil, jsonError()
}
func (p *jsonParser) number() (plugins.OCRValue, error) {
	start := p.at
	if p.raw[p.at] == '-' {
		p.at++
	}
	if p.at >= len(p.raw) {
		return plugins.OCRValue{}, jsonError()
	}
	if p.raw[p.at] == '0' {
		p.at++
	} else {
		if p.raw[p.at] < '1' || p.raw[p.at] > '9' {
			return plugins.OCRValue{}, jsonError()
		}
		for p.at < len(p.raw) && p.raw[p.at] >= '0' && p.raw[p.at] <= '9' {
			p.at++
		}
	}
	floating := false
	if p.at < len(p.raw) && p.raw[p.at] == '.' {
		floating = true
		p.at++
		begin := p.at
		for p.at < len(p.raw) && p.raw[p.at] >= '0' && p.raw[p.at] <= '9' {
			p.at++
		}
		if begin == p.at {
			return plugins.OCRValue{}, jsonError()
		}
	}
	if p.at < len(p.raw) && (p.raw[p.at] == 'e' || p.raw[p.at] == 'E') {
		floating = true
		p.at++
		if p.at < len(p.raw) && (p.raw[p.at] == '+' || p.raw[p.at] == '-') {
			p.at++
		}
		begin := p.at
		for p.at < len(p.raw) && p.raw[p.at] >= '0' && p.raw[p.at] <= '9' {
			p.at++
		}
		if begin == p.at {
			return plugins.OCRValue{}, jsonError()
		}
	}
	token := string(p.raw[start:p.at])
	if !floating {
		digits := len(strings.TrimPrefix(token, "-"))
		if digits > 4300 {
			return plugins.OCRValue{}, &SourceError{Class: "ValueError"}
		}
		n, ok := new(big.Int).SetString(token, 10)
		if !ok {
			return plugins.OCRValue{}, jsonError()
		}
		return plugins.OCRValue{Kind: "int", Number: n.String()}, nil
	}
	n, err := strconv.ParseFloat(token, 64)
	if math.IsInf(n, 1) {
		return plugins.OCRValue{Kind: "float", Number: "+inf"}, nil
	}
	if math.IsInf(n, -1) {
		return plugins.OCRValue{Kind: "float", Number: "-inf"}, nil
	}
	if err != nil {
		return plugins.OCRValue{}, jsonError()
	}
	return plugins.OCRValue{Kind: "float", Number: strconv.FormatFloat(n, 'g', -1, 64)}, nil
}
func keyID(points []rune) string {
	var out strings.Builder
	for _, r := range points {
		out.WriteString(strconv.FormatInt(int64(r), 16))
		out.WriteByte(',')
	}
	return out.String()
}
func (p *jsonParser) collection() (plugins.OCRValue, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth >= 1000 {
		return plugins.OCRValue{}, &SourceError{Class: "RecursionError"}
	}
	open := p.raw[p.at]
	p.at++
	close := byte(']')
	out := plugins.OCRValue{Kind: "list", Items: []plugins.OCRValue{}}
	if open == '{' {
		close = '}'
		out = plugins.OCRValue{Kind: "object", Entries: []plugins.OCREntry{}}
	}
	p.space()
	if p.at < len(p.raw) && p.raw[p.at] == close {
		p.at++
		return out, nil
	}
	positions := map[string]int{}
	for {
		var key []rune
		if open == '{' {
			p.space()
			var err error
			key, err = p.stringValue()
			if err != nil {
				return plugins.OCRValue{}, err
			}
			p.space()
			if p.at >= len(p.raw) || p.raw[p.at] != ':' {
				return plugins.OCRValue{}, jsonError()
			}
			p.at++
		}
		value, err := p.value()
		if err != nil {
			return plugins.OCRValue{}, err
		}
		if open == '{' {
			id := keyID(key)
			if i, ok := positions[id]; ok {
				out.Entries[i].Value = value
			} else {
				positions[id] = len(out.Entries)
				out.Entries = append(out.Entries, plugins.OCREntry{Key: key, Value: value})
			}
		} else {
			out.Items = append(out.Items, value)
		}
		p.space()
		if p.at >= len(p.raw) {
			return plugins.OCRValue{}, jsonError()
		}
		next := p.raw[p.at]
		p.at++
		if next == close {
			return out, nil
		}
		if next != ',' {
			return plugins.OCRValue{}, jsonError()
		}
	}
}
