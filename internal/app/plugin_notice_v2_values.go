package app

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"

	"light-media/internal/jinja/exec"
	"light-media/internal/notice"
)

// A sealed native value snapshot preserves Python-visible numeric, tuple and
// ordered-dictionary types. A Go map remains an unordered Go map; this codec
// does not claim to recover a Python dictionary's lost insertion order.
type noticeV2Value struct {
	Tag, Text string
	Bool      bool
	Items     []noticeV2Value
	Pairs     []noticeV2Pair
}
type noticeV2Pair struct{ Key, Value noticeV2Value }

func noticeV2Encode(value any) (noticeV2Value, error) { return noticeV2EncodeAt(value, 0) }
func noticeV2EncodeAt(value any, depth int) (noticeV2Value, error) {
	bad := func() (noticeV2Value, error) { return noticeV2Value{}, notice.ErrTemplateDependency }
	if depth > 128 {
		return bad()
	}
	if value == nil {
		return noticeV2Value{Tag: "none"}, nil
	}
	switch v := value.(type) {
	case bool:
		return noticeV2Value{Tag: "bool", Bool: v}, nil
	case string:
		return noticeV2Value{Tag: "str", Text: v}, nil
	case json.Number:
		return noticeV2Value{Tag: "json_number", Text: string(v)}, nil
	case *big.Int:
		if v == nil {
			return bad()
		}
		return noticeV2Value{Tag: "bigint", Text: v.String()}, nil
	case *exec.Dict:
		if v == nil {
			return bad()
		}
		out := noticeV2Value{Tag: "ordered_dict", Pairs: []noticeV2Pair{}}
		for _, p := range v.Pairs {
			if p == nil || p.Key == nil || p.Value == nil {
				return bad()
			}
			key, e := noticeV2EncodeAt(p.Key.Interface(), depth+1)
			if e != nil || !noticeV2Hashable(key) {
				return bad()
			}
			val, e := noticeV2EncodeAt(p.Value.Interface(), depth+1)
			if e != nil {
				return bad()
			}
			out.Pairs = append(out.Pairs, noticeV2Pair{key, val})
		}
		return out, nil
	}
	r := reflect.ValueOf(value)
	out := noticeV2Value{}
	switch r.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return noticeV2Value{Tag: "int", Text: strconv.FormatInt(r.Int(), 10)}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return noticeV2Value{Tag: "uint", Text: strconv.FormatUint(r.Uint(), 10)}, nil
	case reflect.Float32, reflect.Float64:
		return noticeV2Value{Tag: "float64", Text: strconv.FormatUint(math.Float64bits(r.Float()), 16)}, nil
	case reflect.Slice:
		out.Tag, out.Items = "list", []noticeV2Value{}
		if _, ok := value.(exec.Tuple); ok {
			out.Tag = "tuple"
		}
		if r.IsNil() {
			out.Tag += "_nil"
		}
		for i := 0; i < r.Len(); i++ {
			item, e := noticeV2EncodeAt(r.Index(i).Interface(), depth+1)
			if e != nil {
				return bad()
			}
			out.Items = append(out.Items, item)
		}
	case reflect.Map:
		out.Tag, out.Pairs = "map_str", []noticeV2Pair{}
		if r.Type().Key().Kind() == reflect.Int {
			out.Tag = "map_int"
		} else if r.Type().Key().Kind() != reflect.String {
			return bad()
		}
		if r.IsNil() {
			out.Tag += "_nil"
		}
		for _, k := range r.MapKeys() {
			key, e := noticeV2EncodeAt(k.Interface(), depth+1)
			if e != nil {
				return bad()
			}
			val, e := noticeV2EncodeAt(r.MapIndex(k).Interface(), depth+1)
			if e != nil {
				return bad()
			}
			out.Pairs = append(out.Pairs, noticeV2Pair{key, val})
		}
		sort.Slice(out.Pairs, func(i, j int) bool { return out.Pairs[i].Key.Text < out.Pairs[j].Key.Text })
	default:
		return bad()
	}
	return out, nil
}

