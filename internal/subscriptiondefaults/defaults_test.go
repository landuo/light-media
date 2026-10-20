package subscriptiondefaults

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func TestOriginalSubscribeDefaultMethodsSourceOracle(t *testing.T) {
	var corpus struct {
		SourceSHA string `json:"source_sha256"`
		Cases     []struct {
			Input struct {
				MediaType   any `json:"media_type"`
				Environment Object
				Database    Object
				Kwargs      Object
			}
			Result Object
			Error  string
			Reads  []string
		}
	}
	raw, e := os.ReadFile("testdata/source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&corpus) != nil || len(corpus.Cases) != 1200 || len(corpus.SourceSHA) != 64 {
		t.Fatal("incomplete original defaults AST corpus")
	}
	for index, tc := range corpus.Cases {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			reads := []string{}
			provider := Provider{Environment: tc.Input.Environment, ReadDatabase: func(key string) (any, error) { reads = append(reads, key); return tc.Input.Database[key], nil }}
			got, e := provider.Apply(tc.Input.MediaType, tc.Input.Kwargs)
			if tc.Error != "" {
				var source *SourceError
				if !errors.As(e, &source) || source.Class != tc.Error {
					t.Fatal("original default exception changed", e, tc.Error)
				}
			} else if e != nil || !reflect.DeepEqual(got, tc.Result) {
				t.Fatalf("original None/default/kwargs changed: got %#v want %#v err %v", got, tc.Result, e)
			}
			if !reflect.DeepEqual(reads, tc.Reads) {
				t.Fatal("original lazy environment/database reads changed", reads, tc.Reads)
			}
		})
	}
}
