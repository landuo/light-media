package pyregex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func canonical(raw []byte) any {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		panic(err)
	}
	return value
}

func compare(t *testing.T, name string, actual any, expected json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canonical(raw), canonical(expected)) {
		t.Errorf("%s\ngot %s\nsource %s", name, raw, expected)
	}
}
func TestActualPython312ParserAST(t *testing.T) { runSourceAST(t, "../testdata/source-oracles/autosignin-regex-frontend-20261106/grammar-source-r1.stdout", 322) }
func TestActualPython312ParserGrammarMatrix(t *testing.T) {
	runSourceAST(t, "../testdata/source-oracles/autosignin-regex-frontend-20261106/grammar-source-r2.stdout", 724)
}
func runSourceAST(t *testing.T, fixture string, count int) {
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Pattern      string          `json:"pattern"`
			Flags        int             `json:"flags"`
			Parser       json.RawMessage `json:"parser"`
			ParseError   json.RawMessage `json:"parse_error"`
			CompileError json.RawMessage `json:"compile_error"`
			Warnings     []Warning       `json:"parse_warnings"`
		}
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != count {
		t.Fatal(len(source.Cases))
	}
	for i, c := range source.Cases {
		t.Run(fmt.Sprintf("%03d", i), func(t *testing.T) {
			p, err := Parse(c.Pattern, c.Flags)
			var failure any
			if err != nil {
				failure = err
			}
			compare(t, "parse error "+c.Pattern, failure, c.ParseError)
			if err != nil {
				return
			}
			compare(t, "AST/state "+c.Pattern, p.Oracle(), c.Parser)
			if !reflect.DeepEqual(p.State.Warnings, c.Warnings) && !(len(p.State.Warnings) == 0 && len(c.Warnings) == 0) {
				t.Errorf("warnings got %+v source %+v", p.State.Warnings, c.Warnings)
			}
			err = CheckCompile(p)
			failure = nil
			if err != nil {
				failure = err
			}
			compare(t, "compile error "+c.Pattern, failure, c.CompileError)
		})
	}
}
