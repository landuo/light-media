// Package mako provides the native Go template dependency for the installed
// notification plugin. It does not execute Python or invoke a Python runtime.
package mako

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// ErrUnsupported distinguishes an unimplemented valid construct from a source
// template error. Callers must not turn it into Mako's original-text fallback.
var ErrUnsupported = errors.New("Mako construct requires native implementation")

type fault string

func (e fault) Error() string { return string(e) }

type undefined struct{}
type tuple []any
type entry struct{ key, value any }
type dictionary []entry
type unorderedDictionary dictionary

// Field/OrderedObject retain source JSON object order. A Go map cannot carry
// that information and must not silently invent Python dict iteration order.
type Field struct {
	Key   string
	Value any
}
type OrderedObject []Field
type callable func(*environment, []any, map[string]any) (any, error)
type namedCallable struct {
	name string
	call callable
}

func asCallable(value any) (callable, bool) {
	if fn, ok := value.(namedCallable); ok {
		return fn.call, true
	}
	fn, ok := value.(callable)
	return fn, ok
}

type environment struct {
	ctx                context.Context
	values             map[string]any
	parent             *environment
	output             *strings.Builder
	module             *environment
	contextData        *environment // Root Context data before template locals.
	strict             bool
	pageFilter         templateFilters // Set on the module scope.
	hasPageArguments   bool            // Set on the module scope.
	externalNamespaces []externalNamespace
	callers            *callerStack // Shared by the render and its lexical scopes.
	callerFrame        *callerNamespace
	keywordOrder       []string
	anonymousDefs      map[int]callable
	rootDefs           []defNode
	rootCalls          map[string]callable
	globals            map[string]bool
	parameters         map[string]bool
	variadicPageargs   bool
	forwarding         map[string]bool
	independentDef     bool
	definitionContext  *environment
	contextLocals      map[string]bool
}

func (e *environment) get(k string) any {
	for at := e; at != nil; at = at.parent {
		if at.globals[k] && at.module != at {
			at = at.module
		}
		if v, ok := at.values[k]; ok {
			return v
		}
	}
	if v, ok := builtins[k]; ok {
		return v
	}
	if unsupportedBuiltins[k] {
		return callable(func(*environment, []any, map[string]any) (any, error) { return nil, ErrUnsupported })
	}
	return undefined{}
}

func (e *environment) set(name string, value any) {
	if e.globals[name] {
		e.module.values[name] = value
		return
	}
	e.values[name] = value
}

// A valid but unported Python builtin must never look like a missing template
// variable and accidentally take TemplateUtil's original-text exception path.
var unsupportedBuiltins = func() map[string]bool {
	r := map[string]bool{}
	for _, n := range strings.Fields("__import__ aiter anext ascii bin breakpoint bytearray bytes compile complex delattr dir divmod eval exec filter frozenset globals hash help hex id input isinstance issubclass iter locals map memoryview next object oct open pow print property setattr set slice staticmethod super type vars __build_class__") {
		r[n] = true
	}
	return r
}()

