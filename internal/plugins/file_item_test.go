package plugins

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestModuleFileItemConstructorFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-info-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name            string
			Input, Output   map[string]json.RawMessage
			ValidationError any `json:"validation_error"`
			WireError       any `json:"wire_error"`
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("fixed FileItem constructor in original TransferInfo oracle", err)
	}
	checked := 0
	for _, sample := range oracle.Cases {
		if !strings.HasPrefix(sample.Name, "item_") {
			continue
		}
		for _, key := range []string{"fileitem", "target_item", "target_diritem"} {
			value := sample.Input[key]
			if len(value) == 0 || string(value) == "null" {
				continue
			}
			checked++
			t.Run(sample.Name+"/"+key, func(t *testing.T) {
				actual, err := NewModuleFileItem(value)
				bad := sample.ValidationError != nil || sample.WireError != nil
				if (err != nil) != bad {
					t.Fatal("fixed constructor / finite JSON transport", err, sample)
				}
				if err != nil {
					return
				}
				var expected map[string]json.RawMessage
				if json.Unmarshal(sample.Output[key], &expected) != nil {
					t.Fatal("fixed FileItem output missing", sample.Name, key)
				}
				if len(actual.Fields) != len(expected) {
					t.Fatal("constructor defaults and ignored unknown fields", actual.Fields, expected)
				}
				for name, want := range expected {
					var a, b any
					left, right := json.NewDecoder(bytes.NewReader(actual.Fields[name])), json.NewDecoder(bytes.NewReader(want))
					left.UseNumber()
					right.UseNumber()
					if left.Decode(&a) != nil || right.Decode(&b) != nil {
						t.Fatal("constructor field", name)
					}
					x, _ := json.Marshal(a)
					y, _ := json.Marshal(b)
					if string(x) != string(y) {
						t.Fatal("fixed constructor field", name, string(x), string(y))
					}
				}
			})
		}
	}
	if checked < 5 {
		t.Fatal("fixed FileItem source coverage missing", checked)
	}
}
