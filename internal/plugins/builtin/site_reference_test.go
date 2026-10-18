package builtin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strconv"
	"testing"
)

func typedSiteJSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out any
	if err := decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMergeSiteSwitchTypedInstalledSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugins/merge-sites-typed-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != "7ea3428140797551fcf789dbe3c99ad1b94c2ec6f5b29af4ea91166979a8d322" {
		t.Fatal("typed source oracle changed")
	}
	var fixture struct {
		Source string `json:"source_sha256"`
		Cases  []struct {
			Config    SiteControls
			Installed []string
			Events    []struct {
				SiteID  SiteReference `json:"site_id"`
				Enabled bool
			}
			State      bool
			Normalized SiteControls
			System     struct{ Search, RSS []SiteReference }
			Plugins    map[string]map[string]json.RawMessage
		}
		Form  json.RawMessage
		Model SiteControls
	}
	if json.Unmarshal(raw, &fixture) != nil || fixture.Source != "52d798421eabe57a88f3d0fc448289309fdfc8ecd6ea50d98cd5436d93289ab0" || len(fixture.Cases) != 64 {
		t.Fatal("wrong installed source reference")
	}
	for i, sample := range fixture.Cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			var custom []SiteReference
			if slices.Contains(sample.Installed, "CustomSites") {
				custom = []SiteReference{{Custom: "7"}, {Custom: "c1"}}
			}
			normalized := NormalizeSiteControlReferences(sample.Config, custom)
			if normalized.Validate() != nil || !reflect.DeepEqual(typedSiteJSON(t, normalized), typedSiteJSON(t, sample.Normalized)) {
				t.Fatal("normalization differs from original", normalized, sample.Normalized)
			}
			if sample.Config.AnyFollow(sample.Installed) != sample.State {
				t.Fatal("original CrossSeed state gate differs")
			}
			actual := map[string][]SiteReference{}
			for _, slot := range SiteControlSlots {
				if slot.PluginID == "" || slices.Contains(sample.Installed, slot.PluginID) {
					actual[slot.Key] = normalized.References(slot.Key)
				}
			}
			if sample.Config.AnyFollow(sample.Installed) {
				for _, event := range sample.Events {
					for _, slot := range SiteControlSlots {
						if sample.Config.Follows(slot.Follow) && (slot.PluginID == "" || slices.Contains(sample.Installed, slot.PluginID)) {
							actual[slot.Key], _ = ChangeSiteReference(actual[slot.Key], event.SiteID, event.Enabled)
						}
					}
				}
			}
			for _, slot := range SiteControlSlots {
				if slot.PluginID != "" && !slices.Contains(sample.Installed, slot.PluginID) {
					continue
				}
				var want []SiteReference
				if slot.Key == "search_sites" {
					want = sample.System.Search
				} else if slot.Key == "rss_sites" {
					want = sample.System.RSS
				} else if err := json.Unmarshal(sample.Plugins[slot.PluginID][slot.PeerField], &want); err != nil {
					t.Fatal(err)
				}
				if want == nil {
					want = []SiteReference{}
				}
				if !reflect.DeepEqual(actual[slot.Key], want) {
					t.Fatal("source event list semantics differ", slot.Key, actual[slot.Key], want)
				}
			}
		})
	}
	conf, err := HandleMergeSiteSwitch(context.Background(), "config_form", nil, func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(SiteControlSnapshot{Config: fixture.Model, Sites: []SiteControlOption{{"数字七", SiteReference{Native: 7}}, {"数字十九", SiteReference{Native: 19}}}, CustomSites: []SiteControlOption{{"自定义七 [自定义]", SiteReference{Custom: "7"}}, {"自定义一 [自定义]", SiteReference{Custom: "c1"}}}, Installed: []string{"AutoSignIn", "SiteStatistic", "IYUUAutoSeed", "BrushFlow", "CrossSeed", "CustomSites"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct{ Conf json.RawMessage }
	if json.Unmarshal(conf, &output) != nil || !reflect.DeepEqual(typedSiteJSON(t, output.Conf), typedSiteJSON(t, fixture.Form)) {
		t.Fatal("original numeric form differs")
	}
}

func TestMergeSiteReferenceExactNumericAndCustomNamespaces(t *testing.T) {
	for _, raw := range []string{`0`, `-1`, `true`, `false`, `null`, `1.0`, `1e0`, `[]`, `{}`, `9223372036854775808`, `""`, `"a\n"`} {
		var ref SiteReference
		if json.Unmarshal([]byte(raw), &ref) == nil {
			t.Fatal("invalid native/custom identity accepted", raw)
		}
	}
	var refs []SiteReference
	if json.Unmarshal([]byte(`[7,"7","all",9007199254740993,9223372036854775807,7]`), &refs) != nil {
		t.Fatal("source scalar identities rejected")
	}
	if refs[0] == refs[1] || refs[3].Native != 9007199254740993 {
		t.Fatal("numeric identity coerced")
	}
	out, changed := ChangeSiteReference(refs, SiteReference{Native: 7}, false)
	if !changed || len(out) != 5 || out[0] != (SiteReference{Custom: "7"}) || out[4] != (SiteReference{Native: 7}) {
		t.Fatal("first-only original list.remove semantics lost")
	}
	encoded, err := json.Marshal(refs)
	if err != nil || string(encoded) != `[7,"7","all",9007199254740993,9223372036854775807,7]` {
		t.Fatal("wire identity changed", string(encoded), err)
	}
}
