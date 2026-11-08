package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func independentCodecOld(f Frame) CodecCAFrame {
	v := CodecCAFrame{Protocol: f.Protocol, ID: f.ID, Type: f.Type, CodecCAIdentity: CodecCAIdentity{PluginID: f.PluginID, InstanceID: f.InstanceID, Version: f.Version}, Method: f.Method, Payload: f.Payload, Result: f.Result}
	if f.Error != nil {
		v.Error = &CodecCAReplyError{Code: f.Error.Code}
	}
	return v
}
func independentCodecNew(f CodecCAFrame) Frame {
	v := Frame{Protocol: f.Protocol, ID: f.ID, Type: f.Type, Identity: Identity{PluginID: f.PluginID, InstanceID: f.InstanceID, Version: f.Version}, Method: f.Method, Payload: f.Payload, Result: f.Result}
	if f.Error != nil {
		v.Error = &ReplyError{Code: f.Error.Code}
	}
	return v
}
func independentCodecCode(e error) string {
	if e == nil {
		return ""
	}
	var n *Error
	if errors.As(e, &n) {
		return n.Code
	}
	var o *CodecCAError
	if errors.As(e, &o) {
		return o.Code
	}
	return fmt.Sprintf("nonlocal:%T", e)
}
func independentCodecCompareError(t *testing.T, old, new error) {
	t.Helper()
	if (old == nil) != (new == nil) || independentCodecCode(old) != independentCodecCode(new) {
		t.Fatalf("source success/code changed: old%v new%v", old, new)
	}
}
func independentCodecSeed() Frame {
	return Frame{Protocol: 99, ID: "retained", Type: "result", Identity: Identity{"Prior", "Prior", "0"}, Result: json.RawMessage(`{"retained":true}`)}
}

func TestIndependentCodecHistoricalMarshal(t *testing.T) {
	values := map[string]json.RawMessage{"nil": nil, "empty": {}, "null": []byte(`null`), "spaced": []byte(" \n{ \"x\": \"<>&\" } \t"), "literalUnicode": []byte("\"<>&\u2028\u2029\""), "surrogate": []byte(`"\ud800"`), "invalidUTF8": {'"', 255, '"'}, "truncated": []byte(`{"x":`), "multiple": []byte(`{} null`), "scalar": []byte(`-1e2`), "deep9998": []byte(strings.Repeat("[", 9998) + "0" + strings.Repeat("]", 9998)), "deep9999": []byte(strings.Repeat("[", 9999) + "0" + strings.Repeat("]", 9999)), "deep10000": []byte(strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000))}
	for _, kind := range []string{"call", "host_call", "result", "host_result"} {
		for name, payload := range values {
			t.Run(kind+"/"+name, func(t *testing.T) {
				f := codecFrame()
				f.Type = kind
				f.Payload = payload
				if strings.Contains(kind, "result") {
					f.Method = ""
					f.Result = payload
					f.Payload = nil
				}
				old := independentCodecOld(f)
				expected, oe := old.MarshalJSON()
				actual, ne := f.MarshalJSON()
				independentCodecCompareError(t, oe, ne)
				if !bytes.Equal(expected, actual) {
					t.Fatal("direct source wire bytes differ")
				}
				expected, oe = json.Marshal(old)
				actual, ne = json.Marshal(f)
				independentCodecCompareError(t, oe, ne)
				if !bytes.Equal(expected, actual) {
					t.Fatal("standard source wire bytes differ")
				}
			})
		}
	}
	for _, kind := range []string{"call", "host_call", "result", "host_result", "bad"} {
		for _, id := range []string{"", strings.Repeat("x", 129), "<>&\u2028\u2029"} {
			t.Run(kind+"/id/"+fmt.Sprint(len(id)), func(t *testing.T) {
				f := codecFrame()
				f.ID = id
				f.Type = kind
				f.Method = ""
				f.Payload = nil
				f.Error = &ReplyError{Code: "FAILED"}
				if kind == "call" || kind == "host_call" {
					f.Method = "invoke"
				}
				a, ae := f.MarshalJSON()
				b, be := independentCodecOld(f).MarshalJSON()
				independentCodecCompareError(t, be, ae)
				if !bytes.Equal(a, b) {
					t.Fatal("invalid source output changed")
				}
			})
		}
	}
}

