package plugins

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestMutableEventSourceNameAccess(t *testing.T) {
	for _, state := range []string{"missing", "null", "empty", "string", "getter-error", "getter-panic", "dynamic-delete"} {
		t.Run(state, func(t *testing.T) {
			for _, failed := range []bool{false, true} {
				calls := 0
				present := true
				raw, err := HandleMutableEventWithName(json.RawMessage(`{"type":"plugin.data.reset","data":{}}`), func(event *MutableEvent) (json.RawMessage, error) {
					event.Data = json.RawMessage(`null`)
					if state == "dynamic-delete" {
						present = false
					}
					if failed {
						return nil, errors.New("private handler text")
					}
					return nil, nil
				}, func() (json.RawMessage, bool, error) {
					calls++
					if state == "getter-panic" {
						panic("private getter text")
					}
					if state == "getter-error" {
						return nil, false, errors.New("private getter text")
					}
					if !present || state == "missing" {
						return nil, false, nil
					}
					switch state {
					case "null":
						return json.RawMessage(`null`), true, nil
					case "empty":
						return json.RawMessage(`""`), true, nil
					default:
						return json.RawMessage(`"ordinary"`), true, nil
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				event, isFailed, tag, err := DecodeMutableEventSnapshot(raw)
				if err != nil || !tag || isFailed != failed || string(event.Data) != "null" {
					t.Fatal(string(raw), err)
				}
				if (!failed && calls != 0) || (failed && calls != 1) {
					t.Fatal("getter timing", calls, failed)
				}
				name, err := DecodeMutableEventErrorName(raw)
				if err != nil {
					t.Fatal(err)
				}
				want := "missing"
				if failed {
					switch state {
					case "null", "empty", "string":
						want = "value"
					case "getter-error", "getter-panic":
						want = "failed"
					}
				}
				if name.State != want {
					t.Fatal(string(raw), want)
				}
			}
		})
	}
}

func TestMutableEventSourceNameWireValidation(t *testing.T) {
	for _, raw := range []string{`{"source_error_name":null}`, `{"source_error_name":{"state":"missing","value":null}}`, `{"source_error_name":{"state":"value"}}`, `{"source_error_name":{"state":"invented"}}`} {
		if _, err := DecodeMutableEventErrorName(json.RawMessage(raw)); err == nil {
			t.Fatal("accepted malformed extension", raw)
		}
	}
	raw, err := HandleMutableEvent(json.RawMessage(`{"type":"plugin.data.reset","data":{}}`), func(*MutableEvent) (json.RawMessage, error) { return nil, errors.New("owned") })
	if err != nil {
		t.Fatal(err)
	}
	name, err := DecodeMutableEventErrorName(raw)
	if err != nil || name.State != "missing" {
		t.Fatal("old worker fallback changed", string(raw), err)
	}
}
