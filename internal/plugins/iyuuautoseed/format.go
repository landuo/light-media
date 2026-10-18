package iyuuautoseed

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"light-media/internal/plugins/larkmessager"
	"light-media/internal/releasemeta/legacyanime"
)

type jsonNumber = json.Number

const maxFormattedURL = 16 << 20

func sourceFormat(template string, fields map[string]any) (string, error) {
	return sourceFormatDepth(template, fields, 2)
}
func sourceFormatDepth(template string, fields map[string]any, depth int) (string, error) {
	if depth < 0 {
		return "", sourceFault("ValueError", "format recursion")
	}
	var out strings.Builder
	for i := 0; i < len(template); {
		if template[i] != '{' && template[i] != '}' {
			_, n := utf8.DecodeRuneInString(template[i:])
			out.WriteString(template[i : i+n])
			i += n
			continue
		}
		if i+1 < len(template) && template[i+1] == template[i] {
			out.WriteByte(template[i])
			i += 2
			continue
		}
		if template[i] == '}' {
			return "", sourceFault("ValueError", "single closing brace")
		}
		start := i + 1
		j := start
		nested := 0
		bracket := false
		for ; j < len(template); j++ {
			switch template[j] {
			case '[':
				bracket = true
			case ']':
				bracket = false
			case '{':
				if !bracket {
					nested++
				}
			case '}':
				if !bracket {
					if nested == 0 {
						goto found
					}
					nested--
				}
			}
		}
		return "", sourceFault("ValueError", "unclosed format field")
	found:
		field := template[start:j]
		name, conversion, spec, e := parseFormatField(field)
		if e != nil {
			return "", e
		}
		v, e := resolveFormatField(name, fields)
		if e != nil {
			return "", e
		}
		if conversion != "" {
			switch conversion {
			case "s":
				v, e = sourceString(v)
			case "r":
				v, e = sourceRepresentation(v, false)
			case "a":
				v, e = sourceRepresentation(v, true)
			default:
				e = sourceFault("ValueError", "unknown conversion")
			}
			if e != nil {
				return "", e
			}
		}
		expanded, e := sourceFormatDepth(spec, fields, depth-1)
		if e != nil {
			return "", e
		}
		text, e := formatScalar(v, expanded)
		if e != nil {
			return "", e
		}
		if out.Len() > maxFormattedURL-len(text) {
			return "", errors.New("format output exceeds bound")
		}
		out.WriteString(text)
		i = j + 1
	}
	return out.String(), nil
}
func parseFormatField(field string) (name, conversion, spec string, err error) {
	bracket := false
	split := len(field)
	for i, r := range field {
		if r == '[' {
			bracket = true
		} else if r == ']' {
			bracket = false
		} else if !bracket && (r == '!' || r == ':') {
			split = i
			break
		}
	}
	name = field[:split]
	if strings.ContainsAny(name, "{}") {
		return "", "", "", sourceFault("ValueError", "invalid field name")
	}
	if split == len(field) {
		return name, "", "", nil
	}
	if field[split] == ':' {
		return name, "", field[split+1:], nil
	}
	rest := field[split+1:]
	if rest == "" {
		return "", "", "", sourceFault("ValueError", "missing conversion")
	}
	r, n := utf8.DecodeRuneInString(rest)
	conversion = string(r)
	rest = rest[n:]
	if rest != "" {
		if rest[0] != ':' {
			return "", "", "", sourceFault("ValueError", "expected format specifier")
		}
		spec = rest[1:]
	}
	return
}
func decimalDigit(r rune) (int, bool) {
	n, e := legacyanime.Integer(string(r))
	if e != nil || n.Sign() < 0 || n.Cmp(big.NewInt(9)) > 0 {
		return 0, false
	}
	return int(n.Int64()), true
}
func decimalString(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if _, ok := decimalDigit(r); !ok {
			return false
		}
	}
	return true
}
func resolveFormatField(name string, fields map[string]any) (any, error) {
	end := len(name)
	for i, r := range name {
		if r == '.' || r == '[' {
			end = i
			break
		}
	}
	root := name[:end]
	if root == "" || decimalString(root) {
		return nil, sourceFault("IndexError", "positional format argument")
	}
	value, ok := fields[root]
	if !ok {
		return nil, sourceFault("KeyError", "format field")
	}
	for rest := name[end:]; rest != ""; {
		if rest[0] == '.' {
			rest = rest[1:]
			end = len(rest)
			for i, r := range rest {
				if r == '.' || r == '[' {
					end = i
					break
				}
			}
			if end == 0 {
				return nil, sourceFault("ValueError", "empty attribute")
			}
			var e error
			value, e = formatAttribute(value, rest[:end])
			if e != nil {
				return nil, e
			}
			rest = rest[end:]
			continue
		}
		if rest[0] != '[' {
			return nil, sourceFault("ValueError", "invalid field navigation")
		}
		end = strings.IndexByte(rest, ']')
		if end < 0 {
			return nil, sourceFault("ValueError", "missing closing bracket")
		}
		key := rest[1:end]
		if key == "" {
			return nil, sourceFault("ValueError", "empty element index")
		}
		var e error
		value, e = formatItem(value, key)
		if e != nil {
			return nil, e
		}
		rest = rest[end+1:]
	}
	return value, nil
}

