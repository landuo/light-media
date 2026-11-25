package torrenttransfer

import (
	"context"
	"encoding/json"
	"light-media/builtinplugins/internal/sdk"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestActualSourceModuleServiceOrderOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/module-service-order-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Cases []struct {
			Name   string `json:"case"`
			Inputs []struct {
				Name    string
				Kind    string `json:"type"`
				Enabled bool
				Label   string
			} `json:"config_inputs"`
			Order    []string `json:"form_order"`
			Services map[string]struct {
				Metadata string `json:"metadata_kind"`
				Runtime  string `json:"runtime_class"`
				ID       string `json:"runtime_marker"`
			} `json:"get_service"`
		}
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			refs := []plugins.BrushDownloaderReferenceV1{}
			kinds := map[string]string{}
			for _, r := range c.Inputs {
				refs = append(refs, plugins.BrushDownloaderReferenceV1{ID: r.Label, Name: r.Name, Kind: r.Kind, Enabled: r.Enabled})
				kinds[r.Label] = r.Kind
			}
			host := func(_ context.Context, m string, r json.RawMessage) (json.RawMessage, error) {
				if m == plugins.DownloaderConfigurationsReadCapability {
					return json.Marshal(plugins.DownloaderConfigurationsV1{SchemaVersion: 1, Downloaders: refs})
				}
				var in plugins.DownloaderConnectionRequestV1
				_ = json.Unmarshal(r, &in)
				return json.Marshal(plugins.DownloaderSourceConnectionV1{DownloaderConnectionV1: plugins.DownloaderConnectionV1{SchemaVersion: 1, DownloaderID: in.DownloaderID, Kind: kinds[in.DownloaderID]}, Exists: true})
			}
			p := New()
			form, e := p.Handle(context.Background(), "get_form", nil, host)
			if e != nil {
				t.Fatal(e)
			}
			var value []any
			_ = json.Unmarshal(form, &value)
			var order []string
			var walk func(any)
			walk = func(v any) {
				switch x := v.(type) {
				case []any:
					for _, v := range x {
						walk(v)
					}
				case map[string]any:
					if props, ok := x["props"].(map[string]any); ok && props["model"] == "fromdownloader" {
						for _, item := range props["items"].([]any) {
							order = append(order, item.(map[string]any)["value"].(string))
						}
					}
					for _, v := range x {
						walk(v)
					}
				}
			}
			walk(value[0])
			if len(order) == 0 {
				order = []string{}
			}
			if !reflect.DeepEqual(order, c.Order) {
				t.Fatal("source form insertion slots changed", order, c.Order)
			}
			for _, name := range c.Order {
				got, e := (sdkHost{caller: host}).Service(context.Background(), name)
				want := c.Services[name]
				if e != nil || got == nil || got.Type != want.Metadata || got.actualKind() != strings.ToLower(want.Runtime) || got.Ref != want.ID {
					t.Fatal("source metadata/runtime lookup changed", got, want, e)
				}
			}
		})
	}
}
func TestMetadataTRRuntimeQBPostsLabelsThenHashAttributeError(t *testing.T) {
	for _, reject := range []bool{false, true} {
		s := newSDKFixture(t, "qbittorrent", "qbittorrent")
		s.rejectAdd = reject
		s.configs = []plugins.BrushDownloaderReferenceV1{{ID: "source", Name: "Source", Kind: "qbittorrent", Enabled: true}, {ID: "target-qb", Name: "Target", Kind: "qbittorrent", Enabled: true}, {ID: "target-tr", Name: "Target", Kind: "transmission", Enabled: true}}
		p := New()
		cfg := s.config()
		cfg["remainoldtag"] = false
		cfg["add_torrent_tags"] = "one, two,one"
		invoke(t, p, s, "init", cfg)
		if _, e := p.Handle(context.Background(), "transfer", nil, s.host); e == nil || !strings.Contains(e.Error(), "hashString") {
			t.Fatal("source QB tuple did not fail at TR hashString access", e)
		}
		if len(s.adds) != 1 || len(s.adds[0].Tags) != 0 || !reflect.DeepEqual(s.adds[0].QBLabels, []string{"one", " two", "one"}) || s.adds[0].SkipCheckingWire != nil {
			t.Fatal("source TR-style arguments changed before actual QB add", s.adds)
		}
		if len(s.controls) != 0 || len(s.history) != 0 || len(s.notices) != 0 || contains(s.calls, plugins.DownloaderSourceFindByTagCapability) {
			t.Fatal("post-add attribute failure continued business effects")
		}
	}
}
func TestInactiveFirstQBDoesNotFallbackToTR(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "qbittorrent")
	s.configs = []plugins.BrushDownloaderReferenceV1{{ID: "target-qb", Name: "Target", Kind: "qbittorrent", Enabled: true}, {ID: "target-tr", Name: "Target", Kind: "transmission", Enabled: true}}
	s.inactiveIDs = map[string]bool{"target-qb": true}
	service, e := (sdkHost{caller: s.host}).Service(context.Background(), "Target")
	if e != nil || service == nil || service.Ref != "target-qb" || !service.Inactive || service.Type != "transmission" {
		t.Fatal(service, e)
	}
	c, _ := NewCore(sourceConfig(), sdkHost{caller: s.host})
	got, e := c.service(context.Background(), "Target")
	if e != nil || got != nil {
		t.Fatal("inactive QB incorrectly fell back", got, e)
	}
}
func TestMetadataQBRuntimeTRFailsBindingBeforeNativeAdd(t *testing.T) {
	for _, skip := range []string{"false", "null", "missing"} {
		t.Run(skip, func(t *testing.T) {
			s := newSDKFixture(t, "qbittorrent", "transmission")
			s.configs = []plugins.BrushDownloaderReferenceV1{{ID: "source", Name: "Source", Kind: "qbittorrent", Enabled: true}, {ID: "target-tr", Name: "Target", Kind: "transmission", Enabled: true}, {ID: "target-qb", Name: "Target", Kind: "qbittorrent", Enabled: true}}
			s.absentIDs = map[string]bool{"target-qb": true}
			cfg := s.config()
			switch skip {
			case "null":
				cfg["skipverify"] = nil
			case "missing":
				delete(cfg, "skipverify")
			}
			p := New()
			invoke(t, p, s, "init", cfg)
			_, e := p.Handle(context.Background(), "transfer", nil, s.host)
			if e == nil || !strings.Contains(e.Error(), "unexpected keyword argument 'tag'") {
				t.Fatal("source TR argument binding did not fail", e)
			}
			if len(s.adds) != 0 || len(s.controls) != 0 || len(s.history) != 0 || len(s.notices) != 0 || contains(s.calls, plugins.DownloaderSourceAddCapability) || contains(s.calls, plugins.DownloaderSourceFindByTagCapability) {
				t.Fatal("source binding failure reached native add or later effects", s.calls)
			}
		})
	}
}
func TestAbsentSourceQBAllowsExistingTR(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "qbittorrent")
	s.configs = []plugins.BrushDownloaderReferenceV1{{ID: "target-qb", Name: "Target", Kind: "qbittorrent", Enabled: true}, {ID: "target-tr", Name: "Target", Kind: "transmission", Enabled: true}}
	s.absentIDs = map[string]bool{"target-qb": true}
	service, e := (sdkHost{caller: s.host}).Service(context.Background(), "Target")
	if e != nil || service == nil || service.Ref != "target-tr" || service.Inactive || service.Type != "transmission" || service.RuntimeKind != "transmission" {
		t.Fatal("absent QB wrapper hid existing TR", service, e)
	}
	s.absentIDs["target-tr"] = true
	service, e = (sdkHost{caller: s.host}).Service(context.Background(), "Target")
	if e != nil || service != nil {
		t.Fatal("absent wrappers became a source service", service, e)
	}
}
func TestCachedInactiveAndLaterQueryFailureKeepDistinctSourceBranches(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "qbittorrent")
	s.inactiveIDs = map[string]bool{"target": true}
	p := New()
	invoke(t, p, s, "init", s.config())
	p.state.Recheck["Target"] = []string{addedHash}
	s.calls = nil
	invoke(t, p, s, "check_recheck", nil)
	if len(p.state.Recheck["Target"]) != 1 || contains(s.calls, plugins.DownloaderSourceTasksCapability) || contains(s.calls, plugins.DownloaderConnectionCapability) {
		t.Fatal("constructor-inactive wrapper queried or lost pending tasks", s.calls)
	}
	// These are host-owned constructor facts, not a worker cache or live probe.
	s.inactiveIDs["target"] = false
	s.nativeQueryError = true
	s.calls = nil
	invoke(t, p, s, "check_recheck", nil)
	if len(p.state.Recheck["Target"]) != 0 || !contains(s.calls, plugins.DownloaderSourceTasksCapability) || contains(s.calls, plugins.DownloaderConnectionCapability) {
		t.Fatal("previously active wrapper did not expose source empty/error result", s.calls)
	}
}
func TestMetadataExtractorsDoNotClaimRuntimeFields(t *testing.T) {
	task := projectTasks(plugins.DownloaderTasksV1{Kind: "qbittorrent", Tasks: []plugins.DownloaderTaskV1{{Hash: sourceHash, QBittorrent: &plugins.QBittorrentTaskInfoV1{SavePath: ptr("/source"), Tags: ptr("a"), Category: ptr("Music"), State: ptr("pausedUP")}}}})[0]
	if GetHash(task, "transmission") != "" || GetSavePath(task, "transmission") != "" || len(GetLabels(task, "transmission")) != 0 || CanSeed(task, "transmission") {
		t.Fatal("runtime projection silently repaired metadata extractor mismatch")
	}
}
