package app

import (
	"encoding/json"
	"light-media/internal/releasemeta"
	"os"
	"testing"
)

func TestOriginalHostMediaBangumiFixedSourceModel(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-integration-20261031/source-models/bangumi-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name     string
			Input    json.RawMessage
			Expected json.RawMessage
			Error    string
		}
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: "legacy"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			value, _ := decodeSourceJSON(row.Input)
			fields, err := originalHostMediaBangumiFields(sourceMap(value), parser)
			if row.Error != "" {
				if err == nil {
					t.Fatalf("expected %s", row.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			actual, err := originalHostMediaSourceSerialize(fields)
			if err != nil {
				t.Fatal(err)
			}
			gotValue, _ := decodeSourceJSON(actual)
			got := sourceMap(gotValue)
			delete(got, "recognize_cache_hit")
			want, _ := decodeSourceJSON(row.Expected)
			if !originalBaseValueEqual(got, want) {
				for key, w := range sourceMap(want) {
					if !originalBaseValueEqual(got[key], w) {
						t.Errorf("%s got=%#v want=%#v", key, got[key], w)
					}
				}
			}
		})
	}
}
