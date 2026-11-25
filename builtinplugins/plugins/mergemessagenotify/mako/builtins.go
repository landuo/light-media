package mako

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

func arity(a []any, k map[string]any, min, max int) error {
	if len(a) < min || max >= 0 && len(a) > max || len(k) != 0 {
		return fault("TypeError")
	}
	return nil
}

var builtins = map[string]any{
	"str": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 0, 1); err != nil {
			return nil, err
		}
		if len(a) == 0 {
			return "", nil
		}
		return pyString(a[0])
	}),
	"repr": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 1); err != nil {
			return nil, err
		}
		return repr(a[0])
	}),
	"bool": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 0, 1); err != nil {
			return nil, err
		}
		return len(a) > 0 && truth(a[0]), nil
	}),
	"int": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 0, 2); err != nil {
			return nil, err
		}
		if len(a) == 0 {
			return big.NewInt(0), nil
		}
		if z, ok := integer(a[0]); ok && len(a) == 1 {
			return z, nil
		}
		if f, ok := a[0].(float64); ok && len(a) == 1 {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, fault("ValueError")
			}
			z, _ := new(big.Float).SetFloat64(f).Int(nil)
			return z, nil
		}
		s, ok := a[0].(string)
		if !ok {
			return nil, fault("TypeError")
		}
		base := 10
		if len(a) > 1 {
			v, err := indexInt(a[1])
			if err != nil {
				return nil, err
			}
			base = v
		}
		if base != 0 && (base < 2 || base > 36) {
			return nil, fault("ValueError")
		}
		s = strings.TrimSpace(s)
		z, ok := new(big.Int).SetString(s, base)
		if !ok {
			return nil, fault("ValueError")
		}
		return z, nil
	}),
	"float": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 0, 1); err != nil {
			return nil, err
		}
		if len(a) == 0 {
			return float64(0), nil
		}
		if f, ok := floating(a[0]); ok {
			return f, nil
		}
		s, ok := a[0].(string)
		if !ok {
			return nil, fault("TypeError")
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return nil, fault("ValueError")
		}
		return f, nil
	}),
	"len": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 1); err != nil {
			return nil, err
		}
		if d, ok := a[0].(unorderedDictionary); ok {
			return big.NewInt(int64(len(d))), nil
		}
		v, err := iterable(a[0])
		if err != nil {
			return nil, err
		}
		return big.NewInt(int64(len(v))), nil
	}),
	"list": sequenceBuiltin(false), "tuple": sequenceBuiltin(true),
	"dict": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if len(a) > 1 {
			return nil, fault("TypeError")
		}
		d := dictionary{}
		if len(a) > 0 {
			if x, ok := a[0].(dictionary); ok {
				d = append(d, x...)
			} else {
				xs, err := iterable(a[0])
				if err != nil {
					return nil, err
				}
				for _, x := range xs {
					pair, err := iterable(x)
					if err != nil || len(pair) != 2 {
						return nil, fault("ValueError")
					}
					d, err = dictSet(d, pair[0], pair[1])
					if err != nil {
						return nil, err
					}
				}
			}
		}
		names := []string{}
		for n := range k {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			d = append(d, entry{n, k[n]})
		}
		return d, nil
	}),
	"range": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 3); err != nil {
			return nil, err
		}
		start, end, step := 0, 0, 1
		ints := make([]int, len(a))
		for i, v := range a {
			n, err := indexInt(v)
			if err != nil {
				return nil, err
			}
			ints[i] = n
		}
		if len(a) == 1 {
			end = ints[0]
		} else {
			start, end = ints[0], ints[1]
		}
		if len(a) == 3 {
			step = ints[2]
		}
		if step == 0 {
			return nil, fault("ValueError")
		}
		out := []any{}
		for i := start; step > 0 && i < end || step < 0 && i > end; i += step {
			if err := e.check(); err != nil {
				return nil, err
			}
			out = append(out, big.NewInt(int64(i)))
			if step > 0 && i > int(^uint(0)>>1)-step || step < 0 && i < (-int(^uint(0)>>1)-1)-step {
				break
			}
		}
		return out, nil
	}),
	"enumerate": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 2); err != nil {
			return nil, err
		}
		xs, err := iterable(a[0])
		if err != nil {
			return nil, err
		}
		start := big.NewInt(0)
		if len(a) > 1 {
			var ok bool
			start, ok = integer(a[1])
			if !ok {
				return nil, fault("TypeError")
			}
		}
		out := []any{}
		for _, v := range xs {
			out = append(out, tuple{new(big.Int).Set(start), v})
			start.Add(start, big.NewInt(1))
		}
		return out, nil
	}),
	"zip": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		strict := truth(k["strict"])
		for n := range k {
			if n != "strict" {
				return nil, fault("TypeError")
			}
		}
		xs := make([][]any, len(a))
		n := 0
		for i, v := range a {
			x, err := iterable(v)
			if err != nil {
				return nil, err
			}
			xs[i] = x
			if i == 0 || len(x) < n {
				n = len(x)
			}
		}
		if strict {
			for _, x := range xs {
				if len(x) != n {
					return nil, fault("ValueError")
				}
			}
		}
		out := []any{}
		for i := 0; i < n; i++ {
			t := tuple{}
			for _, x := range xs {
				t = append(t, x[i])
			}
			out = append(out, t)
		}
		return out, nil
	}),
	"sum": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 2); err != nil {
			return nil, err
		}
		xs, err := iterable(a[0])
		if err != nil {
			return nil, err
		}
		var v any = big.NewInt(0)
		if len(a) > 1 {
			v = a[1]
		}
		for _, x := range xs {
			v, err = binary("+", v, x)
			if err != nil {
				return nil, err
			}
		}
		return v, nil
	}),
	"all": truthBuiltin(true), "any": truthBuiltin(false),
	"abs": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 1); err != nil {
			return nil, err
		}
		if z, ok := integer(a[0]); ok {
			return z.Abs(z), nil
		}
		if f, ok := floating(a[0]); ok {
			return math.Abs(f), nil
		}
		return nil, fault("TypeError")
	}),
	"round": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 2); err != nil {
			return nil, err
		}
		f, ok := floating(a[0])
		if !ok {
			return nil, fault("TypeError")
		}
		if len(a) == 1 {
			z, _ := new(big.Float).SetFloat64(math.RoundToEven(f)).Int(nil)
			return z, nil
		}
		n, err := indexInt(a[1])
		if err != nil {
			return nil, err
		}
		if z, ok := integer(a[0]); ok && n >= 0 {
			return z, nil
		}
		p := math.Pow10(n)
		return math.RoundToEven(f*p) / p, nil
	}),
	"min": extremeBuiltin(false), "max": extremeBuiltin(true),
	"sorted": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if len(a) != 1 {
			return nil, fault("TypeError")
		}
		for n := range k {
			if n != "key" && n != "reverse" {
				return nil, fault("TypeError")
			}
		}
		xs, err := iterable(a[0])
		if err != nil {
			return nil, err
		}
		out := append([]any{}, xs...)
		keys := append([]any{}, out...)
		if fn := k["key"]; fn != nil {
			call, ok := asCallable(fn)
			if !ok {
				return nil, fault("TypeError")
			}
			for i, v := range out {
				keys[i], err = call(e, []any{v}, nil)
				if err != nil {
					return nil, err
				}
			}
		}
		type pair struct{ v, k any }
		ps := make([]pair, len(out))
		for i := range out {
			ps[i] = pair{out[i], keys[i]}
		}
		sort.SliceStable(ps, func(i, j int) bool {
			c, er := compare(ps[i].k, ps[j].k)
			if er != nil {
				err = er
			}
			if truth(k["reverse"]) {
				return c > 0
			}
			return c < 0
		})
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i] = ps[i].v
		}
		return out, nil
	}),
	"reversed": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 1); err != nil {
			return nil, err
		}
		xs, err := iterable(a[0])
		if err != nil {
			return nil, err
		}
		out := append([]any{}, xs...)
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		return out, nil
	}),
	"chr": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 1); err != nil {
			return nil, err
		}
		n, err := indexInt(a[0])
		if err != nil || n < 0 || n > 0x10ffff {
			return nil, fault("ValueError")
		}
		return string(rune(n)), nil
	}),
	"ord": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 1); err != nil {
			return nil, err
		}
		s, ok := a[0].(string)
		if !ok || len([]rune(s)) != 1 {
			return nil, fault("TypeError")
		}
		return big.NewInt(int64([]rune(s)[0])), nil
	}),
	"format": callable(func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 2); err != nil {
			return nil, err
		}
		spec := ""
		if len(a) > 1 {
			var ok bool
			spec, ok = a[1].(string)
			if !ok {
				return nil, fault("TypeError")
			}
		}
		return formatValue(a[0], spec)
	}),
}

