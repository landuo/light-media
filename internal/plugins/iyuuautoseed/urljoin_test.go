package iyuuautoseed

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestURLJoinAgainstActualPinnedCPython(t *testing.T) {
	raw, e := os.ReadFile("reference/url-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		UnicodeVersion string `json:"unicode_version"`
		NFKC           []rune `json:"nfkc_delimiter_runes"`
		Cases          []struct {
			Base     string `json:"base"`
			Relative string `json:"relative"`
			Result   struct {
				Value     string `json:"value"`
				Exception string `json:"exception"`
			} `json:"result"`
		} `json:"cases"`
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	if fixture.UnicodeVersion != "15.0.0" {
		t.Fatal("wrong source Unicode tables", fixture.UnicodeVersion)
	}
	got := []rune{}
	for r := rune(0); r <= 0x10ffff; r++ {
		if sourceNFKCDelimiter(r) {
			got = append(got, r)
		}
	}
	if !reflect.DeepEqual(got, fixture.NFKC) {
		t.Fatal("source complete NFKC delimiter predicate differs", got, fixture.NFKC)
	}
	for i, tc := range fixture.Cases {
		value, e := sourceURLJoin(tc.Base, tc.Relative)
		if tc.Result.Exception != "" {
			var fault *SourceFault
			if !errors.As(e, &fault) || fault.Class != tc.Result.Exception {
				t.Fatalf("case%d want%s got%q %v", i, tc.Result.Exception, value, e)
			}
		} else if e != nil || value != tc.Result.Value {
			t.Fatalf("case%d base%q rel%q want%q got%q %v", i, tc.Base, tc.Relative, tc.Result.Value, value, e)
		}
	}
}
