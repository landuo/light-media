package pyregex

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

type sourceMatch struct {
	Span      [2]int         `json:"span"`
	Groups    []any          `json:"groups"`
	GroupDict map[string]any `json:"groupdict"`
}

func TestActualPython312TypedExecutor(t *testing.T) {
	for _, fixture := range []string{"../testdata/source-oracles/autosignin-regex-frontend-20261106/grammar-source-r1.stdout", "../testdata/source-oracles/autosignin-regex-frontend-20261106/grammar-source-r2.stdout"} {
		raw, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		var source struct {
			Texts []string `json:"texts"`
			Cases []struct {
				Pattern      string          `json:"pattern"`
				Flags        int             `json:"flags"`
				CompileError json.RawMessage `json:"compile_error"`
				Matches      []*sourceMatch  `json:"matches"`
			}
		}
		if err = json.Unmarshal(raw, &source); err != nil {
			t.Fatal(err)
		}
		for i, c := range source.Cases {
			t.Run(fmt.Sprintf("%s/%03d", fixture, i), func(t *testing.T) {
				compiled, err := compileSource(c.Pattern, c.Flags)
				if string(c.CompileError) != "null" {
					var failure any
					if err != nil {
						failure = err
					}
					compare(t, "source invalid pattern "+c.Pattern, failure, c.CompileError)
					return
				}
				if err != nil {
					t.Fatalf("source valid %s: %v", c.Pattern, err)
				}
				for i, text := range source.Texts {
					match, e := compiled.FindStringMatch(text)
					if e != nil {
						t.Errorf("%s %q: %v", c.Pattern, text, e)
						continue
					}
					quick, e := compiled.MatchString(text)
					if e != nil || quick != (c.Matches[i] != nil) {
						t.Errorf("quickbool %s %q got%v err%v source%v", c.Pattern, text, quick, e, c.Matches[i] != nil)
					}
					var actual *sourceMatch
					if match != nil {
						groups := []any{}
						named := map[string]any{}
						for j := 1; j < len(compiled.GetGroupNumbers()); j++ {
							g := match.GroupByNumber(j)
							var value any
							if g != nil && len(g.Captures) > 0 {
								value = g.String()
							}
							groups = append(groups, value)
						}
						parsed, _ := Parse(c.Pattern, c.Flags)
						for name, id := range parsed.State.GroupDict {
							named[name] = groups[id-1]
						}
						actual = &sourceMatch{[2]int{match.RuneIndex, match.RuneIndex + match.RuneLength}, groups, named}
					}
					wanted, _ := json.Marshal(c.Matches[i])
					compare(t, "source fullmatch "+c.Pattern+" text="+repr(text), actual, wanted)
				}
			})
		}
	}
}
