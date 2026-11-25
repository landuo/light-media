package pyregex

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func independentCPEqual(t *testing.T, label string, a, b any) {
	t.Helper()
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	var x, y any
	json.Unmarshal(aa, &x)
	json.Unmarshal(bb, &y)
	if !reflect.DeepEqual(x, y) {
		t.Fatalf("%s got=%s want=%s", label, aa, bb)
	}
}
func TestRootIndependentRawCodepoints(t *testing.T) {
	raw, e := os.ReadFile("../testdata/source-oracles/root-independent-regex-codepoints-20261106/oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var o struct {
		Rows []struct {
			Pattern  []rune
			Texts    [][]rune
			Error    json.RawMessage
			Search   []json.RawMessage
			Warnings []struct {
				Category string `json:"category"`
				Message  string `json:"message"`
			}
		}
	}
	if e = json.Unmarshal(raw, &o); e != nil {
		t.Fatal(e)
	}
	if len(o.Rows) != 15 {
		t.Fatal("source row count")
	}
	for i, c := range o.Rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			p, e := CompileCodepoints(c.Pattern, false)
			var got any
			if e != nil {
				ee, ok := e.(*Error)
				if !ok {
					t.Fatal(e)
				}
				got = map[string]any{"class": ee.Class, "message_codepoints": sourceRunes(ee.Message), "pos": ee.Pos, "lineno": ee.Line, "colno": ee.Column}
			}
			independentCPEqual(t, "compile", got, c.Error)
			var ww []map[string]any = []map[string]any{}
			_, _ = parseSourceWithWarnings(sourceText(c.Pattern), 0, func(x Warning) { ww = append(ww, map[string]any{"category": x.Category, "message": x.Message}) })
			independentCPEqual(t, "warnings", ww, c.Warnings)
			if e != nil {
				return
			}
			if len(c.Search) != len(c.Texts) {
				t.Fatal("source search count")
			}
			for n, txt := range c.Texts {
				t.Run(fmt.Sprint(n), func(t *testing.T) {
					m, e := p.FindStringMatch(sourceText(txt))
					if e != nil {
						t.Fatal(e)
					}
					present, e := p.MatchString(sourceText(txt))
					if e != nil || present != (m != nil) {
						t.Fatalf("quick=%v %v", present, e)
					}
					var result any
					if m != nil {
						groups := [][]rune{}
						for _, id := range p.GetGroupNumbers()[1:] {
							g := m.GroupByNumber(id)
							var value []rune
							if g != nil && len(g.Captures) > 0 {
								value = append([]rune{}, g.Runes()...)
								independentCPEqual(t, "capture String", sourceRunes(g.String()), value)
							}
							groups = append(groups, value)
						}
						whole := append([]rune{}, m.Runes()...)
						independentCPEqual(t, "whole String", sourceRunes(m.String()), whole)
						result = map[string]any{"span": [2]int{m.RuneIndex, m.RuneIndex + m.RuneLength}, "whole": whole, "groups": groups}
					}
					independentCPEqual(t, "search", result, c.Search[n])
				})
			}
		})
	}
}
