package subscriptionquality

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
)

func TestOriginalSubscribeChainASTOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Input           State          `json:"input"`
			Resource        Resource       `json:"resource"`
			OrdinaryMissing int            `json:"ordinary_missing"`
			Expected        map[string]any `json:"expected"`
		} `json:"cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	for i, c := range corpus.Cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			s, r := c.Input, c.Resource
			var changed any
			if s.BestVersion && s.Type == TV {
				changed = s.ChangeTotal(2)
			}
			actual := map[string]any{"targets": s.Targets(), "downloaded": s.Downloaded(), "pending": s.Pending(), "current": s.Current(), "complete": s.Complete(), "interested": s.Interested(r), "selection": s.Select(r), "full_season": s.FullSeason(r), "accepted": s.Accepted([]Resource{r}, c.OrdinaryMissing), "changed_total": changed, "expanded_total": s.ExpandTotal(5)}
			encoded, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			var normalized map[string]any
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.UseNumber()
			if err = decoder.Decode(&normalized); err != nil {
				t.Fatal(err)
			}
			for key, expected := range c.Expected {
				if !reflect.DeepEqual(normalized[key], expected) {
					t.Errorf("%s: actual %v; original AST %v; input %+v; resource %+v", key, normalized[key], expected, s, r)
				}
			}
		})
	}
}
