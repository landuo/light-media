package app

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"sort"
	"testing"
)

func TestOriginalSidebarFixedSourceReplay(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-navigation/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name, Raw string
			Expected  json.RawMessage
		}
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got, _ := originalSidebarEntries("Owned_clone", "Clone display name", json.RawMessage(c.Raw))
			sort.SliceStable(got, func(i, j int) bool {
				for _, k := range []string{"section", "order", "plugin_id", "nav_key"} {
					if k == "order" {
						x, _ := new(big.Int).SetString(string(got[i][k].(json.Number)), 10)
						y, _ := new(big.Int).SetString(string(got[j][k].(json.Number)), 10)
						if x.Cmp(y) != 0 {
							return x.Cmp(y) < 0
						}
					} else {
						x, y := got[i][k].(string), got[j][k].(string)
						if x != y {
							return x < y
						}
					}
				}
				return false
			})
			for _, row := range got {
				delete(row, "instance_id")
			}
			actual, e := json.Marshal(got)
			if e != nil {
				t.Fatal(e)
			}
			var av, ev any
			// Preserve all integer digits while comparing decoded source/target JSON.
			decode := func(data []byte, out *any) {
				d := json.NewDecoder(bytes.NewReader(data))
				d.UseNumber()
				if e := d.Decode(out); e != nil {
					t.Fatal(e)
				}
			}
			decode(actual, &av)
			decode(c.Expected, &ev)
			if !reflect.DeepEqual(av, ev) {
				t.Fatalf("fixed source mismatch\n got %s\nwant %s", actual, c.Expected)
			}
		})
	}
}
