package plugins

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	old "light-media/internal/plugins/testdata/legacywire"
)

func frozenRPCFrame(f Frame) old.Frame {
	o := old.Frame{Protocol: f.Protocol, ID: f.ID, Type: f.Type, Identity: old.Identity{PluginID: f.PluginID, InstanceID: f.InstanceID, Version: f.Version}, Method: f.Method, Payload: f.Payload, Result: f.Result}
	if f.Error != nil {
		o.Error = &old.ReplyError{Code: f.Error.Code}
	}
	return o
}
func currentRPCFrame(o old.Frame) Frame {
	f := Frame{Protocol: o.Protocol, ID: o.ID, Type: o.Type, Identity: Identity{PluginID: o.PluginID, InstanceID: o.InstanceID, Version: o.Version}, Method: o.Method, Payload: o.Payload, Result: o.Result}
	if o.Error != nil {
		f.Error = &ReplyError{Code: o.Error.Code}
	}
	return f
}
func rpcEquivalenceCode(err error) string {
	if err == nil {
		return ""
	}
	var a *Error
	if errors.As(err, &a) {
		return a.Code
	}
	var b *old.Error
	if errors.As(err, &b) {
		return b.Code
	}
	return fmt.Sprintf("nonlocal:%T", err)
}

func TestJSONRPCValidationFrozenDependency(t *testing.T) {
	data, err := os.ReadFile("testdata/legacywire/jsonrpc.go")
	if err != nil {
		t.Fatal(err)
	}
	// Immutable legacy implementation, independent of the candidate Frame codec.
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != "b9646bbfecc56ea2006ebd5d0009f1bd057546b7852558c2755c0715e1ee0593" {
		t.Fatal("legacy oracle changed", got)
	}
}

func TestJSONRPCValidationMarshalLegacyEquivalence(t *testing.T) {
	values := map[string]json.RawMessage{
		"missing": nil, "empty": {}, "whitespace": json.RawMessage(" \n\t"), "null": json.RawMessage(`null`), "boolean": json.RawMessage(`true`), "number": json.RawMessage(`-0.25e+2`), "string": json.RawMessage(`"<>&\u2028\ud800"`),
		"literal_unicode":   json.RawMessage("\"<>&\u2028\u2029\""),
		"escaped_nested":    json.RawMessage(`{"s":"\\\"<>&\u2028\u2029","a":[{"x":"\\"}]}`),
		"braces_in_string":  json.RawMessage(`"` + strings.Repeat("{[", 10000) + `"`),
		"nested_depth_9998": json.RawMessage(strings.Repeat("[", 9998) + "0" + strings.Repeat("]", 9998)),
		"nested_depth_9999": json.RawMessage(strings.Repeat("[", 9999) + "0" + strings.Repeat("]", 9999)),
		"nested":            json.RawMessage(`{"a":[1,true,null,{"b":"x"}]}`), "duplicate": json.RawMessage(`{"x":1,"x":2}`), "leading_trailing_space": json.RawMessage(" \n [1,2] \t"),
		"invalid_utf8": json.RawMessage{'"', 255, '"'}, "truncated": json.RawMessage(`{"a":`), "multiple_values": json.RawMessage(`null null`), "bad_escape": json.RawMessage(`"\q"`), "nonfinite": json.RawMessage(`NaN`), "extra_comma": json.RawMessage(`[1,]`),
		"depth_limit": json.RawMessage(strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000)), "beyond_depth": json.RawMessage(strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)),
	}
	for _, kind := range []string{"call", "host_call", "result", "host_result"} {
		for name, value := range values {
			t.Run(kind+"/"+name, func(t *testing.T) {
				f := Frame{Protocol: 1, ID: "test", Type: kind, Identity: Identity{"Fixture", "Fixture", "1.2.3"}, Result: value}
				if kind == "call" || kind == "host_call" {
					f.Method = "invoke"
					f.Payload, f.Result = value, nil
				}
				o := frozenRPCFrame(f)
				a, ae := f.MarshalJSON()
				b, be := o.MarshalJSON()
				if !bytes.Equal(a, b) || rpcEquivalenceCode(ae) != rpcEquivalenceCode(be) {
					t.Fatalf("direct acceptance/output/code changed: candidate=%q/%v legacy=%q/%v", a, ae, b, be)
				}
				a, ae = json.Marshal(f)
				b, be = json.Marshal(o)
				if !bytes.Equal(a, b) || rpcEquivalenceCode(ae) != rpcEquivalenceCode(be) {
					t.Fatalf("standard acceptance/output/code changed: candidate=%q/%v legacy=%q/%v", a, ae, b, be)
				}
			})
		}
	}
}