func sequenceBuiltin(t bool) callable {
	return func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 0, 1); err != nil {
			return nil, err
		}
		out := []any{}
		if len(a) > 0 {
			v, err := iterable(a[0])
			if err != nil {
				return nil, err
			}
			out = append(out, v...)
		}
		if t {
			return tuple(out), nil
		}
		return out, nil
	}
}
func truthBuiltin(all bool) callable {
	return func(e *environment, a []any, k map[string]any) (any, error) {
		if err := arity(a, k, 1, 1); err != nil {
			return nil, err
		}
		xs, err := iterable(a[0])
		if err != nil {
			return nil, err
		}
		for _, v := range xs {
			if truth(v) != all {
				return !all, nil
			}
		}
		return all, nil
	}
}
func extremeBuiltin(max bool) callable {
	return func(e *environment, a []any, k map[string]any) (any, error) {
		if len(a) == 0 {
			return nil, fault("TypeError")
		}
		for n := range k {
			if n != "key" && n != "default" {
				return nil, fault("TypeError")
			}
		}
		xs := a
		if len(a) == 1 {
			v, err := iterable(a[0])
			if err != nil {
				return nil, err
			}
			xs = v
		}
		if len(xs) == 0 {
			if v, ok := k["default"]; ok {
				return v, nil
			}
			return nil, fault("ValueError")
		}
		key := func(v any) (any, error) {
			if fn := k["key"]; fn != nil {
				f, ok := asCallable(fn)
				if !ok {
					return nil, fault("TypeError")
				}
				return f(e, []any{v}, nil)
			}
			return v, nil
		}
		best := xs[0]
		bk, err := key(best)
		if err != nil {
			return nil, err
		}
		for _, v := range xs[1:] {
			vk, er := key(v)
			if er != nil {
				return nil, er
			}
			c, er := compare(vk, bk)
			if er != nil {
				return nil, er
			}
			if max && c > 0 || !max && c < 0 {
				best, bk = v, vk
			}
		}
		return best, nil
	}
}

