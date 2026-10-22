package app

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"light-media/internal/notice"
)

func TestSiteMessagePythonStringOrderedJSONValues(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`"你好\n🦋"`, "你好\n🦋"},
		{`null`, "None"},
		{`[true,false,null]`, "[True, False, None]"},
		{`["a'b","a\"b","a'b\"c","\u0000\u0085\u200b"]`, `["a'b", 'a"b', 'a\'b"c', '\x00\x85\u200b']`},
		{`{"z":1,"a":{"二":"值","一":null},"m":["x",{"b":true,"a":false}]}`, "{'z': 1, 'a': {'二': '值', '一': None}, 'm': ['x', {'b': True, 'a': False}]}"},
		{`{"z":1,"a":2,"z":3}`, "{'z': 3, 'a': 2}"},
		{`[1e3,1.00,-0,-0.0,0.00001,1e16]`, "[1000.0, 1.0, 0, -0.0, 1e-05, 1e+16]"},
		{`[9007199254740993,9223372036854775808,-9223372036854775809]`, "[9007199254740993, 9223372036854775808, -9223372036854775809]"},
		{`[1e999,-1e999,1e-999]`, "[inf, -inf, 0.0]"},
	} {
		got, err := siteMessagePythonString(json.RawMessage(tc.raw))
		if err != nil || got != tc.want {
			t.Fatalf("str(%s): got %q, want %q, err %v", tc.raw, got, tc.want, err)
		}
	}
	for _, raw := range []string{``, `{`, `[] true`, `[1,]`} {
		if _, err := siteMessagePythonString(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid JSON %q", raw)
		}
	}
}

func TestSiteMessageSourceSQLNumericTypesAndRange(t *testing.T) {
	for _, tc := range []struct{ input, want any }{
		{"source", "source"}, {true, true}, {false, false}, {nil, nil},
		{json.Number("1e0"), float64(1)}, {json.Number("1.5"), float64(1.5)},
		{json.Number("9007199254740993"), int64(9007199254740993)},
		{json.Number("9223372036854775807"), int64(9223372036854775807)},
		{json.Number("-9223372036854775808"), int64(-9223372036854775808)},
		{json.Number("-0"), int64(0)}, {int(7), int64(7)},
	} {
		got, err := siteMessageSourceSQLValue(tc.input)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("SQL %v: got %#v (%T), want %#v (%T), err %v", tc.input, got, got, tc.want, tc.want, err)
		}
	}
	for _, tc := range []struct {
		input any
		class string
	}{
		{json.Number("9223372036854775808"), "OverflowError"},
		{json.Number("-9223372036854775809"), "OverflowError"},
		{[]any{"x"}, "ProgrammingError"}, {map[string]any{"x": 1}, "ProgrammingError"},
	} {
		_, err := siteMessageSourceSQLValue(tc.input)
		var source notice.SourceError
		if !errors.As(err, &source) || source.Class != tc.class {
			t.Fatalf("SQL %v: expected %s, got %v", tc.input, tc.class, err)
		}
	}
	got, err := siteMessageSourceSQLValue(json.Number("-0.0"))
	if err != nil || !math.Signbit(got.(float64)) {
		t.Fatal("negative floating zero lost", got, err)
	}
	got, err = siteMessageSourceSQLValue(json.Number("1e999"))
	if err != nil || !math.IsInf(got.(float64), 1) {
		t.Fatal("JSON floating overflow changed", got, err)
	}
}
