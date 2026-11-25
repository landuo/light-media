package mergemessagenotify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"unicode"
)

// JSON configuration and event values retain json.Number; nil is Python None.
func Decode(raw []byte) (any, error) {
	if extendedJSON(string(raw)) {
		return nil, ErrDependency
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	err := d.Decode(&v)
	if err == nil && d.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid trailing JSON")
	}
	return v, err
}

// CPython accepts these non-RFC JSON constants. Until their downstream Python
// serializer behavior is ported, detect them rather than silently taking the
// source's invalid-JSON fallback branch.
func extendedJSON(s string) bool {
	var b strings.Builder
	quoted, escaped, changed := false, false, false
	for i := 0; i < len(s); {
		c := s[i]
		if quoted {
			b.WriteByte(c)
			i++
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			b.WriteByte(c)
			i++
			continue
		}
		found := false
		for _, token := range []string{"-Infinity", "Infinity", "NaN"} {
			if strings.HasPrefix(s[i:], token) {
				b.WriteString("null")
				i += len(token)
				changed = true
				found = true
				break
			}
		}
		if !found {
			b.WriteByte(c)
			i++
		}
	}
	return changed && json.Valid([]byte(b.String()))
}
func clone(v any) any {
	switch x := v.(type) {
	case map[string]any:
		r := map[string]any{}
		for k, v := range x {
			r[k] = clone(v)
		}
		return r
	case []any:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = clone(v)
		}
		return r
	case []byte:
		return bytes.Clone(x)
	case []string:
		return append([]string(nil), x...)
	default:
		return x
	}
}
func cloneMap(v map[string]any) map[string]any {
	if v == nil {
		return nil
	}
	return clone(v).(map[string]any)
}
func truth(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		n, e := strconv.ParseFloat(string(x), 64)
		return e != nil || n != 0
	case float64:
		return x != 0
	case int:
		return x != 0
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		return r.Len() > 0
	}
	return true
}
func str(v any) string {
	if v == nil {
		return "None"
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case json.Number:
		return pythonNumber(x)
	case float64:
		return pythonFloat(x)
	}
	return fmt.Sprint(v)
}
func pythonNumber(n json.Number) string {
	if strings.ContainsAny(string(n), ".eE") {
		if v, e := strconv.ParseFloat(string(n), 64); e == nil {
			return pythonFloat(v)
		}
	}
	return string(n)
}
func pythonFloat(v float64) string {
	s := strconv.FormatFloat(v, 'e', -1, 64)
	pos := strings.IndexByte(s, 'e')
	if pos < 0 {
		return s
	}
	exp, _ := strconv.Atoi(s[pos+1:])
	if exp >= -4 && exp < 16 {
		s = strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	return s
}
func stringValue(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", errors.New("TypeError")
	}
	return s, nil
}
func or(v, other any) any {
	if truth(v) {
		return v
	}
	return other
}
func strip(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
}
func numberEq(v any, want int) bool {
	switch x := v.(type) {
	case bool:
		return (x && want == 1) || (!x && want == 0)
	case int:
		return x == want
	case float64:
		return x == float64(want)
	case json.Number:
		n, e := strconv.ParseFloat(string(x), 64)
		return e == nil && n == float64(want)
	}
	return false
}
func contains(v any, want string) (bool, error) {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, want), nil
	case []any:
		for _, i := range x {
			if i == want {
				return true, nil
			}
		}
		return false, nil
	case []string:
		for _, i := range x {
			if i == want {
				return true, nil
			}
		}
		return false, nil
	case map[string]any:
		_, ok := x[want]
		return ok, nil
	default:
		return false, errors.New("TypeError")
	}
}
func iterable(v any) ([]any, error) {
	switch x := v.(type) {
	case []any:
		return x, nil
	case []string:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = v
		}
		return r, nil
	case string:
		r := []any{}
		for _, c := range x {
			r = append(r, string(c))
		}
		return r, nil
	case map[string]any:
		return nil, errors.New("unordered Python mapping iteration requires ordered input")
	}
	return nil, errors.New("TypeError")
}

// Python set iteration has no cross-process stable order. Set-derived channel
// recipients are represented in first-occurrence order; tests compare them as
// sets, and integrations must not infer a priority from their order.
func splitMulti(v any) ([]string, error) {
	r := []string{}
	if !truth(v) {
		return r, nil
	}
	s, e := stringValue(v)
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	for _, item := range strings.Split(s, ",") {
		item = strip(item)
		if item != "" && !seen[item] {
			r = append(r, item)
			seen[item] = true
		}
	}
	return r, nil
}

type pair struct {
	k any
	v any
}

// object preserves Python dict insertion order for Bark's encrypted JSON.
type object []pair

func obj(kv ...any) object {
	r := object{}
	for i := 0; i < len(kv); i += 2 {
		r = append(r, pair{kv[i].(string), kv[i+1]})
	}
	return r
}
func (o *object) put(k string, v any) {
	o.putAny(k, v)
}
func (o *object) putAny(k any, v any) {
	for i := range *o {
		if keyEqual((*o)[i].k, k) {
			(*o)[i].v = v
			return
		}
	}
	*o = append(*o, pair{k, v})
}
func (o object) asMap() map[string]any {
	r := map[string]any{}
	for _, v := range o {
		r[str(v.k)] = v.v
	}
	return r
}
func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, p := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(jsonKey(p.k))
		v, e := json.Marshal(p.v)
		if e != nil {
			return nil, e
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
func numericRat(v any) *big.Rat {
	switch x := v.(type) {
	case bool:
		if x {
			return big.NewRat(1, 1)
		}
		return big.NewRat(0, 1)
	case int:
		return big.NewRat(int64(x), 1)
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			n, e := strconv.ParseFloat(string(x), 64)
			if e != nil {
				return nil
			}
			return new(big.Rat).SetFloat64(n)
		}
		r, _ := new(big.Rat).SetString(string(x))
		return r
	}
	return nil
}
func keyEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if sa, ok := a.(string); ok {
		sb, ok := b.(string)
		return ok && sa == sb
	}
	ra, rb := numericRat(a), numericRat(b)
	return ra != nil && rb != nil && ra.Cmp(rb) == 0
}
func jsonKey(k any) string {
	switch v := k.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return str(v)
	}
}
func normalize(v any) any {
	switch x := v.(type) {
	case object:
		return normalize(x.asMap())
	case map[string]any:
		if x == nil {
			return nil
		}
		r := map[string]any{}
		for k, v := range x {
			r[k] = normalize(v)
		}
		return r
	case []byte:
		return map[string]any{"$bytes": encode64(x)}
	case []any:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = normalize(v)
		}
		return r
	default:
		return v
	}
}