func getAttribute(v any, name string) (any, error) {
	if fn, ok := v.(namedCallable); ok && name == "__name__" {
		if fn.name == "" {
			return nil, fault("AttributeError")
		}
		return fn.name, nil
	}
	if c, ok := v.(renderContext); ok {
		return c.attribute(name)
	}
	if s, ok := v.(selfNamespace); ok {
		return s.attribute(name)
	}
	if callers, ok := v.(*callerStack); ok {
		return callers.current().attribute(name)
	}
	if caller, ok := v.(*callerNamespace); ok {
		return caller.attribute(name)
	}
	if loop, ok := v.(*loopState); ok {
		return loop.attribute(name)
	}
	if s, ok := v.(string); ok {
		return stringMethod(s, name)
	}
	if d, ok := v.(unorderedDictionary); ok {
		if name == "get" {
			return getAttribute(dictionary(d), name)
		}
		if knownTypeAttribute(dictionary(d), name) {
			return nil, ErrUnsupported
		}
		return nil, fault("AttributeError")
	}
	if d, ok := v.(dictionary); ok {
		switch name {
		case "get":
			return callable(func(e *environment, a []any, k map[string]any) (any, error) {
				if err := arity(a, k, 1, 2); err != nil {
					return nil, err
				}
				if v, ok := dictGet(d, a[0]); ok {
					return v, nil
				}
				if len(a) > 1 {
					return a[1], nil
				}
				return nil, nil
			}), nil
		case "keys", "values", "items":
			return callable(func(e *environment, a []any, k map[string]any) (any, error) {
				if err := arity(a, k, 0, 0); err != nil {
					return nil, err
				}
				out := []any{}
				for _, kv := range d {
					switch name {
					case "keys":
						out = append(out, kv.key)
					case "values":
						out = append(out, kv.value)
					case "items":
						out = append(out, tuple{kv.key, kv.value})
					}
				}
				return out, nil
			}), nil
		}
	}
	if xs, ok := v.([]any); ok && (name == "count" || name == "index") {
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if err := arity(a, k, 1, 1); err != nil {
				return nil, err
			}
			count := int64(0)
			for i, v := range xs {
				if equal(v, a[0]) {
					count++
					if name == "index" {
						return big.NewInt(int64(i)), nil
					}
				}
			}
			if name == "index" {
				return nil, fault("ValueError")
			}
			return big.NewInt(count), nil
		}), nil
	}
	if _, ok := v.(undefined); ok {
		return nil, fault("NameError")
	}
	if knownTypeAttribute(v, name) {
		return nil, ErrUnsupported
	}
	return nil, fault("AttributeError")
}
func knownTypeAttribute(v any, name string) bool {
	methods := ""
	switch v.(type) {
	case []any:
		methods = "append clear copy count extend index insert pop remove reverse sort"
	case tuple:
		methods = "count index"
	case dictionary:
		methods = "clear copy fromkeys get items keys pop popitem setdefault update values"
	case *big.Int:
		methods = "as_integer_ratio bit_count bit_length conjugate denominator from_bytes imag is_integer numerator real to_bytes"
	case float64:
		methods = "as_integer_ratio conjugate fromhex hex imag is_integer real"
	}
	return strings.Contains(" "+methods+" ", " "+name+" ") || strings.HasPrefix(name, "__")
}
func stringMethod(s, name string) (any, error) {
	switch name {
	case "upper", "lower", "casefold", "capitalize", "title", "swapcase":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if err := arity(a, k, 0, 0); err != nil {
				return nil, err
			}
			return pythonCase(s, name), nil
		}), nil
	case "strip", "lstrip", "rstrip":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if err := arity(a, k, 0, 1); err != nil {
				return nil, err
			}
			test := func(r rune) bool { return pythonSpace(r) }
			if len(a) > 0 && a[0] != nil {
				chars, ok := a[0].(string)
				if !ok {
					return nil, fault("TypeError")
				}
				test = func(r rune) bool { return strings.ContainsRune(chars, r) }
			}
			if name == "lstrip" {
				return strings.TrimLeftFunc(s, test), nil
			}
			if name == "rstrip" {
				return strings.TrimRightFunc(s, test), nil
			}
			return strings.TrimFunc(s, test), nil
		}), nil
	case "replace":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if err := arity(a, k, 2, 3); err != nil {
				return nil, err
			}
			old, ok := a[0].(string)
			if !ok {
				return nil, fault("TypeError")
			}
			new, ok := a[1].(string)
			if !ok {
				return nil, fault("TypeError")
			}
			n := -1
			if len(a) > 2 {
				var err error
				n, err = indexInt(a[2])
				if err != nil {
					return nil, err
				}
			}
			return strings.Replace(s, old, new, n), nil
		}), nil
	case "split", "rsplit":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if len(a) > 2 {
				return nil, fault("TypeError")
			}
			sep := any(nil)
			max := -1
			if len(a) > 0 {
				sep = a[0]
			}
			if len(a) > 1 {
				n, err := indexInt(a[1])
				if err != nil {
					return nil, err
				}
				max = n
			}
			for n, v := range k {
				if n == "sep" && len(a) == 0 {
					sep = v
				} else if n == "maxsplit" && len(a) < 2 {
					z, err := indexInt(v)
					if err != nil {
						return nil, err
					}
					max = z
				} else {
					return nil, fault("TypeError")
				}
			}
			parts, err := pythonSplit(s, sep, max, name == "rsplit")
			if err != nil {
				return nil, err
			}
			out := []any{}
			for _, p := range parts {
				out = append(out, p)
			}
			return out, nil
		}), nil
	case "join":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if err := arity(a, k, 1, 1); err != nil {
				return nil, err
			}
			xs, err := iterable(a[0])
			if err != nil {
				return nil, err
			}
			strs := make([]string, len(xs))
			for i, v := range xs {
				ss, ok := v.(string)
				if !ok {
					return nil, fault("TypeError")
				}
				strs[i] = ss
			}
			return strings.Join(strs, s), nil
		}), nil
	case "startswith", "endswith", "find", "rfind", "index", "rindex", "count":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if err := arity(a, k, 1, 3); err != nil {
				return nil, err
			}
			r := []rune(s)
			start, end := 0, len(r)
			if len(a) > 1 {
				n, err := indexInt(a[1])
				if err != nil {
					return nil, err
				}
				start = n
				if start < 0 {
					start += len(r)
				}
				if start < 0 {
					start = 0
				}
			}
			if len(a) > 2 {
				n, err := indexInt(a[2])
				if err != nil {
					return nil, err
				}
				end = n
				if end < 0 {
					end += len(r)
				}
				if end < 0 {
					end = 0
				}
				if end > len(r) {
					end = len(r)
				}
			}
			candidates := []any{a[0]}
			if t, ok := a[0].(tuple); ok && (name == "startswith" || name == "endswith") {
				candidates = []any(t)
			}
			valid := start <= end && start <= len(r)
			ss := ""
			if valid {
				ss = string(r[start:end])
			}
			for _, c := range candidates {
				needle, ok := c.(string)
				if !ok {
					return nil, fault("TypeError")
				}
				switch name {
				case "startswith":
					if valid && strings.HasPrefix(ss, needle) {
						return true, nil
					}
				case "endswith":
					if valid && strings.HasSuffix(ss, needle) {
						return true, nil
					}
				case "count":
					if !valid {
						return big.NewInt(0), nil
					}
					return big.NewInt(int64(strings.Count(ss, needle))), nil
				default:
					i := -1
					if valid {
						if name == "rfind" || name == "rindex" {
							i = strings.LastIndex(ss, needle)
						} else {
							i = strings.Index(ss, needle)
						}
					}
					if i >= 0 {
						i = start + len([]rune(ss[:i]))
					}
					if i < 0 && (name == "index" || name == "rindex") {
						return nil, fault("ValueError")
					}
					return big.NewInt(int64(i)), nil
				}
			}
			return false, nil
		}), nil
	case "format":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) { return braceFormat(e, s, a, k) }), nil
	case "removeprefix", "removesuffix":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if err := arity(a, k, 1, 1); err != nil {
				return nil, err
			}
			p, ok := a[0].(string)
			if !ok {
				return nil, fault("TypeError")
			}
			if name == "removeprefix" {
				return strings.TrimPrefix(s, p), nil
			}
			return strings.TrimSuffix(s, p), nil
		}), nil
	}
	if strings.Contains(" center encode expandtabs format_map isalnum isalpha isascii isdecimal isdigit isidentifier islower isnumeric isprintable isspace istitle isupper ljust maketrans partition rjust rpartition splitlines translate zfill ", " "+name+" ") || strings.HasPrefix(name, "__") {
		return nil, ErrUnsupported
	}
	return nil, fault("AttributeError")
}
func pythonSpace(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f }
func pythonSplit(s string, separator any, max int, reverse bool) ([]string, error) {
	if separator == nil {
		// Whitespace splitting retains the unsplit suffix/prefix when maxsplit is
		// reached, unlike Fields + Join which loses original internal whitespace.
		r := []rune(s)
		out := []string{}
		if reverse {
			end := len(r)
			for end > 0 && pythonSpace(r[end-1]) {
				end--
			}
			for end > 0 {
				if max >= 0 && len(out) == max {
					out = append(out, string(r[:end]))
					break
				}
				start := end
				for start > 0 && !pythonSpace(r[start-1]) {
					start--
				}
				out = append(out, string(r[start:end]))
				end = start
				for end > 0 && pythonSpace(r[end-1]) {
					end--
				}
			}
			for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
				out[i], out[j] = out[j], out[i]
			}
			return out, nil
		}
		start := 0
		for start < len(r) && pythonSpace(r[start]) {
			start++
		}
		for start < len(r) {
			if max >= 0 && len(out) == max {
				out = append(out, string(r[start:]))
				break
			}
			end := start
			for end < len(r) && !pythonSpace(r[end]) {
				end++
			}
			out = append(out, string(r[start:end]))
			start = end
			for start < len(r) && pythonSpace(r[start]) {
				start++
			}
		}
		return out, nil
	}
	sep, ok := separator.(string)
	if !ok {
		return nil, fault("TypeError")
	}
	if sep == "" {
		return nil, fault("ValueError")
	}
	if max < 0 {
		return strings.Split(s, sep), nil
	}
	if !reverse {
		return strings.SplitN(s, sep, max+1), nil
	}
	out := []string{}
	for i := 0; i < max; i++ {
		at := strings.LastIndex(s, sep)
		if at < 0 {
			break
		}
		out = append(out, s[at+len(sep):])
		s = s[:at]
	}
	out = append(out, s)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
