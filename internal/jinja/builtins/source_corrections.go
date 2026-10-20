package builtins

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"light-media/internal/jinja/exec"
)

// These changes follow Jinja2 3.1.6, rather than permissive Go conversions.
func init() {
	for _, name := range []string{"length", "list", "sort", "reverse", "first", "last", "join", "batch", "slice", "select", "reject", "map", "selectattr", "rejectattr"} {
		old, ok := Filters.Get(name)
		if !ok {
			panic(name)
		}
		if err := Filters.Replace(name, func(e *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
			if in.IsError() {
				return in
			}
			if (name == "select" || name == "reject" || name == "map" || name == "selectattr" || name == "rejectattr") && !in.IsTrue() {
				return exec.AsValue([]any{})
			}
			if !in.IsUndefined() && !in.IsIterable() {
				if name == "reverse" {
					return exec.ValueError(exec.SourceFailuref("FilterArgumentError: argument must be iterable"))
				}
				return exec.ValueError(exec.SourceFailuref("TypeError: object is not iterable"))
			}
			return old(e, in, p)
		}); err != nil {
			panic(err)
		}
	}
	for _, name := range []string{"upper", "lower", "capitalize"} {
		if err := Filters.Replace(name, func(_ *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
			if in.IsError() {
				return in
			}
			if err := p.Take(); err != nil {
				return exec.ValueError(err)
			}
			return exec.AsValue(exec.PythonCase(in.String(), name))
		}); err != nil {
			panic(err)
		}
	}
	if err := Filters.Replace("title", sourceTitle); err != nil {
		panic(err)
	}
	if err := Filters.Replace("trim", sourceTrim); err != nil {
		panic(err)
	}
	if err := Filters.Replace("round", sourceRound); err != nil {
		panic(err)
	}
	if err := Filters.Replace("format", sourceFormat); err != nil {
		panic(err)
	}
	if err := Filters.Replace("tojson", sourceJSON); err != nil {
		panic(err)
	}
	oldURL, _ := Filters.Get("urlencode")
	if err := Filters.Replace("urlencode", func(e *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
		if in.IsString() {
			if err := p.Take(); err != nil {
				return exec.ValueError(err)
			}
			s := url.QueryEscape(in.String())
			s = strings.ReplaceAll(s, "+", "%20")
			s = strings.ReplaceAll(s, "%2F", "/")
			return exec.AsValue(s)
		}
		if !in.IsIterable() {
			if err := p.Take(); err != nil {
				return exec.ValueError(err)
			}
			s := url.QueryEscape(in.String())
			return exec.AsValue(strings.ReplaceAll(strings.ReplaceAll(s, "+", "%20"), "%2F", "/"))
		}
		if in.IsList() {
			valid := true
			wrongLength := false
			in.Iterate(func(_, _ int, v, _ *exec.Value) bool {
				if !v.IsIterable() || v.Len() != 2 {
					valid = false
					wrongLength = v.IsIterable()
				}
				return valid
			}, func() {})
			if !valid {
				if wrongLength {
					return exec.ValueError(exec.SourceFailuref("ValueError: invalid URL pair length"))
				}
				return exec.ValueError(exec.SourceFailuref("TypeError: cannot unpack URL pairs"))
			}
		}
		return oldURL(e, in, p)
	}); err != nil {
		panic(err)
	}
	for _, name := range []string{"int", "float"} {
		old, _ := Filters.Get(name)
		if err := Filters.Replace(name, func(e *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
			if in.IsBool() {
				n := 0
				if in.Bool() {
					n = 1
				}
				in = exec.AsValue(n)
			}
			if in.IsString() {
				in = exec.AsValue(exec.PythonDecimalASCII(in.String()))
			}
			return old(e, in, p)
		}); err != nil {
			panic(err)
		}
	}
}
func sourceTitle(_ *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
	if in.IsError() {
		return in
	}
	if err := p.Take(); err != nil {
		return exec.ValueError(err)
	}
	// Jinja's title filter splits words at its own [-{}\s(]+ boundary.
	s := in.String()
	var b strings.Builder
	var word strings.Builder
	flush := func() {
		r := []rune(word.String())
		if len(r) > 0 {
			b.WriteString(exec.PythonCase(string(r[:1]), "upper"))
			b.WriteString(exec.PythonCase(string(r[1:]), "lower"))
		}
		word.Reset()
	}
	for _, r := range s {
		if strings.ContainsRune("-{}([<", r) || unicode.IsSpace(r) {
			flush()
			b.WriteRune(r)
		} else {
			word.WriteRune(r)
		}
	}
	flush()
	return exec.AsValue(b.String())
}
func sourceTrim(_ *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
	if in.IsError() {
		return in
	}
	var chars *exec.Value
	if err := p.Take(exec.KeywordArgument("chars", exec.AsValue(nil), takeValueArgument(&chars))); err != nil {
		return exec.ValueError(err)
	}
	if chars.IsNil() {
		return exec.AsValue(strings.TrimSpace(in.String()))
	}
	if !chars.IsString() {
		return exec.ValueError(exec.SourceFailuref("TypeError: strip argument must be str or None"))
	}
	return exec.AsValue(strings.Trim(in.String(), chars.String()))
}
func sourceRound(_ *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
	if in.IsError() {
		return in
	}
	var precision int
	var method string
	if err := p.Take(exec.KeywordArgument("precision", exec.AsValue(0), takeIntArgument(&precision)), exec.KeywordArgument("method", exec.AsValue("common"), takeStringArgument(&method))); err != nil {
		return exec.ValueError(err)
	}
	if method != "common" && method != "ceil" && method != "floor" {
		return exec.ValueError(exec.SourceFailuref("FilterArgumentError: method must be common, ceil or floor"))
	}
	if !in.IsNumber() && !in.IsBool() {
		return exec.ValueError(exec.SourceFailuref("TypeError: value does not define __round__"))
	}
	if in.IsBool() {
		n := 0
		if in.Bool() {
			n = 1
		}
		in = exec.AsValue(n)
	}
	if method == "common" && in.IsInteger() && precision >= 0 {
		return in
	}
	f := in.Float()
	if method == "common" {
		if precision >= 0 && precision < 324 && !math.IsInf(f, 0) && !math.IsNaN(f) {
			rounded, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'f', precision, 64), 64)
			f = rounded
		} else if precision < 0 && precision > -309 {
			power := math.Pow10(-precision)
			f = math.RoundToEven(f/power) * power
		}
		if in.IsInteger() {
			return exec.AsValue(int(f))
		}
		return exec.AsValue(f)
	}
	power := math.Pow10(precision)
	if method == "ceil" {
		f = math.Ceil(f*power) / power
	} else {
		f = math.Floor(f*power) / power
	}
	return exec.AsValue(f)
}
func sourceFormat(_ *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
	if in.IsError() {
		return in
	}
	if len(p.KwArgs) > 0 {
		return exec.ValueError(exec.SourceFailuref("native dependency: mapping percent formatting"))
	}
	format := in.String()
	args := []any{}
	index := 0
	for pos := 0; pos < len(format); pos++ {
		if format[pos] != '%' {
			continue
		}
		pos++
		if pos >= len(format) {
			return exec.ValueError(exec.SourceFailuref("ValueError: incomplete format"))
		}
		if format[pos] == '%' {
			continue
		}
		for pos < len(format) && strings.ContainsRune("#0- +0123456789.*hlL", rune(format[pos])) {
			pos++
		}
		if pos >= len(format) {
			return exec.ValueError(exec.SourceFailuref("ValueError: incomplete format"))
		}
		if index >= len(p.Args) {
			return exec.ValueError(exec.SourceFailuref("TypeError: not enough arguments"))
		}
		v := p.Args[index]
		index++
		switch format[pos] {
		case 'f', 'F', 'e', 'E', 'g', 'G':
			if !v.IsNumber() && !v.IsBool() {
				return exec.ValueError(exec.SourceFailuref("TypeError: float argument required"))
			}
			if v.IsBool() {
				n := 0.0
				if v.Bool() {
					n = 1
				}
				args = append(args, n)
			} else {
				args = append(args, v.Float())
			}
		case 'd', 'i', 'u', 'o', 'x', 'X':
			if !v.IsNumber() && !v.IsBool() {
				return exec.ValueError(exec.SourceFailuref("TypeError: number argument required"))
			}
			if v.IsBool() {
				n := 0
				if v.Bool() {
					n = 1
				}
				args = append(args, n)
			} else {
				args = append(args, v.Integer())
			}
		case 's':
			args = append(args, v.String())
		default:
			return exec.ValueError(exec.SourceFailuref("native dependency: percent format %c", format[pos]))
		}
	}
	if index != len(p.Args) {
		return exec.ValueError(exec.SourceFailuref("TypeError: not all arguments converted"))
	}
	return exec.AsValue(fmt.Sprintf(format, args...))
}
func sourceJSON(_ *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
	if in.IsError() {
		return in
	}
	var indent *exec.Value
	if err := p.Take(exec.KeywordArgument("indent", exec.AsValue(nil), takeValueArgument(&indent))); err != nil {
		return exec.ValueError(err)
	}
	var spaces *int
	if !indent.IsNil() {
		if !indent.IsInteger() {
			return exec.ValueError(exec.SourceFailuref("TypeError: indent must be integer"))
		}
		n := indent.Integer()
		spaces = &n
	}
	s, err := exec.PythonJSON(in, spaces)
	if err != nil {
		return exec.ValueError(err)
	}
	for _, p := range [][2]string{{"<", `\u003c`}, {">", `\u003e`}, {"&", `\u0026`}, {"'", `\u0027`}} {
		s = strings.ReplaceAll(s, p[0], p[1])
	}
	return exec.AsSafeValue(s)
}
