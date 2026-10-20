package noticetemplate

import (
	"encoding/json"
	"math/big"
	"os"
	"testing"

	"light-media/internal/jinja/exec"
)

func TestContextOrderedIntegerSeasonYearsOriginalBuilderAST(t *testing.T) {
	raw, e := os.ReadFile("testdata/ordered-season-source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Ordered []struct {
			Name, Key             string
			KeyType               string `json:"key_type"`
			Media, Meta, Expected Object
		}
	}
	if e = decode(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	if len(fixture.Ordered) != 8 {
		t.Fatal("original builder fixture truncated")
	}
	for _, c := range fixture.Ordered {
		t.Run(c.Name, func(t *testing.T) {
			var key any
			switch c.KeyType {
			case "empty":
				key = nil
			case "int":
				key = 1
			case "str":
				key = "1"
			case "bool":
				key = true
			case "float":
				key = 1.0
			case "fraction":
				key = 1.5
			case "zero":
				key = 0
			case "huge":
				key, _ = new(big.Int).SetString(c.Key, 10)
			default:
				t.Fatal(c.KeyType)
			}
			years := exec.NewDict()
			years.Pairs = append(years.Pairs, &exec.Pair{Key: exec.AsValue(3), Value: exec.AsValue("2028")}, &exec.Pair{Key: exec.AsValue(key), Value: exec.AsValue("2027")})
			if c.KeyType == "empty" {
				years.Pairs = nil
			}
			c.Media["season_years"] = years
			rawObjects := false
			got, e := BuildContext(ContextInput{Meta: c.Meta, Media: c.Media, IncludeRawObjects: &rawObjects})
			if e != nil || !equalJSON(got, c.Expected) {
				g, _ := json.Marshal(got)
				w, _ := json.Marshal(c.Expected)
				t.Fatalf("original ordered lookup differs err=%v native=%s original=%s", e, g, w)
			}
		})
	}
}
