package app

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
)

// Values are produced by the unmodified fixed manager and route functions,
// with real FastAPI registration and owned endpoints; not target-derived.
func TestOriginalAPI35FixedRouteDefaultsAnonymousAndFailures(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-api/source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name   string
			Input  []json.RawMessage
			Routes []struct {
				Path    string
				Methods []string
				Auth    string
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 9 {
		t.Fatal("fixed route oracle unavailable", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			actual, err := decodeOriginalPluginAPIs(sample.Input, nil)
			if err != nil {
				actual = nil
			}
			got := make([]struct {
				Path    string
				Methods []string
				Auth    string
			}, 0)
			for _, api := range actual {
				if api.Auth == "" {
					api.Auth = "apikey"
				}
				if api.Validate() != nil {
					continue
				}
				methods := slices.Clone(api.Methods)
				slices.Sort(methods)
				got = append(got, struct {
					Path    string
					Methods []string
					Auth    string
				}{api.Path, methods, api.Auth})
			}
			if !reflect.DeepEqual(got, sample.Routes) {
				t.Fatalf("fixed source route behavior: got %#v expected %#v (decode %v)", got, sample.Routes, err)
			}
		})
	}
}