type formatClass string

func formatType(v any) string {
	switch x := v.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case string:
		return "str"
	case jsonNumber:
		if strings.ContainsAny(string(x), ".eE") {
			return "float"
		}
		return "int"
	case []any:
		return "list"
	case map[string]any, *larkmessager.SourceObject:
		return "dict"
	case formatClass:
		return "type"
	}
	return "object"
}
func formatAttribute(v any, key string) (any, error) {
	if key == "__class__" {
		return formatClass(formatType(v)), nil
	}
	if c, ok := v.(formatClass); ok {
		switch key {
		case "__name__", "__qualname__":
			return string(c), nil
		case "__module__":
			return "builtins", nil
		}
	}
	if i, ok := formatInteger(v); ok {
		switch key {
		case "real", "numerator":
			return jsonNumber(i.String()), nil
		case "imag":
			return jsonNumber("0"), nil
		case "denominator":
			return jsonNumber("1"), nil
		}
	}
	if n, ok := v.(jsonNumber); ok && strings.ContainsAny(string(n), ".eE") {
		switch key {
		case "real":
			return n, nil
		case "imag":
			return jsonNumber("0.0"), nil
		}
	}
	return nil, sourceFault("AttributeError", "format attribute")
}
func formatItem(v any, key string) (any, error) {
	if decimalString(key) {
		n, e := legacyanime.Integer(key)
		if e != nil || !n.IsInt64() {
			return nil, sourceFault("IndexError", "format item")
		}
		i := n.Int64()
		switch x := v.(type) {
		case string:
			r := []rune(x)
			if i >= int64(len(r)) {
				return nil, sourceFault("IndexError", "format string item")
			}
			return string(r[i]), nil
		case []any:
			if i >= int64(len(x)) {
				return nil, sourceFault("IndexError", "format list item")
			}
			return x[i], nil
		}
		switch v.(type) {
		case map[string]any, *larkmessager.SourceObject:
			return nil, sourceFault("KeyError", "numeric format key")
		default:
			return nil, sourceFault("TypeError", "format object not subscriptable")
		}
	}
	switch x := v.(type) {
	case map[string]any:
		if value, ok := x[key]; ok {
			return value, nil
		}
	case *larkmessager.SourceObject:
		if value, ok := x.Values[key]; ok {
			return value, nil
		}
	default:
		return nil, sourceFault("TypeError", "format item index")
	}
	return nil, sourceFault("KeyError", "format mapping key")
}
func sourceString(v any) (string, error) {
	if c, ok := v.(formatClass); ok {
		return "<class '" + string(c) + "'>", nil
	}
	return larkmessager.PythonString(v)
}
func sourceRepresentation(v any, ascii bool) (string, error) {
	if _, ok := v.(formatClass); ok {
		return sourceString(v)
	}
	s, e := larkmessager.PythonString([]any{v})
	if e != nil {
		return "", e
	}
	s = s[1 : len(s)-1]
	if !ascii {
		return s, nil
	}
	var out strings.Builder
	for _, r := range s {
		switch {
		case r < 128:
			out.WriteRune(r)
		case r < 256:
			out.WriteString(`\x` + fixedHex(uint32(r), 2))
		case r <= 0xffff:
			out.WriteString(`\u` + fixedHex(uint32(r), 4))
		default:
			out.WriteString(`\U` + fixedHex(uint32(r), 8))
		}
	}
	return out.String(), nil
}
func fixedHex(n uint32, width int) string {
	s := strconv.FormatUint(uint64(n), 16)
	return strings.Repeat("0", width-len(s)) + s
}

