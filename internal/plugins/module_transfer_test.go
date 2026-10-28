package plugins

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestModuleTransferInfoFixedSchema(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-info-source-oracle.json")
	// Source uses snake-case field names, so keep those mappings explicit.
	var source struct {
		Cases []struct {
			Name            string
			Input, Output   json.RawMessage
			ValidationError *string `json:"validation_error"`
			WireError       *string `json:"wire_error"`
		}
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || len(source.Cases) != 45 {
		t.Fatal("fixed schema constructor oracle", err)
	}
	decode := func(raw json.RawMessage) any {
		t.Helper()
		var out any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, sample := range source.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			wire, err := ModuleTransferInfoResult(sample.Input)
			if sample.ValidationError != nil || sample.WireError != nil {
				if err == nil {
					t.Fatal("invalid source model or non-standard JSON accepted", string(wire))
				}
				return
			}
			if err != nil {
				t.Fatal("valid pinned constructor rejected", err)
			}
			out, err := DecodeModuleTransferInfo(wire)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decode(encoded), decode(sample.Output)) {
				t.Fatal("fixed complete constructor/default/nullability", string(encoded), string(sample.Output))
			}
		})
	}
}

func TestModuleTransferInfoPlainDictionaryRemainsDistinct(t *testing.T) {
	for _, raw := range []string{`{"success":true}`, `null`, `[]`, `{"__plugin_transfer_info":null}`, `{"__plugin_transfer_info":{},"extra":true}`} {
		if _, err := DecodeModuleTransferInfo(json.RawMessage(raw)); err == nil {
			t.Fatal("non-model promoted", raw)
		}
	}
}
