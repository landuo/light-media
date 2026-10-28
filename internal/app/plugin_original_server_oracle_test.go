package app

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestOriginalPluginRatingFixedPydanticOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-market/server-oracle.json")
	var oracle struct {
		Pydantic string `json:"pydantic_version"`
		Cases    []struct {
			Kind         string
			Input, Value json.RawMessage
			Success      bool
		} `json:"schema_cases"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Pydantic != "2.13.4" || len(oracle.Cases) < 33 {
		t.Fatal("fixed Pydantic source oracle unavailable", err)
	}
	for i, row := range oracle.Cases {
		var actual any
		accepted := false
		if row.Kind == "request" {
			var fields map[string]json.RawMessage
			if json.Unmarshal(row.Input, &fields) == nil {
				value, err := originalRatingFloat(fields["rating"])
				accepted = err == nil && validOriginalPluginRating(value)
				if accepted {
					actual = map[string]float64{"rating": value}
				}
			}
		} else {
			var value originalPluginRating
			err := json.Unmarshal(row.Input, &value)
			accepted = err == nil
			if accepted {
				actual = value
			}
		}
		if accepted != row.Success {
			t.Fatalf("fixed schema case%d success=%t expected=%t", i, accepted, row.Success)
		}
		if accepted {
			encoded, _ := json.Marshal(actual)
			var got, want any
			_ = json.Unmarshal(encoded, &got)
			_ = json.Unmarshal(row.Value, &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("fixed schema case%d result differs: got%s want%s", i, encoded, row.Value)
			}
			if row.Kind == "response" {
				var actualFields, expectedFields map[string]json.RawMessage
				_ = json.Unmarshal(encoded, &actualFields)
				_ = json.Unmarshal(row.Value, &expectedFields)
				if !bytes.Equal(actualFields["rating_count"], expectedFields["rating_count"]) {
					t.Fatalf("fixed schema case%d lost integer precision", i)
				}
			}
		}
	}
}
