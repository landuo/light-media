package pyregex

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"light-media/builtinplugins/plugins/autosignin/regexengine/syntax"
)

func simpleSet(p *Pattern, flags int) (runeSet, bool) {
	if len(p.Nodes) != 1 {
		panic("source set fixture must be one operation")
	}
	n := p.Nodes[0]
	switch n.Op {
	case Subpattern:
		if n.AddFlags&typeFlags != 0 {
			flags &^= typeFlags
		}
		return simpleSet(n.Children[0], (flags|n.AddFlags)&^n.DelFlags)
	case Literal:
		return classRanges([]ClassItem{{Op: Literal, Rune: n.Rune}}, flags)
	case NotLiteral:
		return classRanges([]ClassItem{{Op: Negate}, {Op: Literal, Rune: n.Rune}}, flags)
	case In:
		return classRanges(n.Set, flags)
	}
	panic("source class fixture operation")
}
func TestActualSourceCaseSetsAllCodepoints(t *testing.T) {
	raw, err := os.ReadFile("../testdata/source-oracles/autosignin-regex-frontend-20261106/case-sets-source-r1.stdout")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Pattern string          `json:"pattern"`
			Ranges  json.RawMessage `json:"ranges"`
		}
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 24 {
		t.Fatal(len(source.Cases))
	}
	for i, c := range source.Cases {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			p, err := Parse(c.Pattern, 0)
			if err != nil {
				t.Fatal(err)
			}
			ranges, negate := simpleSet(p, p.State.Flags)
			if negate {
				ranges = ranges.inverse()
			}
			out := [][2]rune{}
			for _, r := range ranges {
				out = append(out, [2]rune{r.lo, r.hi})
			}
			compare(t, "all1,114,112 source codepoints "+c.Pattern, out, c.Ranges)
		})
	}
}
func hasSurrogates(input []rune) bool {
	for _, r := range input {
		if r >= 0xd800 && r <= 0xdfff {
			return true
		}
	}
	return false
}

type sourceRuneMatch struct {
	Span      [2]int            `json:"span"`
	Groups    [][]rune          `json:"groups"`
	GroupDict map[string][]rune `json:"groupdict"`
}

func TestActualSourceScalarEscapedSurrogateWidths(t *testing.T) {
	runScalarSource(t, "../testdata/source-oracles/autosignin-regex-frontend-20261106/scalar-boundary-source-r1.stdout", 62)
}
func TestActualSourceSurrogateHashAndCHRBoundary(t *testing.T) {
	runScalarSource(t, "../testdata/source-oracles/autosignin-regex-frontend-20261106/scalar-boundary-source-r2.stdout", 20)
}
func runScalarSource(t *testing.T, fixture string, count int) {
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Texts [][]rune `json:"text_codepoints"`
		Cases []struct {
			Pattern      string             `json:"pattern"`
			PatternCP    []rune             `json:"pattern_codepoints"`
			PatternJSON  string             `json:"pattern_json"`
			Flags        int                `json:"flags"`
			Parser       json.RawMessage    `json:"parser"`
			ParseError   json.RawMessage    `json:"parse_error"`
			CompileError json.RawMessage    `json:"compile_error"`
			Matches      []*sourceRuneMatch `json:"matches"`
		}
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != count {
		t.Fatal(len(source.Cases))
	}
	for i, c := range source.Cases {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			c.Pattern = sourceText(c.PatternCP)
			decoded, _ := json.Marshal(sourceRunes(c.Pattern))
			expected, _ := json.Marshal(c.PatternCP)
			compare(t, "pattern input preserved", json.RawMessage(decoded), expected)
			parsed, err := Parse(c.Pattern, c.Flags)
			compareSourceScalarError(t, "parse exact scalar", err, c.ParseError)
			if err != nil {
				return
			}
			compare(t, "AST/width exact scalar", parsed.Oracle(), c.Parser)
			compiled, err := compileSource(c.Pattern, c.Flags)
			compareSourceScalarError(t, "compile exact scalar", err, c.CompileError)
			if err != nil {
				return
			}
			for index, input := range source.Texts {
				match, e := compiled.FindRunesMatch(input)
				if e != nil {
					t.Fatal(e)
				}
				var actual *sourceRuneMatch
				if match != nil {
					groups := [][]rune{}
					named := map[string][]rune{}
					for j := 1; j < len(compiled.GetGroupNumbers()); j++ {
						g := match.GroupByNumber(j)
						var cp []rune
						if g != nil && len(g.Captures) > 0 {
							cp = append([]rune{}, g.Runes()...)
						}
						groups = append(groups, cp)
					}
					for name, id := range parsed.State.GroupDict {
						named[name] = groups[id-1]
					}
					actual = &sourceRuneMatch{[2]int{match.RuneIndex, match.RuneIndex + match.RuneLength}, groups, named}
				}
				wanted, _ := json.Marshal(c.Matches[index])
				compare(t, fmt.Sprintf("internal rune executor (not string IPC parity) pattern=%s inputCP%v", c.Pattern, input), actual, wanted)
				if true {
					quick, e := compiled.MatchString(sourceText(input))
					if e != nil || quick != (c.Matches[index] != nil) {
						t.Fatalf("valid scalar string path %q sourcebool%v got%v err%v", c.Pattern, c.Matches[index] != nil, quick, e)
					}
				}
			}
		})
	}
}
func TestSourceFiniteLargeCounterIsNotInfinity(t *testing.T) {
	for _, pattern := range []string{`a{0,4294967294}`, `a{0,4294967294}?`} {
		p, err := Parse(pattern, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = CheckCompile(p); err != nil {
			t.Fatal(err)
		}
		if p.Nodes[0].Max != 4294967294 {
			t.Fatal("source upper bound fixture drift")
		}
		code, err := syntax.Write(lowerTree(p))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(code.Dump(), "Limit = 4294967294") {
			t.Fatalf("source legal finite maximum lost in executor:\n%s", code.Dump())
		}
	}
}