func noticeV2Hashable(v noticeV2Value) bool {
	switch v.Tag {
	case "none", "bool", "str", "json_number", "bigint", "int", "uint", "float64":
		return true
	case "tuple", "tuple_nil":
		for _, item := range v.Items {
			if !noticeV2Hashable(item) {
				return false
			}
		}
		return true
	}
	return false
}

func noticeV2Decode(value noticeV2Value) (any, error) {
	decoded, e := noticeV2DecodeAt(value, 0)
	if e != nil {
		return nil, e
	}
	canonical, e := noticeV2Encode(decoded)
	if e != nil || !bytes.Equal(mustCleanSeedJSON(canonical), mustCleanSeedJSON(value)) {
		return nil, notice.ErrTemplateDependency
	}
	return decoded, nil
}
func noticeV2DecodeAt(v noticeV2Value, depth int) (any, error) {
	bad := func() (any, error) { return nil, notice.ErrTemplateDependency }
	if depth > 128 {
		return bad()
	}
	switch v.Tag {
	case "none":
		return nil, nil
	case "bool":
		return v.Bool, nil
	case "str":
		return v.Text, nil
	case "json_number":
		var checked any
		decoder := json.NewDecoder(bytes.NewReader([]byte(v.Text)))
		decoder.UseNumber()
		if decoder.Decode(&checked) != nil {
			return bad()
		}
		if _, err := decoder.Token(); err != io.EOF {
			return bad()
		}
		if _, ok := checked.(json.Number); !ok {
			return bad()
		}
		return json.Number(v.Text), nil
	case "bigint":
		n, ok := new(big.Int).SetString(v.Text, 10)
		if !ok {
			return bad()
		}
		return n, nil
	case "int":
		n, e := strconv.ParseInt(v.Text, 10, 64)
		if e != nil || int64(int(n)) != n {
			return bad()
		}
		return int(n), nil
	case "uint":
		n, e := strconv.ParseUint(v.Text, 10, 64)
		if e != nil {
			return bad()
		}
		return n, nil
	case "float64":
		n, e := strconv.ParseUint(v.Text, 16, 64)
		if e != nil {
			return bad()
		}
		return math.Float64frombits(n), nil
	case "list", "tuple", "list_nil", "tuple_nil":
		var out []any
		if v.Tag == "list" || v.Tag == "tuple" {
			out = []any{}
		}
		for _, item := range v.Items {
			x, e := noticeV2DecodeAt(item, depth+1)
			if e != nil {
				return bad()
			}
			out = append(out, x)
		}
		if v.Tag == "tuple" || v.Tag == "tuple_nil" {
			return exec.Tuple(out), nil
		}
		return out, nil
	case "ordered_dict", "map_str", "map_int", "map_str_nil", "map_int_nil":
		dict := exec.NewDict()
		var stringsMap map[string]any
		var intsMap map[int]any
		if v.Tag == "map_str" {
			stringsMap = map[string]any{}
		}
		if v.Tag == "map_int" {
			intsMap = map[int]any{}
		}
		for _, pair := range v.Pairs {
			key, e := noticeV2DecodeAt(pair.Key, depth+1)
			if e != nil {
				return bad()
			}
			value, e := noticeV2DecodeAt(pair.Value, depth+1)
			if e != nil {
				return bad()
			}
			switch v.Tag {
			case "ordered_dict":
				dict.Pairs = append(dict.Pairs, &exec.Pair{Key: exec.AsValue(key), Value: exec.AsValue(value)})
			case "map_str":
				key, ok := key.(string)
				if !ok {
					return bad()
				}
				stringsMap[key] = value
			case "map_int":
				key, ok := key.(int)
				if !ok {
					return bad()
				}
				intsMap[key] = value
			default:
				return bad()
			}
		}
		if v.Tag == "ordered_dict" {
			return dict, nil
		}
		if v.Tag == "map_int" || v.Tag == "map_int_nil" {
			return intsMap, nil
		}
		return stringsMap, nil
	default:
		return bad()
	}
}