func TestJSONRPCValidationUnmarshalLegacyEquivalence(t *testing.T) {
	prefix := `{"jsonrpc":"2.0","id":"test","result":{"protocol":1,"plugin_id":"Fixture","instance_id":"Fixture","plugin_version":"1.2.3","kind":"result"`
	cases := map[string][]byte{}
	for name, value := range map[string]string{"null": "null", "scalar": "3", "empty_string": `""`, "nested": `{"a":[null,true,1]}`, "duplicate_nested": `{"x":1,"x":2}`, "whitespace": " \n null \t", "invalid": "[1,]", "empty": "", "multiple": "null null", "invalid_utf8": string([]byte{'"', 255, '"'}), "lone_surrogate": `"\ud800"`, "depth_limit": strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000)} {
		cases[name] = []byte(prefix + `,"payload":` + value + `}}`)
	}
	cases["missing"] = []byte(prefix + `}}`)
	cases["unknown_value"] = []byte(prefix + `,"payload":null,"unknown":1}}`)
	cases["duplicate_payload_last_null"] = []byte(prefix + `,"payload":1,"payload":null}}`)
	cases["duplicate_payload_last_object"] = []byte(prefix + `,"payload":null,"payload":{"x":2}}`)
	cases["duplicate_result"] = []byte(prefix + `,"payload":null},"result":{"protocol":1,"kind":"result","payload":1}}`)
	cases["case_fold_payload"] = []byte(prefix + `,"PAYLOAD":null}}`)
	cases["error"] = []byte(`{"jsonrpc":"2.0","id":"test","error":{"code":-32000,"message":"plugin operation failed","data":{"protocol":1,"kind":"result","code":"FAILED"}}}`)
	good := []byte(prefix + `,"payload":null}}`)
	cases["trailing_null"] = append(bytes.Clone(good), []byte(" null")...)
	cases["trailing_garbage"] = append(bytes.Clone(good), '!')
	seed := Frame{Protocol: 99, ID: "retained", Type: "result", Result: json.RawMessage(`"retained"`), Identity: Identity{"Old", "Old", "0"}}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			for _, initial := range []Frame{{}, seed} {
				a, b := initial, frozenRPCFrame(initial)
				ae, be := a.UnmarshalJSON(raw), b.UnmarshalJSON(raw)
				if rpcEquivalenceCode(ae) != rpcEquivalenceCode(be) || !reflect.DeepEqual(a, currentRPCFrame(b)) {
					t.Fatalf("direct acceptance/code/receiver differs: candidate=%+v/%v legacy=%+v/%v", a, ae, b, be)
				}
				a, b = initial, frozenRPCFrame(initial)
				ae, be = json.Unmarshal(raw, &a), json.Unmarshal(raw, &b)
				if (ae == nil) != (be == nil) || rpcEquivalenceCode(ae) != rpcEquivalenceCode(be) || !reflect.DeepEqual(a, currentRPCFrame(b)) {
					t.Fatalf("standard acceptance/code/receiver differs: candidate=%+v/%v legacy=%+v/%v", a, ae, b, be)
				}
				a, b = initial, frozenRPCFrame(initial)
				ae, be = strictJSON(raw, &a), strictJSON(raw, &b)
				if rpcEquivalenceCode(ae) != rpcEquivalenceCode(be) || !reflect.DeepEqual(a, currentRPCFrame(b)) {
					t.Fatalf("strict acceptance/code/receiver differs: candidate=%+v/%v legacy=%+v/%v", a, ae, b, be)
				}
			}
		})
	}
}

func TestJSONRPCEnvelopeMetadataAndErrorLegacyEquivalence(t *testing.T) {
	for _, kind := range []string{"call", "host_call", "result", "host_result"} {
		for _, id := range []string{"test", "quote\"\\<>&\u2028\u2029", string([]byte{255}), "", strings.Repeat("x", 129)} {
			for _, fail := range []bool{false, true} {
				f := Frame{Protocol: 1, ID: id, Type: kind, Identity: Identity{"<>&\u2028", "quote\"\\\u2029", string([]byte{255})}}
				if kind == "call" || kind == "host_call" {
					f.Method = "invoke"
					f.Payload = json.RawMessage(`{"x":"<>&"}`)
				} else {
					f.Result = json.RawMessage(`null`)
				}
				if fail {
					f.Result = nil
					f.Error = &ReplyError{Code: "FAILED<>&\u2028\u2029"}
				}
				a, ae := f.MarshalJSON()
				b, be := frozenRPCFrame(f).MarshalJSON()
				if !bytes.Equal(a, b) || rpcEquivalenceCode(ae) != rpcEquivalenceCode(be) || fmt.Sprint(ae) != fmt.Sprint(be) {
					t.Fatalf("metadata/error mismatch kind=%s failed=%v candidate=%q/%v legacy=%q/%v", kind, fail, a, ae, b, be)
				}
			}
		}
	}
}

func TestJSONRPCEnvelopeStandardDepthBoundary(t *testing.T) {
	for _, n := range []int{9998, 9999, 10000, 10001} {
		raw := []byte(strings.Repeat("[", n) + "0" + strings.Repeat("]", n))
		if json.Valid(raw) != (n <= 10000) {
			t.Fatalf("encoding/json depth assumption changed at %d; review envelope fast path", n)
		}
	}
}
