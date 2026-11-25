// Package sitestatistic implements the installed SiteStatistic 1.9.7 in Go.
package sitestatistic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Data map[string]any
type Selected struct {
	Day               any
	Current, Previous []Data
}
type Snapshot struct {
	Latest, History []Data
	ActiveDomains   []string
}
type Node map[string]any
type Notification struct {
	Title, Text, Fingerprint   string
	uploadTotal, downloadTotal *big.Int
}
type Indexer map[string]any

// Host contains source operations, not a plugin business executor. A source
// refresh must describe this attempt's returned DTO, independently of storage.
type Host interface {
	Latest(context.Context) ([]Data, error)
	ActiveDomains(context.Context) ([]string, error)
	ByDate(context.Context, string) ([]Data, error)
	GetData(context.Context, string) (any, error)
	SaveData(context.Context, string, any) error
	SaveConfig(context.Context, map[string]any) error
	Post(context.Context, Notification) error
	Log(context.Context, string, string) error
	Indexer(context.Context, string) (Indexer, error)
	Refresh(context.Context, Indexer) (json.RawMessage, error)
	RefreshAll(context.Context) error
}

func decode(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing source JSON")
	}
	return v, nil
}
func clone(v any) any {
	raw, e := json.Marshal(v)
	if e != nil {
		return v
	}
	x, e := decode(raw)
	if e != nil {
		return v
	}
	return x
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
	case *big.Int:
		return x.Sign() != 0
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Array, reflect.Slice, reflect.Map:
		return r.Len() != 0
	}
	return true
}
func sourceTypeName(v any) string {
	if v == nil {
		return "NoneType"
	}
	switch x := v.(type) {
	case bool:
		return "bool"
	case string:
		return "str"
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return "float"
		}
		return "int"
	case float64:
		return "float"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	}
	return "int"
}
func pyText(v any) string {
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
	case float64:
		return pyFloat(x)
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			return string(x)
		}
		f, _ := strconv.ParseFloat(string(x), 64)
		return pyFloat(f)
	case *big.Int:
		return x.String()
	case []any:
		parts := make([]string, len(x))
		for i, v := range x {
			parts[i] = pyRepr(v)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := []string{}
		for _, k := range keys {
			parts = append(parts, pyRepr(k)+": "+pyRepr(x[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}
func pyRepr(v any) string {
	if text, ok := v.(string); ok {
		quote := rune('\'')
		if strings.ContainsRune(text, '\'') && !strings.ContainsRune(text, '"') {
			quote = '"'
		}
		var b strings.Builder
		b.WriteRune(quote)
		for _, r := range text {
			switch {
			case r == quote || r == '\\':
				b.WriteRune('\\')
				b.WriteRune(r)
			case r == '\n':
				b.WriteString(`\n`)
			case r == '\r':
				b.WriteString(`\r`)
			case r == '\t':
				b.WriteString(`\t`)
			case unicode.IsPrint(r):
				b.WriteRune(r)
			case r <= 0xff:
				fmt.Fprintf(&b, `\x%02x`, r)
			case r <= 0xffff:
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				fmt.Fprintf(&b, `\U%08x`, r)
			}
		}
		b.WriteRune(quote)
		return b.String()
	}
	return pyText(v)
}
func newFloat(v *big.Int) (float64, big.Accuracy) { return new(big.Float).SetInt(v).Float64() }
func sourcePythonSpace(r rune) bool               { return codepointIn(r, characterProfile.Spaces) }
func numeric(v any) (float64, error) {
	if v == nil {
		return 0, &SourceError{"TypeError", "float() argument must be a string or a real number, not 'NoneType'"}
	}
	switch x := v.(type) {
	case json.Number:
		f, e := strconv.ParseFloat(string(x), 64)
		if errors.Is(e, strconv.ErrRange) {
			return f, nil
		}
		return f, e
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case string:
		return sourceFloat(x)
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		if math.IsInf(f, 0) {
			return 0, &SourceError{"OverflowError", "int too large to convert to float"}
		}
		return f, nil
	}
	return 0, &SourceError{"TypeError", "float() argument must be a string or a real number"}
}
func number(v any) float64 {
	if !truth(v) {
		return 0
	}
	f, e := numeric(v)
	if e != nil {
		panic(e)
	}
	return f
}
func integer(v any) *big.Int {
	if !truth(v) {
		return new(big.Int)
	}
	if n, ok := v.(*big.Int); ok {
		return new(big.Int).Set(n)
	}
	if s, ok := v.(string); ok {
		n, e := sourceIntString(s)
		if e != nil {
			panic(e)
		}
		return n
	}
	if n, ok := v.(json.Number); ok && !strings.ContainsAny(string(n), ".eE") {
		if r, ok := new(big.Int).SetString(string(n), 10); ok {
			return r
		}
	}
	f, e := numeric(v)
	if e != nil {
		panic(e)
	}
	if math.IsNaN(f) {
		panic(&SourceError{"ValueError", "cannot convert float NaN to integer"})
	}
	if math.IsInf(f, 0) {
		panic(&SourceError{"OverflowError", "cannot convert float infinity to integer"})
	}
	r, _ := new(big.Float).SetFloat64(f).Int(nil)
	return r
}
func dataKey(d Data) string               { b, _ := json.Marshal(d["name"]); return string(b) }
func sourceDate(v any) (time.Time, error) { return parseSourceDate(v) }
func pyFloat(f float64) string {
	if math.IsInf(f, 1) {
		return "inf"
	}
	if math.IsInf(f, -1) {
		return "-inf"
	}
	if math.IsNaN(f) {
		return "nan"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	i := strings.LastIndexByte(s, 'e')
	exp, _ := strconv.Atoi(s[i+1:])
	if exp >= -4 && exp < 16 {
		s = strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.ContainsRune(s, '.') {
			s += ".0"
		}
	}
	return s
}
func rounded(v float64, precision int) float64 {
	s := strconv.FormatFloat(v, 'f', precision, 64)
	r, _ := strconv.ParseFloat(s, 64)
	return r
}

// Python3.12 float sum uses a compensated partial, independently observed in
// the exact source image. Integer notification totals use big.Int separately.
func floatSum(values []float64) float64 {
	hi, lo := 0.0, 0.0
	for _, x := range values {
		t := hi + x
		if math.Abs(hi) >= math.Abs(x) {
			lo += (hi - t) + x
		} else {
			lo += (x - t) + hi
		}
		hi = t
	}
	if lo != 0 && !math.IsNaN(lo) {
		return hi + lo
	}
	return hi
}
func recoverError(result *error) {
	if v := recover(); v != nil {
		if e, ok := v.(error); ok {
			*result = e
		} else {
			*result = fmt.Errorf("source failure: %v", v)
		}
	}
}