func TestIndependentCodecHistoricalDecodeReceiver(t *testing.T) {
	good, err := independentCodecOld(codecFrame()).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	inputs := map[string][]byte{"good": good, "unknownOuter": bytes.Replace(good, []byte(`"jsonrpc":`), []byte(`"extra":1,"jsonrpc":`), 1), "unknownValue": bytes.Replace(good, []byte(`"protocol":`), []byte(`"extra":1,"protocol":`), 1), "trailingNull": append(bytes.Clone(good), []byte(` null`)...), "trailingObject": append(bytes.Clone(good), []byte(` {}`)...), "trailingGarbage": append(bytes.Clone(good), '!'), "truncated": good[:len(good)-1], "null": []byte(`null`), "array": []byte(`[]`), "empty": nil, "duplicateID": bytes.Replace(good, []byte(`"id":"logical"`), []byte(`"id":"old","id":"logical"`), 1), "caseFold": bytes.Replace(good, []byte(`"jsonrpc":`), []byte(`"JSONRPC":`), 1), "invalidUTF8": bytes.Replace(good, []byte(`nested`), []byte{255}, 1), "wrongVersion": bytes.Replace(good, []byte(`"2.0"`), []byte(`"1.0"`), 1), "badMethod": bytes.Replace(good, []byte(`plugin.invoke`), []byte(`wrong.invoke`), 1), "wrongProtocol": bytes.Replace(good, []byte(`"protocol":1`), []byte(`"protocol":2`), 1)}
	for name, raw := range inputs {
		t.Run(name, func(t *testing.T) {
			for _, seed := range []Frame{{}, independentCodecSeed()} {
				a, b := seed, independentCodecOld(seed)
				ae, be := a.UnmarshalJSON(raw), b.UnmarshalJSON(raw)
				independentCodecCompareError(t, be, ae)
				if !reflect.DeepEqual(a, independentCodecNew(b)) {
					t.Fatalf("direct receiver changed source%+v current%+v", b, a)
				}
				a, b = seed, independentCodecOld(seed)
				ae, be = strictJSON(raw, &a), codecCAStrict(raw, &b)
				independentCodecCompareError(t, be, ae)
				if !reflect.DeepEqual(a, independentCodecNew(b)) {
					t.Fatal("strict receiver changed")
				}
				a, b = seed, independentCodecOld(seed)
				ae, be = decodeTransportFrame(raw, &a), codecCAStrict(raw, &b)
				independentCodecCompareError(t, be, ae)
				if !reflect.DeepEqual(a, independentCodecNew(b)) {
					t.Fatalf("transport failure receiver changed source%+v current%+v", b, a)
				}
			}
		})
	}
	// Premise is independently historical: the outer strict decoder mutates the
	// receiver before rejecting a second JSON value; direct Unmarshal must not.
	raw := inputs["trailingNull"]
	s, d := independentCodecOld(independentCodecSeed()), independentCodecOld(independentCodecSeed())
	se, de := codecCAStrict(raw, &s), d.UnmarshalJSON(raw)
	if se == nil || de == nil || s.ID != "logical" || d.ID != "retained" {
		t.Fatal("historical receiver premise changed")
	}
}

func TestIndependentCodecCleanupAdditiveContract(t *testing.T) {
	// This is an explicit f7 source contract, not old codec equality or manager authority.
	for _, kind := range []string{"call", "host_call", "result", "host_result"} {
		for _, root := range []string{"Root_1", "Root.one", "1root", "", strings.Repeat("A", 129)} {
			for _, method := range []string{"step", "init", "handshake", "close", "stop_service", "stop", "transport.chunk"} {
				name := kind + "/" + method + "/root" + fmt.Sprint(len(root))
				t.Run(name, func(t *testing.T) {
					f := codecFrame()
					f.Type = kind
					f.CleanupRoot = root
					if strings.Contains(kind, "result") {
						f.Method = ""
						f.Result = f.Payload
						f.Payload = nil
					} else {
						f.Method = method
					}
					want := root == "" || (kind == "call" && root == "Root_1" && method == "step")
					raw, err := f.MarshalJSON()
					if (err == nil) != want {
						t.Fatalf("explicit cleanup acceptance want%v err%v", want, err)
					}
					if err == nil {
						var decoded Frame
						if err = decoded.UnmarshalJSON(raw); err != nil || decoded.CleanupRoot != root {
							t.Fatal("cleanup root lost during round trip", err)
						}
						if root != "" && !bytes.Contains(raw, []byte(`"cleanup_root":"Root_1"`)) {
							t.Fatal("cleanup root missing from wire")
						}
						if root == "" {
							a, oe := independentCodecOld(f).MarshalJSON()
							independentCodecCompareError(t, oe, err)
							if !bytes.Equal(a, raw) {
								t.Fatal("empty root changed legacy wire")
							}
						}
					}
				})
			}
		}
	}
	valid := `{"jsonrpc":"2.0","id":"x","method":"plugin.step","params":{"protocol":1,"plugin_id":"Fixture","instance_id":"Fixture","plugin_version":"1.2.3","cleanup_root":"Root_1","payload":null}}`
	for name, raw := range map[string]string{"valid": valid, "badRoot": strings.Replace(valid, "Root_1", "Root.one", 1), "reservedMethod": strings.Replace(valid, "plugin.step", "plugin.stop", 1), "hostCall": strings.Replace(valid, "plugin.step", "host.step", 1), "unknown": strings.Replace(valid, `"protocol":1`, `"protocol":1,"alien":1`, 1)} {
		t.Run("decode/"+name, func(t *testing.T) {
			old, seed := independentCodecOld(independentCodecSeed()), independentCodecSeed()
			if old.UnmarshalJSON([]byte(raw)) == nil {
				t.Fatal("historical decoder unexpectedly knows cleanup field")
			}
			before := seed
			err := seed.UnmarshalJSON([]byte(raw))
			if name == "valid" {
				if err != nil || seed.CleanupRoot != "Root_1" {
					t.Fatal("valid source cleanup extension failed", err)
				}
			} else if err == nil || !reflect.DeepEqual(seed, before) {
				t.Fatal("invalid cleanup accepted or modified receiver", err)
			}
		})
	}
}
