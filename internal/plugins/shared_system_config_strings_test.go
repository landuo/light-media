package plugins

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSharedSystemConfigSourceStringCodec(t *testing.T) {
	tests := []struct {
		raw     string
		points  []rune
		invalid bool
	}{
		{`"A\ud800B"`, []rune{'A', 0xd800, 'B'}, true},
		{`"\udc00"`, []rune{0xdc00}, true},
		{`"\ud83d\ude00"`, []rune{0x1f600}, false},
		{`"\ud800\ud800\udc00"`, []rune{0xd800, 0x10000}, true},
		{`"�"`, []rune{0xfffd}, false},
		{`"\\ud800"`, []rune{'\\', 'u', 'd', '8', '0', '0'}, false},
		{`"\"\\\/\b\f\n\r\t"`, []rune{'"', '\\', '/', '\b', '\f', '\n', '\r', '\t'}, false},
	}
	for _, test := range tests {
		points, e := DecodeSystemConfigJSONString(json.RawMessage(test.raw))
		if e != nil || !reflect.DeepEqual(points, test.points) {
			t.Fatalf("%s: %v %v", test.raw, points, e)
		}
		raw, e := EncodeSystemConfigJSONString(points)
		if e != nil {
			t.Fatal(e)
		}
		again, e := DecodeSystemConfigJSONString(raw)
		if e != nil || !reflect.DeepEqual(again, points) {
			t.Fatalf("roundtrip %s %v %v", raw, again, e)
		}
		if e = ValidateSystemConfigUTF8JSON(raw); (e != nil) != test.invalid {
			t.Fatalf("render %s: %v", raw, e)
		}
	}
	for _, raw := range []string{`{"\ud800":1}`, `["okay",{"nested":"\udc00"}]`} {
		if ValidateSystemConfigUTF8JSON(json.RawMessage(raw)) == nil {
			t.Fatal("surrogate render accepted", raw)
		}
	}
	for _, raw := range []string{`null`, `1`, `{}`, `"unterminated`, string([]byte{'"', 0xff, '"'})} {
		if _, e := DecodeSystemConfigJSONString(json.RawMessage(raw)); e == nil {
			t.Fatal("invalid source string", raw)
		}
	}
	for _, r := range []rune{-1, 0x110000} {
		if _, e := EncodeSystemConfigJSONString([]rune{r}); e == nil {
			t.Fatal("out of range accepted", r)
		}
	}
}
