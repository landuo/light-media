package app

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"reflect"
	"testing"

	"light-media/internal/jinja/exec"
	"light-media/internal/notice"
)

func TestPluginNoticeV2TypedContextColdCodec(t *testing.T) {
	ordered := exec.NewDict()
	for _, p := range []struct{ k, v any }{{2, "second"}, {"2", "string"}, {true, exec.Tuple{1, "tuple"}}, {nil, math.Float64frombits(0x7ff8000000000042)}} {
		ordered.Pairs = append(ordered.Pairs, &exec.Pair{Key: exec.AsValue(p.k), Value: exec.AsValue(p.v)})
	}
	huge, _ := new(big.Int).SetString("900719925474099312345678901234567890", 10)
	input := map[string]any{"ordered": ordered, "negative_zero": math.Copysign(0, -1), "huge": huge, "json_number": json.Number("1e99999"), "tuple": exec.Tuple{}, "tuple_nil": exec.Tuple(nil), "list": []any{}, "list_nil": []any(nil), "map_nil": map[int]any(nil), "integer_map": map[int]any{0: "special", 3: "third"}}
	encoded, e := noticeV2Encode(input)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(encoded)
	if e != nil {
		t.Fatal(e)
	}
	var cold noticeV2Value
	if e = json.Unmarshal(raw, &cold); e != nil {
		t.Fatal(e)
	}
	got, e := noticeV2Decode(cold)
	if e != nil {
		t.Fatal(e)
	}
	out := got.(map[string]any)
	if out["huge"].(*big.Int).Cmp(huge) != 0 || math.Float64bits(out["negative_zero"].(float64)) != math.Float64bits(math.Copysign(0, -1)) {
		t.Fatal("numeric identity lost")
	}
	d := out["ordered"].(*exec.Dict)
	if len(d.Pairs) != 4 || d.Pairs[0].Key.Interface() != 2 || d.Pairs[1].Key.Interface() != "2" || d.Pairs[2].Key.Interface() != true || math.Float64bits(d.Pairs[3].Value.Interface().(float64)) != 0x7ff8000000000042 {
		t.Fatal("ordered key types or IEEE NaN payload lost")
	}
	if reflect.ValueOf(out["tuple"]).IsNil() || !reflect.ValueOf(out["tuple_nil"]).IsNil() || reflect.ValueOf(out["list"]).IsNil() || !reflect.ValueOf(out["list_nil"]).IsNil() || !reflect.ValueOf(out["map_nil"]).IsNil() {
		t.Fatal("None and empty collection conflated")
	}
	again, e := noticeV2Encode(got)
	if e != nil || !reflect.DeepEqual(encoded, again) {
		t.Fatal("cold value changed canonical receipt", e)
	}
}

func TestPluginNoticeV2TypedContextRejectsUnknownOrMalformed(t *testing.T) {
	for _, input := range []any{func() {}, struct{ Field string }{"not a Python class"}, map[float64]any{1: "unsupported"}} {
		if _, e := noticeV2Encode(input); !errors.Is(e, notice.ErrTemplateDependency) {
			t.Fatal("unknown type accepted", e)
		}
	}
	for _, input := range []noticeV2Value{{Tag: "json_number", Text: "1 2"}, {Tag: "json_number", Text: "true"}, {Tag: "bigint", Text: "+1"}, {Tag: "int", Text: "01"}, {Tag: "none", Bool: true}, {Tag: "list_nil", Items: []noticeV2Value{{Tag: "int", Text: "1"}}}, {Tag: "ordered_dict", Pairs: []noticeV2Pair{{Key: noticeV2Value{Tag: "list"}, Value: noticeV2Value{Tag: "none"}}}}} {
		if _, e := noticeV2Decode(input); !errors.Is(e, notice.ErrTemplateDependency) {
			t.Fatal("malformed snapshot accepted", input, e)
		}
	}
}
