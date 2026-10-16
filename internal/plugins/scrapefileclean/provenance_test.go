package scrapefileclean

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestCompleteExecutableSourceAndOriginalForm(t *testing.T) {
	data, e := os.ReadFile("../../../testdata/plugins/scrape-file-clean-1.0.7/installed-reference.json")
	if e != nil {
		t.Fatal(e)
	}
	var installed struct {
		SHA     string `json:"sha256"`
		Version string `json:"version"`
		Whole   bool   `json:"complete_module_ast_matches"`
		AST     string `json:"complete_module_ast_sha256"`
	}
	if json.Unmarshal(data, &installed) != nil || installed.SHA != "c14c36e17112605e6c856614e88ea425530be2462123126a85e1adfea009a3af" || installed.Version != "1.0.7" || !installed.Whole || installed.AST != "02f66833b2f3acb2347a6ee9361db5805629ae9d6f79fd50884edcdcf1bfe595" {
		t.Fatal("source changed")
	}
	data, e = os.ReadFile("../../../testdata/plugins/scrape-file-clean-1.0.7/oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var actual struct {
		Form          json.RawMessage
		Commands, API []any
		Page          any
		Alnum         [][2]rune `json:"alnum_ranges"`
	}
	if json.Unmarshal(data, &actual) != nil {
		t.Fatal("bad original reference")
	}
	var expected, got any
	json.Unmarshal(actual.Form, &expected)
	json.Unmarshal(Form(), &got)
	if !reflect.DeepEqual(expected, got) || actual.Page != nil || len(actual.Commands) != 0 || len(actual.API) != 0 {
		t.Fatal("original SDK surface changed")
	}
	if !reflect.DeepEqual(alnumRanges, actual.Alnum) {
		t.Fatal("Unicode15 table changed")
	}
}
