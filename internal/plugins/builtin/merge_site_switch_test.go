package builtin

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
)

func TestMergeSiteSwitchOriginalOracle(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugins/merge-sites-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Config    SiteControls
			Installed []string
			Events    []struct {
				SiteID  string `json:"site_id"`
				Enabled bool
			}
			State      bool
			Normalized SiteControls
			System     struct{ Search, RSS []string }
			Plugins    map[string]map[string]json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for index, sample := range fixture.Cases {
		t.Run(string(rune('A'+index)), func(t *testing.T) {
			custom := []string{}
			if slices.Contains(sample.Installed, "CustomSites") {
				custom = []string{"c1"}
			}
			normalized := NormalizeSiteControls(sample.Config, custom)
			if normalized.Validate() != nil {
				t.Fatal("source configuration rejected")
			}
			var expected, got map[string]any
			a, _ := json.Marshal(normalized)
			b, _ := json.Marshal(sample.Normalized)
			json.Unmarshal(a, &got)
			json.Unmarshal(b, &expected)
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("normalization differs: got %s want %s", a, b)
			}
			if state := sample.Config.AnyFollow(sample.Installed); state != sample.State {
				t.Fatal("source state gate differs", state, sample.State)
			}
			actual := map[string][]string{}
			for _, slot := range SiteControlSlots {
				if slot.PluginID == "" || slices.Contains(sample.Installed, slot.PluginID) {
					actual[slot.Key] = normalized.Sites(slot.Key)
				}
			}
			if sample.Config.AnyFollow(sample.Installed) {
				for _, event := range sample.Events {
					for _, slot := range SiteControlSlots {
						if sample.Config.Follows(slot.Follow) && (slot.PluginID == "" || slices.Contains(sample.Installed, slot.PluginID)) {
							actual[slot.Key], _ = ChangeSiteControl(actual[slot.Key], event.SiteID, event.Enabled)
						}
					}
				}
			}
			for _, slot := range SiteControlSlots {
				if slot.PluginID != "" && !slices.Contains(sample.Installed, slot.PluginID) {
					continue
				}
				var want []string
				if slot.Key == "search_sites" {
					want = sample.System.Search
				} else if slot.Key == "rss_sites" {
					want = sample.System.RSS
				} else {
					json.Unmarshal(sample.Plugins[slot.PluginID][slot.PeerField], &want)
				}
				if want == nil {
					want = []string{}
				}
				got := actual[slot.Key]
				if got == nil {
					got = []string{}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("site-event result differs", slot.Key, got, want)
				}
			}
		})
	}
}

func TestMergeSiteSwitchOriginalFormAndNoImplicitApply(t *testing.T) {
	for _, installed := range [][]string{{}, {"AutoSignIn", "SiteStatistic", "IYUUAutoSeed", "BrushFlow", "CrossSeed", "CustomSites"}} {
		snapshot := SiteControlSnapshot{Revision: 5, Config: SiteControls{}, Sites: []SiteControlOption{{"系统一", "s1"}}, CustomSites: []SiteControlOption{{"自定义一 [自定义]", "c1"}}, Installed: installed}
		response, err := HandleMergeSiteSwitch(context.Background(), "config_form", nil, func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			if method != "sites.controls.read" || string(raw) != "{}" {
				t.Fatal("unscoped form call")
			}
			return json.Marshal(snapshot)
		})
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Conf     []map[string]any
			Revision int64
		}
		if json.Unmarshal(response, &result) != nil || result.Revision != 5 {
			t.Fatal("missing revision")
		}
		rows := result.Conf[0]["content"].([]any)
		want := 4
		if len(installed) > 0 {
			want = 9
		}
		if len(rows) != want {
			t.Fatal("original conditional rows differ", len(rows), want)
		}
	}
	for _, raw := range []string{`{"action":"init","config":{}}`, `{"action":"reload","config":{}}`, `{"action":"init","config":{},"input":{"config_saved":true}}`} {
		called := false
		_, err := HandleMergeSiteSwitch(context.Background(), "invoke", json.RawMessage(raw), func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			called = true
			if method != "sites.controls.configure" {
				t.Fatal("unexpected write capability")
			}
			return json.RawMessage(`{"configured":true}`), nil
		})
		if err != nil || !called {
			t.Fatal("initialization did not register current flags", err)
		}
	}
}

func TestMergeSiteSwitchExactOriginalFormMetadata(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugins/merge-sites-original-forms.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Installed []string
			Conf      any
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, sample := range fixture.Cases {
		snapshot := SiteControlSnapshot{Config: SiteControls{}, Installed: sample.Installed, Sites: []SiteControlOption{{"系统一", "s1"}, {"系统二", "s2"}}}
		if slices.Contains(sample.Installed, "CustomSites") {
			snapshot.CustomSites = []SiteControlOption{{"自定义一 [自定义]", "c1"}}
		}
		result, err := HandleMergeSiteSwitch(context.Background(), "config_form", nil, func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(snapshot)
		})
		if err != nil {
			t.Fatal(err)
		}
		var actual struct{ Conf any }
		if err := json.Unmarshal(result, &actual); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual.Conf, sample.Conf) {
			t.Fatal("original component/props/labels/hints differ", sample.Installed)
		}
	}
}
