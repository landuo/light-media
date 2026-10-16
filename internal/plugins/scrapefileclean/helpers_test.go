package scrapefileclean

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestActualSourceHelpers(t *testing.T) {
	data, e := os.ReadFile("../../../testdata/plugins/scrape-file-clean-1.0.7/helpers-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var suite struct {
		Home    string
		Contain []struct {
			Path, Base string
			Result     bool
		}
		Extensions []struct {
			Value  string
			Result []string
		}
		Names []struct {
			Name, Media string
			Result      bool
		}
		Others []struct {
			Name, Media string
			Other       []string
			Result      bool
		}
	}
	if e = json.Unmarshal(data, &suite); e != nil {
		t.Fatal(e)
	}
	for _, c := range suite.Contain {
		if got := sameOrChild(cleanPath(c.Path), c.Base, suite.Home); got != c.Result {
			t.Errorf("contain %q %q got%v want%v", c.Path, c.Base, got, c.Result)
		}
	}
	for _, c := range suite.Extensions {
		if !reflect.DeepEqual(ParseExtensions(c.Value), c.Result) {
			t.Errorf("extensions %q", c.Value)
		}
	}
	for _, c := range suite.Names {
		if got := SameMediaScrapName(c.Name, c.Media); got != c.Result {
			t.Errorf("name %q/%q", c.Name, c.Media)
		}
	}
	for _, c := range suite.Others {
		if got := BelongsToOtherMedia(c.Name, c.Media, c.Other); got != c.Result {
			t.Errorf("other %#v", c)
		}
	}
}

func TestOriginalUncaughtOverflowPreservesPreviousDelay(t *testing.T) {
	data, e := os.ReadFile("../../../testdata/plugins/scrape-file-clean-1.0.7/oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var expected struct {
		Overflow struct {
			Delay   int
			Error   string
			Enabled bool
		} `json:"config_overflow"`
	}
	if e = json.Unmarshal(data, &expected); e != nil {
		t.Fatal(e)
	}
	m := New(nil)
	m.Config.Delay = 12
	err := m.Init(json.RawMessage(`{"enabled":true,"delay_seconds":1e400}`))
	if err == nil || err.Error() != expected.Overflow.Error || m.Config.Delay != expected.Overflow.Delay || m.GetState() != expected.Overflow.Enabled {
		t.Fatal("source partial state on OverflowError changed", err, m.Config.Delay)
	}
}
