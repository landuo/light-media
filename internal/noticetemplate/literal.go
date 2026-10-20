package noticetemplate

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"light-media/internal/jinja/exec"
)

type literalParser struct {
	s          string
	pos, depth int
}

func literalError() error { return SourceError{"ValueError"} }
func parseLiteral(s string) (any, error) {
	p := literalParser{s: s}
	value, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(s) {
		return nil, literalError()
	}
	return value, nil
}
func (p *literalParser) space() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\n', '\r', '\f':
			p.pos++
		case '#':
			for p.pos < len(p.s) && p.s[p.pos] != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}
func (p *literalParser) take(s string) bool {
	p.space()
	if strings.HasPrefix(p.s[p.pos:], s) {
		p.pos += len(s)
		return true
	}
	return false
}
func (p *literalParser) value() (any, error) {
	p.space()
	if p.pos >= len(p.s) || p.depth >= 200 {
		return nil, literalError()
	}
	p.depth++
	defer func() { p.depth-- }()
	if p.s[p.pos] == '{' {
		return p.braces()
	}
	if p.s[p.pos] == '[' || p.s[p.pos] == '(' {
		open := p.s[p.pos]
		p.pos++
		end := "]"
		if open == '(' {
			end = ")"
		}
		out := []any{}
		if p.take(end) {
			if open == '(' {
				return exec.Tuple(out), nil
			}
			return out, nil
		}
		trailing := false
		for {
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			if p.take(end) {
				if open == '(' && !trailing && len(out) == 1 {
					return out[0], nil
				}
				if open == '(' {
					return exec.Tuple(out), nil
				}
				return out, nil
			}
			if !p.take(",") {
				return nil, literalError()
			}
			trailing = true
			if p.take(end) {
				if open == '(' {
					return exec.Tuple(out), nil
				}
				return out, nil
			}
		}
	}
	if p.stringStart() {
		bytesKind := p.bytesStart()
		var b strings.Builder
		for p.stringStart() {
			if p.bytesStart() != bytesKind {
				return nil, literalError()
			}
			s, err := p.string()
			if err != nil {
				return nil, err
			}
			b.WriteString(s)
			p.space()
		}
		if bytesKind {
			return literalBytes{b.String()}, nil
		}
		return b.String(), nil
	}
	if strings.HasPrefix(p.s[p.pos:], "set") && (p.pos+3 == len(p.s) || !isIdent(p.s[p.pos+3])) {
		p.pos += 3
		if !p.take("(") || !p.take(")") {
			return nil, literalError()
		}
		return literalSet{}, nil
	}
	for _, kw := range []struct {
		name  string
		value any
	}{{"True", true}, {"False", false}, {"None", nil}} {
		if strings.HasPrefix(p.s[p.pos:], kw.name) {
			end := p.pos + len(kw.name)
			if end == len(p.s) || !isIdent(p.s[end]) {
				p.pos = end
				return kw.value, nil
			}
		}
	}
	start := p.pos
	if p.s[p.pos] == '+' || p.s[p.pos] == '-' {
		p.pos++
		p.space()
	}
	digits := p.pos
	for p.pos < len(p.s) && strings.ContainsRune("0123456789abcdefABCDEFxXoObB._eE+-", rune(p.s[p.pos])) {
		c := p.s[p.pos]
		if (c == '+' || c == '-') && p.pos > digits && p.s[p.pos-1] != 'e' && p.s[p.pos-1] != 'E' {
			break
		}
		p.pos++
	}
	raw := strings.ReplaceAll(p.s[start:p.pos], " ", "")
	raw = strings.ReplaceAll(raw, "\t", "")
	if p.pos == digits || raw == "" {
		return nil, literalError()
	}
	if strings.ContainsAny(raw, ".eE") && !strings.ContainsAny(raw, "xX") {
		if strings.HasPrefix(raw, "_") || strings.HasSuffix(raw, "_") || strings.Contains(raw, "__") {
			return nil, literalError()
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil && !strings.Contains(err.Error(), "value out of range") {
			return nil, literalError()
		}
		return f, nil
	}
	clean := strings.ReplaceAll(raw, "_", "")
	unsigned := strings.TrimLeft(clean, "+-")
	base := 10
	if strings.HasPrefix(strings.ToLower(unsigned), "0x") {
		base = 0
	} else if strings.HasPrefix(strings.ToLower(unsigned), "0o") || strings.HasPrefix(strings.ToLower(unsigned), "0b") {
		base = 0
	} else if len(unsigned) > 1 && unsigned[0] == '0' && strings.Trim(unsigned, "0") != "" {
		return nil, literalError()
	}
	if strings.HasPrefix(raw, "_") || strings.HasSuffix(raw, "_") || strings.Contains(raw, "__") {
		return nil, literalError()
	}
	n, ok := new(big.Int).SetString(clean, base)
	if !ok {
		return nil, literalError()
	}
	return n, nil
}
func isIdent(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}
func (p *literalParser) stringStart() bool {
	p.space()
	i := p.pos
	for i < len(p.s) && i-p.pos < 2 && strings.ContainsRune("rRuUbBfF", rune(p.s[i])) {
		i++
	}
	return i < len(p.s) && (p.s[i] == '\'' || p.s[i] == '"')
}
func (p *literalParser) string() (string, error) {
	start := p.pos
	for p.pos < len(p.s) && p.s[p.pos] != '\'' && p.s[p.pos] != '"' {
		p.pos++
	}
	prefix := strings.ToLower(p.s[start:p.pos])
	if prefix != "" && prefix != "r" && prefix != "u" && prefix != "b" && prefix != "rb" && prefix != "br" {
		return "", literalError()
	}
	raw := strings.Contains(prefix, "r")
	byteLiteral := strings.Contains(prefix, "b")
	quote := p.s[p.pos]
	marker := string(quote)
	if strings.HasPrefix(p.s[p.pos:], strings.Repeat(marker, 3)) {
		marker = strings.Repeat(marker, 3)
	}
	p.pos += len(marker)
	var b strings.Builder
	for p.pos < len(p.s) {
		if strings.HasPrefix(p.s[p.pos:], marker) {
			p.pos += len(marker)
			return b.String(), nil
		}
		c := p.s[p.pos]
		p.pos++
		if byteLiteral && c >= 128 {
			return "", literalError()
		}
		if len(marker) == 1 && (c == '\n' || c == '\r') {
			return "", literalError()
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if p.pos >= len(p.s) {
			return "", literalError()
		}
		c = p.s[p.pos]
		p.pos++
		if byteLiteral && (c == 'u' || c == 'U' || c == 'N') {
			b.WriteByte('\\')
			b.WriteByte(c)
			continue
		}
		if raw {
			b.WriteByte('\\')
			b.WriteByte(c)
			continue
		}
		switch c {
		case '\\', '\'', '"':
			b.WriteByte(c)
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case '\n':
		case '\r':
			if p.pos < len(p.s) && p.s[p.pos] == '\n' {
				p.pos++
			}
		case 'N':
			return "", fmt.Errorf("%w: Unicode named escape", exec.ErrUnsupported)
		case 'x', 'u', 'U':
			count := 2
			if c == 'u' {
				count = 4
			}
			if c == 'U' {
				count = 8
			}
			if p.pos+count > len(p.s) {
				return "", literalError()
			}
			n, err := strconv.ParseUint(p.s[p.pos:p.pos+count], 16, 32)
			if err != nil || n > utf8.MaxRune {
				return "", literalError()
			}
			p.pos += count
			if n >= 0xd800 && n <= 0xdfff {
				return "", fmt.Errorf("%w: lone surrogate", exec.ErrUnsupported)
			}
			b.WriteRune(rune(n))
		default:
			if c >= '0' && c <= '7' {
				digits := string(c)
				for len(digits) < 3 && p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '7' {
					digits += string(p.s[p.pos])
					p.pos++
				}
				n, _ := strconv.ParseUint(digits, 8, 16)
				b.WriteRune(rune(n))
			} else {
				b.WriteByte('\\')
				b.WriteByte(c)
			}
		}
	}
	return "", literalError()
}
func literalHashable(v any) bool {
	switch v.(type) {
	case nil, string, bool, *big.Int, float64, literalBytes:
		return true
	case exec.Tuple:
		for _, x := range v.(exec.Tuple) {
			if !literalHashable(x) {
				return false
			}
		}
		return true
	}
	return false
}
func literalKeyEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	sa, aString := a.(string)
	sb, bString := b.(string)
	if aString || bString {
		return aString && bString && sa == sb
	}
	toRat := func(v any) *big.Rat {
		switch n := v.(type) {
		case bool:
			if n {
				return new(big.Rat).SetInt64(1)
			}
			return new(big.Rat)
		case *big.Int:
			return new(big.Rat).SetInt(n)
		case float64:
			return new(big.Rat).SetFloat64(n)
		}
		return nil
	}
	ra, rb := toRat(a), toRat(b)
	if ra != nil && rb != nil {
		return ra.Cmp(rb) == 0
	}
	ta, okA := a.(exec.Tuple)
	tb, okB := b.(exec.Tuple)
	if okA && okB && len(ta) == len(tb) {
		for i := range ta {
			if !literalKeyEqual(ta[i], tb[i]) {
				return false
			}
		}
		return true
	}
	return false
}
