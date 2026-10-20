package releasemeta

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestInstalledLegacyExtensionMembership(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Extensions []string
			Title      string
			Isfile     bool
			Stripped   string
		}
	}
	raw, e := os.ReadFile("testdata/legacy-extension-membership.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	for i, tc := range oracle.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			p, e := NewMetaInfoParser(MetaInfoEnvironment{Options: Options{SourceMode: "legacy", MediaExts: tc.Extensions}})
			if e != nil {
				t.Fatal(e)
			}
			meta, e := p.ParseTitle(tc.Title, nil, nil)
			if e != nil || meta.Get("isfile") != tc.Isfile {
				t.Fatal("actual source wrapper suffix membership", meta.Get("isfile"), tc.Isfile, tc.Title, tc.Extensions, e)
			}
		})
	}
}