func formatValue(v any, spec string) (any, error) {
	if spec == "" {
		return pyString(v)
	}
	if spec == "s" {
		s, ok := v.(string)
		if !ok {
			return nil, fault("ValueError")
		}
		return s, nil
	}
	if strings.HasSuffix(spec, "d") {
		z, ok := integer(v)
		if !ok {
			return nil, fault("ValueError")
		}
		width := strings.TrimSuffix(spec, "d")
		n, err := strconv.Atoi(width)
		if err != nil {
			return nil, ErrUnsupported
		}
		s := z.String()
		if len(s) < n {
			fill := " "
			if strings.HasPrefix(width, "0") {
				fill = "0"
			}
			if strings.HasPrefix(s, "-") && fill == "0" {
				s = "-" + strings.Repeat(fill, n-len(s)) + s[1:]
			} else {
				s = strings.Repeat(fill, n-len(s)) + s
			}
		}
		return s, nil
	}
	if strings.HasPrefix(spec, ".") && strings.HasSuffix(spec, "f") {
		n, err := strconv.Atoi(spec[1 : len(spec)-1])
		f, ok := floating(v)
		if err != nil || !ok {
			return nil, fault("ValueError")
		}
		return strconv.FormatFloat(f, 'f', n, 64), nil
	}
	return nil, ErrUnsupported
}
func braceFormat(e *environment, s string, args []any, kw map[string]any) (any, error) {
	var b strings.Builder
	auto := 0
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "{{") {
			b.WriteByte('{')
			i += 2
			continue
		}
		if strings.HasPrefix(s[i:], "}}") {
			b.WriteByte('}')
			i += 2
			continue
		}
		if s[i] == '}' {
			return nil, fault("ValueError")
		}
		if s[i] != '{' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i+1:], '}')
		if end < 0 {
			return nil, fault("ValueError")
		}
		field := s[i+1 : i+1+end]
		i += end + 2
		spec := ""
		if at := strings.IndexByte(field, ':'); at >= 0 {
			spec = field[at+1:]
			field = field[:at]
		}
		conversion := ""
		if at := strings.IndexByte(field, '!'); at >= 0 {
			conversion = field[at+1:]
			field = field[:at]
		}
		if field == "" {
			field = strconv.Itoa(auto)
			auto++
		}
		var v any
		if n, err := strconv.Atoi(field); err == nil {
			if n < 0 || n >= len(args) {
				return nil, fault("IndexError")
			}
			v = args[n]
		} else {
			var ok bool
			v, ok = kw[field]
			if !ok {
				return nil, fault("KeyError")
			}
		}
		if conversion == "r" {
			r, err := repr(v)
			if err != nil {
				return nil, err
			}
			v = r
		} else if conversion == "s" {
			r, err := pyString(v)
			if err != nil {
				return nil, err
			}
			v = r
		} else if conversion != "" {
			return nil, ErrUnsupported
		}
		out, err := formatValue(v, spec)
		if err != nil {
			return nil, err
		}
		b.WriteString(fmt.Sprint(out))
	}
	return b.String(), nil
}