func (e *environment) child() *environment {
	return &environment{ctx: e.ctx, values: map[string]any{}, parent: e, output: e.output, module: e.module, strict: e.strict,
		callers: e.callers, callerFrame: e.callerFrame, keywordOrder: e.keywordOrder, definitionContext: e.definitionContext}
}
func (e *environment) check() error { return e.ctx.Err() }
func normalize(v any) any {
	switch x := v.(type) {
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			f, e := strconv.ParseFloat(string(x), 64)
			if e == nil {
				return f
			}
		} else if z, ok := new(big.Int).SetString(string(x), 10); ok {
			return z
		}
	case int:
		return big.NewInt(int64(x))
	case int64:
		return big.NewInt(x)
	case int32:
		return big.NewInt(int64(x))
	case uint64:
		return new(big.Int).SetUint64(x)
	case []any:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = normalize(v)
		}
		return r
	case []string:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = v
		}
		return r
	case map[string]any:
		// JSON and Go maps have no insertion-order metadata. Preserve the received
		// order separately in the host before using it as a user template dictionary.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		r := dictionary{}
		for _, k := range keys {
			r = append(r, entry{k, normalize(x[k])})
		}
		return unorderedDictionary(r)
	case OrderedObject:
		r := dictionary{}
		for _, f := range x {
			r = append(r, entry{f.Key, normalize(f.Value)})
		}
		return r
	}
	return v
}
func truth(v any) bool {
	switch x := v.(type) {
	case nil, undefined:
		return false
	case bool:
		return x
	case *big.Int:
		return x.Sign() != 0
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case tuple:
		return len(x) > 0
	case dictionary:
		return len(x) > 0
	case unorderedDictionary:
		return len(x) > 0
	case *callerStack:
		return x.current() != nil
	}
	return true
}
func pyString(v any) (string, error) {
	switch x := v.(type) {
	case caughtException:
		return "", ErrUnsupported
	case unorderedDictionary:
		return "", ErrUnsupported
	case undefined:
		return "", fault("NameError")
	case nil:
		return "None", nil
	case bool:
		if x {
			return "True", nil
		}
		return "False", nil
	case string:
		return x, nil
	case *big.Int:
		return x.String(), nil
	case float64:
		if math.IsInf(x, 1) {
			return "inf", nil
		}
		if math.IsInf(x, -1) {
			return "-inf", nil
		}
		if math.IsNaN(x) {
			return "nan", nil
		}
		s := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		// CPython uses fixed notation for 1e-4 <= abs(n) < 1e16.
		if math.Abs(x) >= 1e-4 && math.Abs(x) < 1e16 {
			s = strconv.FormatFloat(x, 'f', -1, 64)
			if !strings.Contains(s, ".") {
				s += ".0"
			}
		}
		return s, nil
	case []any:
		return sequenceRepr(x, "[", "]")
	case tuple:
		s, e := sequenceRepr([]any(x), "(", ")")
		if len(x) == 1 {
			s = strings.TrimSuffix(s, ")") + ",)"
		}
		return s, e
	case dictionary:
		p := []string{}
		for _, kv := range x {
			k, e := repr(kv.key)
			if e != nil {
				return "", e
			}
			v, e := repr(kv.value)
			if e != nil {
				return "", e
			}
			p = append(p, k+": "+v)
		}
		return "{" + strings.Join(p, ", ") + "}", nil
	}
	return "", ErrUnsupported
}
func repr(v any) (string, error) {
	if s, ok := v.(string); ok {
		quote := byte('\'')
		if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
			quote = '"'
		}
		var b strings.Builder
		b.WriteByte(quote)
		for _, r := range s {
			switch r {
			case '\\':
				b.WriteString("\\\\")
			case '\n':
				b.WriteString("\\n")
			case '\r':
				b.WriteString("\\r")
			case '\t':
				b.WriteString("\\t")
			default:
				if r == rune(quote) {
					b.WriteByte('\\')
					b.WriteRune(r)
				} else if !pythonPrintable(r) {
					if r <= 0xff {
						fmt.Fprintf(&b, "\\x%02x", r)
					} else if r <= 0xffff {
						fmt.Fprintf(&b, "\\u%04x", r)
					} else {
						fmt.Fprintf(&b, "\\U%08x", r)
					}
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte(quote)
		return b.String(), nil
	}
	return pyString(v)
}
func sequenceRepr(v []any, l, r string) (string, error) {
	p := make([]string, len(v))
	for i, x := range v {
		s, e := repr(x)
		if e != nil {
			return "", e
		}
		p[i] = s
	}
	return l + strings.Join(p, ", ") + r, nil
}
func integer(v any) (*big.Int, bool) {
	switch x := v.(type) {
	case *big.Int:
		return new(big.Int).Set(x), true
	case bool:
		if x {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	}
	return nil, false
}
func floating(v any) (float64, bool) {
	if x, ok := v.(float64); ok {
		return x, true
	}
	if z, ok := integer(v); ok {
		f, _ := new(big.Float).SetInt(z).Float64()
		return f, true
	}
	return 0, false
}
func indexInt(v any) (int, error) {
	z, ok := integer(v)
	if !ok || !z.IsInt64() {
		return 0, fault("TypeError")
	}
	i := z.Int64()
	if int64(int(i)) != i {
		return 0, fault("OverflowError")
	}
	return int(i), nil
}
func iterable(v any) ([]any, error) {
	switch x := v.(type) {
	case unorderedDictionary:
		return nil, ErrUnsupported
	case []any:
		return x, nil
	case tuple:
		return []any(x), nil
	case dictionary:
		r := make([]any, len(x))
		for i, k := range x {
			r[i] = k.key
		}
		return r, nil
	case string:
		r := []any{}
		for _, c := range x {
			r = append(r, string(c))
		}
		return r, nil
	}
	return nil, fault("TypeError")
}
func equal(a, b any) bool {
	if ai, ok := integer(a); ok {
		if bi, ok := integer(b); ok {
			return ai.Cmp(bi) == 0
		}
	}
	if c, ok, nan := numericCompare(a, b); ok {
		return !nan && c == 0
	}
	return reflect.DeepEqual(a, b)
}
func dictGet(d dictionary, k any) (any, bool) {
	for _, v := range d {
		if equal(v.key, k) {
			return v.value, true
		}
	}
	return nil, false
}
func dictSet(d dictionary, k, v any) (dictionary, error) {
	if !hashable(k) {
		return nil, fault("TypeError")
	}
	for i, x := range d {
		if equal(x.key, k) {
			d[i].value = v
			return d, nil
		}
	}
	return append(d, entry{k, v}), nil
}
func hashable(v any) bool {
	switch x := v.(type) {
	case []any, dictionary, unorderedDictionary:
		return false
	case tuple:
		for _, v := range x {
			if !hashable(v) {
				return false
			}
		}
	}
	return true
}
func compare(a, b any) (int, error) {
	if c, ok, _ := numericCompare(a, b); ok {
		return c, nil
	}
	if s, ok := a.(string); ok {
		if t, ok := b.(string); ok {
			return strings.Compare(s, t), nil
		}
	}
	return 0, fault("TypeError")
}
func numericCompare(a, b any) (int, bool, bool) {
	af, aok := floating(a)
	bf, bok := floating(b)
	if !aok || !bok {
		return 0, false, false
	}
	if math.IsNaN(af) || math.IsNaN(bf) {
		return 0, true, true
	}
	ai, ais := integer(a)
	bi, bis := integer(b)
	if ais && bis {
		return ai.Cmp(bi), true, false
	}
	if math.IsInf(af, 0) || math.IsInf(bf, 0) {
		if af < bf {
			return -1, true, false
		}
		if af > bf {
			return 1, true, false
		}
		return 0, true, false
	}
	ar, br := new(big.Rat), new(big.Rat)
	if ais {
		ar.SetInt(ai)
	} else {
		ar.SetFloat64(af)
	}
	if bis {
		br.SetInt(bi)
	} else {
		br.SetFloat64(bf)
	}
	return ar.Cmp(br), true, false
}
func binary(op string, a, b any) (any, error) {
	if op == "==" {
		return equal(a, b), nil
	}
	if op == "!=" {
		return !equal(a, b), nil
	}
	if op == "is" || op == "is not" {
		v := a == nil && b == nil
		if ab, ok := a.(bool); ok {
			bb, bok := b.(bool)
			v = bok && ab == bb
		} else if a != nil && b != nil {
			if _, ok := a.(undefined); !ok {
				return nil, ErrUnsupported
			}
		}
		if _, ok := a.(undefined); ok {
			_, v = b.(undefined)
		}
		if op == "is not" {
			v = !v
		}
		return v, nil
	}
	if op == "in" || op == "not in" {
		v := false
		if d, ok := b.(unorderedDictionary); ok {
			_, v = dictGet(dictionary(d), a)
		} else if s, ok := b.(string); ok {
			n, ok := a.(string)
			if !ok {
				return nil, fault("TypeError")
			}
			v = strings.Contains(s, n)
		} else {
			seq, e := iterable(b)
			if e != nil {
				return nil, e
			}
			for _, x := range seq {
				if equal(a, x) {
					v = true
					break
				}
			}
		}
		if op == "not in" {
			v = !v
		}
		return v, nil
	}
	if op == "<" || op == "<=" || op == ">" || op == ">=" {
		if _, ok, nan := numericCompare(a, b); ok && nan {
			return false, nil
		}
		c, e := compare(a, b)
		if e != nil {
			return nil, e
		}
		switch op {
		case "<":
			return c < 0, nil
		case "<=":
			return c <= 0, nil
		case ">":
			return c > 0, nil
		default:
			return c >= 0, nil
		}
	}
	if op == "+" {
		if s, ok := a.(string); ok {
			if t, ok := b.(string); ok {
				return s + t, nil
			}
		}
		if x, ok := a.([]any); ok {
			if y, ok := b.([]any); ok {
				return append(append([]any{}, x...), y...), nil
			}
		}
		if x, ok := a.(tuple); ok {
			if y, ok := b.(tuple); ok {
				return append(append(tuple{}, x...), y...), nil
			}
		}
	}
	if op == "*" {
		switch a.(type) {
		case string, []any, tuple:
			if n, e := indexInt(b); e == nil {
				return repeat(a, n)
			}
		}
		switch b.(type) {
		case string, []any, tuple:
			if n, e := indexInt(a); e == nil {
				return repeat(b, n)
			}
		}
	}
	if op == "%" {
		if s, ok := a.(string); ok {
			return percentFormat(s, b)
		}
	}
	if x, ok := integer(a); ok {
		if y, ok := integer(b); ok {
			switch op {
			case "+":
				return x.Add(x, y), nil
			case "-":
				return x.Sub(x, y), nil
			case "*":
				return x.Mul(x, y), nil
			case "&":
				return x.And(x, y), nil
			case "|":
				return x.Or(x, y), nil
			case "^":
				return x.Xor(x, y), nil
			case "<<", ">>":
				if y.Sign() < 0 {
					return nil, fault("ValueError")
				}
				if !y.IsUint64() {
					return nil, fault("OverflowError")
				}
				if op == "<<" {
					return x.Lsh(x, uint(y.Uint64())), nil
				}
				return x.Rsh(x, uint(y.Uint64())), nil
			case "//", "%":
				if y.Sign() == 0 {
					return nil, fault("ZeroDivisionError")
				}
				q, r := new(big.Int), new(big.Int)
				q.QuoRem(x, y, r)
				if r.Sign() != 0 && r.Sign() != y.Sign() {
					q.Sub(q, big.NewInt(1))
					r.Add(r, y)
				}
				if op == "//" {
					return q, nil
				}
				return r, nil
			case "**":
				if y.Sign() >= 0 {
					return x.Exp(x, y, nil), nil
				}
			}
		}
	}
	x, xok := floating(a)
	y, yok := floating(b)
	if !xok || !yok {
		return nil, fault("TypeError")
	}
	switch op {
	case "+":
		return x + y, nil
	case "-":
		return x - y, nil
	case "*":
		return x * y, nil
	case "/":
		if y == 0 {
			return nil, fault("ZeroDivisionError")
		}
		return x / y, nil
	case "//":
		if y == 0 {
			return nil, fault("ZeroDivisionError")
		}
		q, _ := floatDivMod(x, y)
		return q, nil
	case "%":
		if y == 0 {
			return nil, fault("ZeroDivisionError")
		}
		_, r := floatDivMod(x, y)
		return r, nil
	case "**":
		return math.Pow(x, y), nil
	}
	return nil, ErrUnsupported
}
func floatDivMod(x, y float64) (float64, float64) {
	r := math.Mod(x, y)
	q := (x - r) / y
	if r != 0 {
		if math.Signbit(y) != math.Signbit(r) {
			r += y
			q--
		}
	} else {
		r = math.Copysign(0, y)
	}
	floor := float64(0)
	if q != 0 {
		floor = math.Floor(q)
		if q-floor > 0.5 {
			floor++
		}
	} else {
		floor = math.Copysign(0, x/y)
	}
	return floor, r
}

// String preserves the source's Python str conversion before URL encoding.
func String(v any) (string, error) { return pyString(normalize(v)) }
func repeat(v any, n int) (any, error) {
	if n < 0 {
		n = 0
	}
	switch x := v.(type) {
	case string:
		if n > 0 && len(x) > int(^uint(0)>>1)/n {
			return nil, fault("OverflowError")
		}
		return strings.Repeat(x, n), nil
	case []any:
		r := []any{}
		for i := 0; i < n; i++ {
			r = append(r, x...)
		}
		return r, nil
	case tuple:
		r := tuple{}
		for i := 0; i < n; i++ {
			r = append(r, x...)
		}
		return r, nil
	}
	return nil, fault("TypeError")
}
func percentFormat(s string, v any) (any, error) {
	args := []any{v}
	if t, ok := v.(tuple); ok {
		args = []any(t)
	}
	at := 0
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i == len(s) {
			return nil, fault("ValueError")
		}
		if s[i] == '%' {
			b.WriteByte('%')
			continue
		}
		if at == len(args) {
			return nil, fault("TypeError")
		}
		x := args[at]
		at++
		switch s[i] {
		case 's':
			a, e := pyString(x)
			if e != nil {
				return nil, e
			}
			b.WriteString(a)
		case 'r':
			a, e := repr(x)
			if e != nil {
				return nil, e
			}
			b.WriteString(a)
		case 'd', 'i':
			z, ok := integer(x)
			if !ok {
				f, ok := floating(x)
				if !ok {
					return nil, fault("TypeError")
				}
				z = big.NewInt(int64(f))
			}
			b.WriteString(z.String())
		default:
			return nil, ErrUnsupported
		}
	}
	if at != len(args) {
		return nil, fault("TypeError")
	}
	return b.String(), nil
}
