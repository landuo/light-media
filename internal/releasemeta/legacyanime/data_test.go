package legacyanime

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
)

func TestActualInstalledPython15Data(t *testing.T) {
	file, err := os.Open("../../../testdata/plugins/release-meta-legacy-python-data.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var source struct {
		Unicode struct {
			Version    string               `json:"unicode_version"`
			Ranges     map[string][][2]rune `json:"ranges"`
			Normalized map[string]string    `json:"normalized"`
			Decimal    map[string]int       `json:"decimal"`
		} `json:"unicode"`
		Casing struct {
			Version    string                       `json:"unicode_version"`
			Properties map[string][][2]rune         `json:"properties"`
			Maps       map[string]map[string]string `json:"maps"`
		} `json:"casing"`
		Keywords map[string]map[string]struct {
			Category     Category `json:"category"`
			Identifiable bool     `json:"identifiable"`
			Searchable   bool     `json:"searchable"`
			Valid        bool     `json:"valid"`
		} `json:"keywords"`
	}
	if err := json.NewDecoder(reader).Decode(&source); err != nil {
		t.Fatal(err)
	}
	if source.Unicode.Version != "15.0.0" || source.Casing.Version != "15.0.0" {
		t.Fatal("wrong source Unicode version")
	}
	ranges := func(name string, actual []sourceRange, want [][2]rune) {
		t.Helper()
		values := make([][2]rune, len(actual))
		for i, r := range actual {
			values[i] = [2]rune{r.lo, r.hi}
		}
		if !reflect.DeepEqual(values, want) {
			t.Fatalf("%s ranges differ", name)
		}
	}
	for name, actual := range map[string][]sourceRange{"digit": pyDigitRanges, "alnum": pyAlnumRanges, "named": pyNamedRanges, "latin": pyLatinRanges} {
		ranges(name, actual, source.Unicode.Ranges[name])
	}
	ranges("cased", pyCasedRanges, source.Casing.Properties["cased"])
	ranges("case_ignorable", pyCaseIgnorableRanges, source.Casing.Properties["case_ignorable"])
	mappings := func(name string, actual map[rune]string, want map[string]string) {
		t.Helper()
		if len(actual) != len(want) {
			t.Fatalf("%s count differs", name)
		}
		for scalar, v := range want {
			n, e := strconv.Atoi(scalar)
			if e != nil {
				t.Fatal(e)
			}
			if a, ok := actual[rune(n)]; !ok || a != v {
				t.Fatalf("%s U+%X got %q/%t want%q", name, n, a, ok, v)
			}
		}
	}
	mappings("normalized", pyNormalized, source.Unicode.Normalized)
	mappings("lower", pyLowerMappings, source.Casing.Maps["lower"])
	mappings("upper", pyUpperMappings, source.Casing.Maps["upper"])
	mappings("title", pyTitleMappings, source.Casing.Maps["title"])
	if len(pyDecimal) != len(source.Unicode.Decimal) {
		t.Fatal("decimal count differs")
	}
	for scalar, want := range source.Unicode.Decimal {
		n, e := strconv.Atoi(scalar)
		if e != nil {
			t.Fatal(e)
		}
		if got, ok := pyDecimal[rune(n)]; !ok || got != want {
			t.Fatalf("decimal U+%X differs", n)
		}
	}
	for namespace, actual := range map[string]map[string]keyword{"_keys": keywords, "_file_extensions": extensionKeywords} {
		want := source.Keywords[namespace]
		if len(actual) != len(want) {
			t.Fatalf("%s count differs", namespace)
		}
		for key, v := range want {
			a, ok := actual[key]
			if !ok || a.category != v.Category || a.identifiable != v.Identifiable || a.searchable != v.Searchable || a.valid != v.Valid {
				t.Fatalf("%s/%s differs", namespace, key)
			}
		}
	}
}
