package removelink

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSourcePureHelperOracle(t *testing.T) {
	raw, e := os.ReadFile("../../../testdata/plugins/remove-link-2.17/helpers-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var suite struct {
		Cases []struct {
			Kind   string
			Args   []string
			Result any
			Error  any
		}
	}
	if json.Unmarshal(raw, &suite) != nil {
		t.Fatal("invalid helper oracle")
	}
	for _, x := range suite.Cases {
		t.Run(x.Kind+":"+x.Args[0], func(t *testing.T) {
			var out any
			switch x.Kind {
			case "extensions":
				out = ParseExtensions(x.Args[0])
			case "trickplay":
				out = Trickplay(x.Args[0])
			case "path":
				out = []string{cleanPath(x.Args[0]), parent(x.Args[0]), base(x.Args[0]), stem(x.Args[0]), suffix(x.Args[0])}
			case "mapping":
				a, b, ok := StoragePath(x.Args[0], x.Args[1])
				if ok {
					out = []string{a, b}
				} else {
					out = []any{nil, nil}
				}
			case "child":
				out = (Config{Exclude: x.Args[1]}).Excluded(x.Args[0], "")
			case "scrap":
				out = (Config{Extensions: ParseExtensions(x.Args[1])}).IsScrap(x.Args[0])
			default:
				t.Fatal(x.Kind)
			}
			b, _ := json.Marshal(out)
			json.Unmarshal(b, &out)
			if x.Error != nil || !reflect.DeepEqual(out, x.Result) {
				t.Fatalf("source=%v error=%v Go=%v", x.Result, x.Error, out)
			}
		})
	}
}
