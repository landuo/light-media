package integrations

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMTeamIDsAreValidNonEmptyScalars(t *testing.T) {
	for _, v := range []string{`null`, `{}`, `[]`, `""`, `0`, `-1`, `1.5`, `true`, `"secret-value"`} {
		if validMTeamID(json.RawMessage(v)) {
			t.Errorf("invalid ID accepted: %s", v)
		}
	}
	for _, v := range []string{`123`, `"123"`} {
		if !validMTeamID(json.RawMessage(v)) {
			t.Errorf("valid ID rejected: %s", v)
		}
	}
}
func TestNexusIgnoresSidebarLinksAndEmptyText(t *testing.T) {
	b := []byte(`<a href="details.php?id=99">side</a>No torrents found<table class="torrents"><tr><td>unknown</td></tr></table>`)
	scope := nexusResults(b)
	if scope == nil || detailLink.Match(scope) || strings.Contains(string(scope), "No torrents found") {
		t.Fatal("sidebar affected result scope")
	}
	b = []byte(`<table class="torrents"><tr><td><table><tr><td>x</td></tr></table><a href="details.php?id=123">item</a></td></tr></table><a href="details.php?id=99">side</a>`)
	if got := detailLink.FindAllSubmatch(nexusResults(b), -1); len(got) != 1 || string(got[0][1]) != "123" {
		t.Fatal("nested table scope failed")
	}
	if nexusResults([]byte(`<table class="torrents"><tr><td>x`)) != nil {
		t.Fatal("unclosed structure accepted")
	}
}