type scalarSpec struct {
	fill                          rune
	align, sign, kind             rune
	negativeZero, alternate, zero bool
	width, precision              int
	hasPrecision                  bool
	group                         rune
	explicitAlign, explicitFill   bool
}

func parseScalarSpec(s string) (scalarSpec, error) {
	p := scalarSpec{fill: ' '}
	r := []rune(s)
	i := 0
	isAlign := func(r rune) bool { return strings.ContainsRune("<>=^", r) }
	if len(r) >= 2 && isAlign(r[1]) {
		p.fill = r[0]
		p.align = r[1]
		p.explicitAlign = true
		p.explicitFill = true
		i = 2
	} else if len(r) > 0 && isAlign(r[0]) {
		p.align = r[0]
		p.explicitAlign = true
		i = 1
	}
	if i < len(r) && strings.ContainsRune("+- ", r[i]) {
		p.sign = r[i]
		i++
	}
	if i < len(r) && r[i] == 'z' {
		p.negativeZero = true
		i++
	}
	if i < len(r) && r[i] == '#' {
		p.alternate = true
		i++
	}
	if i < len(r) && r[i] == '0' {
		p.zero = true
		i++
	}
	scan := func() (int, error) {
		n := 0
		for i < len(r) {
			d, ok := decimalDigit(r[i])
			if !ok {
				break
			}
			if n > maxFormattedURL/10 {
				return 0, errors.New("format width exceeds bound")
			}
			n = n*10 + d
			if n > maxFormattedURL {
				return 0, errors.New("format width exceeds bound")
			}
			i++
		}
		return n, nil
	}
	var e error
	p.width, e = scan()
	if e != nil {
		return p, e
	}
	if i < len(r) && (r[i] == ',' || r[i] == '_') {
		p.group = r[i]
		i++
	}
	if i < len(r) && r[i] == '.' {
		p.hasPrecision = true
		i++
		start := i
		p.precision, e = scan()
		if e != nil {
			return p, e
		}
		if i == start {
			return p, sourceFault("ValueError", "missing precision")
		}
	}
	if i < len(r) {
		p.kind = r[i]
		i++
	}
	if i != len(r) {
		return p, sourceFault("ValueError", "invalid format specifier")
	}
	return p, nil
}
func formatScalar(v any, spec string) (string, error) {
	if spec == "" {
		return sourceString(v)
	}
	switch v.(type) {
	case string, bool, jsonNumber:
	default:
		return "", sourceFault("TypeError", "unsupported object format specifier")
	}
	p, e := parseScalarSpec(spec)
	if e != nil {
		return "", e
	}
	switch x := v.(type) {
	case string:
		return formatString(x, p)
	case bool:
		n := int64(0)
		if x {
			n = 1
		}
		return formatInt(big.NewInt(n), p)
	case jsonNumber:
		if !strings.ContainsAny(string(x), ".eE") {
			n, ok := new(big.Int).SetString(string(x), 10)
			if !ok {
				return "", sourceFault("ValueError", "integer format")
			}
			return formatInt(n, p)
		}
		f, e := strconv.ParseFloat(string(x), 64)
		if e != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return "", errors.New("unsupported nonfinite format value")
		}
		return formatFloat(f, p)
	default:
		return "", sourceFault("TypeError", "unsupported object format specifier")
	}
}
func formatString(s string, p scalarSpec) (string, error) {
	if p.kind != 0 && p.kind != 's' || p.sign != 0 || p.alternate || p.negativeZero || p.group != 0 || p.align == '=' {
		return "", sourceFault("ValueError", "invalid string format specifier")
	}
	if p.hasPrecision {
		r := []rune(s)
		if len(r) > p.precision {
			s = string(r[:p.precision])
		}
	}
	if p.align == 0 {
		p.align = '<'
	}
	if p.zero && !p.explicitFill {
		p.fill = '0'
	}
	return padFormatted("", s, p), nil
}
func formatInteger(v any) (*big.Int, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	case jsonNumber:
		if !strings.ContainsAny(string(x), ".eE") {
			n, ok := new(big.Int).SetString(string(x), 10)
			return n, ok
		}
	}
	return nil, false
}
func formatInt(n *big.Int, p scalarSpec) (string, error) {
	if strings.ContainsRune("eEfFgG%", p.kind) && p.kind != 0 {
		f, _ := new(big.Float).SetInt(n).Float64()
		if math.IsInf(f, 0) {
			return "", sourceFault("OverflowError", "integer to float")
		}
		return formatFloat(f, p)
	}
	if p.negativeZero || p.hasPrecision {
		return "", sourceFault("ValueError", "invalid integer precision")
	}
	kind := p.kind
	if kind == 0 || kind == 'n' {
		kind = 'd'
	}
	base := 10
	prefix := ""
	switch kind {
	case 'd':
	case 'b':
		base = 2
		prefix = "0b"
	case 'o':
		base = 8
		prefix = "0o"
	case 'x':
		base = 16
		prefix = "0x"
	case 'X':
		base = 16
		prefix = "0X"
	case 'c':
		if !n.IsInt64() || n.Sign() < 0 || n.Int64() > utf8.MaxRune {
			return "", sourceFault("OverflowError", "character format")
		}
		if p.sign != 0 || p.alternate || p.group != 0 {
			return "", sourceFault("ValueError", "character format flags")
		}
		r := rune(n.Int64())
		if r >= 0xd800 && r <= 0xdfff {
			return "", errors.New("unsupported surrogate format character")
		}
		if p.zero && !p.explicitFill {
			p.fill = '0'
			if !p.explicitAlign {
				p.align = '='
			}
		}
		if p.align == 0 {
			p.align = '>'
		}
		return padFormatted("", string(r), p), nil
	default:
		return "", sourceFault("ValueError", "unknown integer format type")
	}
	if p.group == ',' && base != 10 || p.kind == 'n' && p.group != 0 {
		return "", sourceFault("ValueError", "invalid numeric grouping")
	}
	digits := new(big.Int).Abs(n).Text(base)
	if kind == 'X' {
		digits = strings.ToUpper(digits)
	}
	if !p.alternate {
		prefix = ""
	}
	sign := ""
	if n.Sign() < 0 {
		sign = "-"
	} else if p.sign == '+' {
		sign = "+"
	} else if p.sign == ' ' {
		sign = " "
	}
	prefix = sign + prefix
	if p.zero && !p.explicitFill {
		p.fill = '0'
		if !p.explicitAlign {
			p.align = '='
		}
	}
	if p.align == 0 {
		p.align = '>'
	}
	groupSize := 3
	if base != 10 {
		groupSize = 4
	}
	if p.group != 0 {
		if p.fill == '0' && p.align == '=' {
			for utf8.RuneCountInString(prefix)+len(groupDigits(digits, p.group, groupSize)) < p.width {
				digits = "0" + digits
			}
		}
		digits = groupDigits(digits, p.group, groupSize)
	}
	return padFormatted(prefix, digits, p), nil
}
func groupDigits(s string, sep rune, size int) string {
	if len(s) <= size {
		return s
	}
	first := len(s) % size
	if first == 0 {
		first = size
	}
	var b strings.Builder
	b.WriteString(s[:first])
	for i := first; i < len(s); i += size {
		b.WriteRune(sep)
		b.WriteString(s[i : i+size])
	}
	return b.String()
}
func padFormatted(prefix, body string, p scalarSpec) string {
	count := p.width - utf8.RuneCountInString(prefix) - utf8.RuneCountInString(body)
	if count <= 0 {
		return prefix + body
	}
	fill := strings.Repeat(string(p.fill), count)
	switch p.align {
	case '<':
		return prefix + body + fill
	case '^':
		left := count / 2
		return strings.Repeat(string(p.fill), left) + prefix + body + strings.Repeat(string(p.fill), count-left)
	case '=':
		return prefix + fill + body
	default:
		return fill + prefix + body
	}
}
func formatFloat(f float64, p scalarSpec) (string, error) {
	kind := p.kind
	if kind == 0 {
		kind = 'g'
	}
	if !strings.ContainsRune("eEfFgGn%", kind) {
		return "", sourceFault("ValueError", "unknown float format type")
	}
	if p.kind == 'n' && p.group != 0 {
		return "", sourceFault("ValueError", "invalid locale grouping")
	}
	precision := 6
	if p.hasPrecision {
		precision = p.precision
	}
	if kind == 'g' || kind == 'G' || kind == 'n' {
		if precision == 0 {
			precision = 1
		}
	}
	if kind == '%' {
		f *= 100
		kind = 'f'
	}
	formatKind := byte(kind)
	if kind == 'F' {
		formatKind = 'f'
	} else if kind == 'n' {
		formatKind = 'g'
	}
	text := ""
	if p.kind == 0 && !p.hasPrecision {
		text, _ = larkmessager.PythonString(jsonNumber(strconv.FormatFloat(f, 'e', -1, 64)))
	} else {
		text = strconv.FormatFloat(f, formatKind, precision, 64)
	}
	if p.kind == 0 && p.hasPrecision {
		scientific := strconv.FormatFloat(f, 'e', precision-1, 64)
		index := strings.LastIndexByte(scientific, 'e')
		exponent, _ := strconv.Atoi(scientific[index+1:])
		if exponent >= precision-1 {
			mantissa := scientific[:index]
			if strings.Contains(mantissa, ".") {
				mantissa = strings.TrimRight(strings.TrimRight(mantissa, "0"), ".")
			}
			text = mantissa + scientific[index:]
		}
	}
	if p.negativeZero && strings.HasPrefix(text, "-") {
		nonzero := false
		for _, r := range strings.SplitN(text, "e", 2)[0] {
			if r >= '1' && r <= '9' {
				nonzero = true
				break
			}
		}
		if !nonzero {
			text = strings.TrimPrefix(text, "-")
		}
	}
	prefix := ""
	if strings.HasPrefix(text, "-") {
		prefix = "-"
		text = text[1:]
	} else if p.sign == '+' {
		prefix = "+"
	} else if p.sign == ' ' {
		prefix = " "
	}
	if p.alternate {
		alternateKind := kind
		if p.kind == 0 && !p.hasPrecision {
			alternateKind = 'r'
		}
		text = alternateFloat(text, alternateKind, precision)
	}
	if p.kind == 0 && p.hasPrecision && !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	if p.kind == '%' {
		text += "%"
	}
	if p.kind == 'F' {
		text = strings.ToUpper(text)
	}
	if p.zero && !p.explicitFill {
		p.fill = '0'
		if !p.explicitAlign {
			p.align = '='
		}
	}
	if p.align == 0 {
		p.align = '>'
	}
	if p.group != 0 {
		split := len(text)
		for i, r := range text {
			if r == '.' || r == 'e' || r == 'E' || r == '%' {
				split = i
				break
			}
		}
		integer, rest := text[:split], text[split:]
		if p.fill == '0' && p.align == '=' {
			for len(prefix)+len(groupDigits(integer, p.group, 3))+len(rest) < p.width {
				integer = "0" + integer
			}
		}
		text = groupDigits(integer, p.group, 3) + rest
	}
	return padFormatted(prefix, text, p), nil
}
func alternateFloat(s string, kind rune, precision int) string {
	pos := strings.IndexAny(s, "eE")
	mantissa, exponent := s, ""
	if pos >= 0 {
		mantissa, exponent = s[:pos], s[pos:]
	}
	if !strings.Contains(mantissa, ".") {
		mantissa += "."
	}
	if kind == 'g' || kind == 'G' || kind == 'n' {
		digits := 0
		started := false
		for _, r := range mantissa {
			if r == '.' {
				continue
			}
			if r != '0' {
				started = true
			}
			if started {
				digits++
			}
		}
		if !started {
			digits = 1
		}
		if digits < precision {
			mantissa += strings.Repeat("0", precision-digits)
		}
	}
	return mantissa + exponent
}
