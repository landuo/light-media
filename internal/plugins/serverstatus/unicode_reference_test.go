package serverstatus

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestInstalledPythonUnicodeAndIgnoreCaseReference(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugins/server-status-unicode-original-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Provenance Object
		Cases      []struct {
			Name, Input, Error string
			Expected           Object
		}
	}
	if err = decode(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Provenance["unicode"] != "15.0.0" || reference.Provenance["regex_module"] != "stdlib re" || len(reference.Cases) != 8 {
		t.Fatal("incomplete installed Python reference")
	}
	for _, c := range reference.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := Parse(c.Input)
			if c.Error != "" {
				if err == nil || err.Error() != c.Error {
					t.Fatalf("error=%v want %s", err, c.Error)
				}
				return
			}
			if err != nil || !equivalent(canonical(got), canonical(c.Expected)) {
				t.Fatalf("got=%v err=%v want=%v", got, err, c.Expected)
			}
		})
	}
	raw, err = os.ReadFile("../../../testdata/plugins/server-status-reference/installed-source.json")
	if err != nil {
		t.Fatal(err)
	}
	var tables struct {
		Parser struct {
			Unicode    string
			Count      int    `json:"decimal_count"`
			Zeroes     []rune `json:"decimal_zeroes"`
			Whitespace []rune
		} `json:"parser_unicode"`
	}
	if err = json.Unmarshal(raw, &tables); err != nil {
		t.Fatal(err)
	}
	if tables.Parser.Unicode != "15.0.0" || tables.Parser.Count != 680 || !reflect.DeepEqual(pythonDecimalZeroes, tables.Parser.Zeroes) || !reflect.DeepEqual(pythonWhitespace, tables.Parser.Whitespace) {
		t.Fatal("Go tables differ from actual Python scan")
	}
	for _, zero := range tables.Parser.Zeroes {
		for i := rune(0); i < 10; i++ {
			s := string(zero + i)
			if !digitsRE.MatchString(s) || decimal(s) != json.Number(string('0'+i)) {
				t.Fatalf("decimal U+%04X", zero+i)
			}
		}
	}
	if digitsRE.MatchString("\U00010d40") || pythonSpace('\u180e') {
		t.Fatal("inherited a newer Unicode table")
	}
	if !strings.Contains(pythonCaseLiteral("i"), "İı") {
		t.Fatal("RE2-only case folding lost Python variants")
	}
}
