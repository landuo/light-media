package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestSharedOCRValuesAndTypedSourceErrors(t *testing.T) {
	invalid := string([]byte{0xff})
	for _, input := range []OCRRequest{{ImageURL: &invalid}, {ImageBase64: &invalid}, {Cookie: &invalid}, {UserAgent: &invalid}} {
		calls := 0
		_, err := (SharedOCR{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, nil }}).GetCaptchaText(context.Background(), input)
		if err == nil || calls != 0 {
			t.Fatalf("invalid UTF8 must fail before host: %v calls=%d", err, calls)
		}
	}
	values := []OCRValue{
		{Kind: "null"}, {Kind: "bool"}, {Kind: "bool", Bool: true},
		{Kind: "int", Number: "123456789012345678901234567890"},
		{Kind: "float", Number: "nan"}, {Kind: "float", Number: "-0"},
		{Kind: "string", Points: []rune{'A', 0xd800, 0x1f600}},
		{Kind: "list", Items: []OCRValue{{Kind: "int", Number: "1"}}},
		{Kind: "object", Entries: []OCREntry{{Key: []rune{0xd800}, Value: OCRValue{Kind: "bool", Bool: true}}}},
	}
	for _, value := range values {
		if err := value.Validate(); err != nil {
			t.Fatal(err)
		}
		host := func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			if method != SharedOCRGetCaptchaText {
				t.Fatalf("method %q", method)
			}
			return json.Marshal(OCRReply{Value: &value})
		}
		got, err := (SharedOCR{Host: host}).GetCaptchaText(context.Background(), OCRRequest{})
		if err != nil || !reflect.DeepEqual(got, value) {
			t.Fatalf("value %#v => %#v %v", value, got, err)
		}
	}
	for _, class := range []string{"JSONDecodeError", "AttributeError", "ValueError", "UnicodeDecodeError", "UnicodeEncodeError", "LookupError", "RecursionError"} {
		s := SharedOCR{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(OCRReply{SourceError: class})
		}}
		_, err := s.GetCaptchaText(context.Background(), OCRRequest{})
		var source *OCRSourceError
		if !errors.As(err, &source) || source.Class != class {
			t.Fatalf("class %s: %v", class, err)
		}
	}
	for _, raw := range []string{`{}`, `{"source_error":"InternalSecret"}`, `{"value":{"kind":"bogus"}}`, `{"value":{"kind":"int","number":"01"}}`, `{"value":{"kind":"string","points":[1114112]}}`, `{"source_error":"JSONDecodeError","value":{"kind":"null"}}`} {
		_, err := (SharedOCR{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}}).GetCaptchaText(context.Background(), OCRRequest{})
		if err == nil {
			t.Fatalf("invalid reply accepted: %s", raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (SharedOCR{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		t.Fatal("canceled invoked host")
		return nil, nil
	}}).GetCaptchaText(ctx, OCRRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, value := range []OCRValue{{Kind: "null"}, {Kind: "bool"}, {Kind: "int", Number: "0"}, {Kind: "float", Number: "-0"}, {Kind: "string"}, {Kind: "list"}, {Kind: "object"}} {
		if value.Truthy() {
			t.Fatalf("falsey %#v", value)
		}
	}
}
