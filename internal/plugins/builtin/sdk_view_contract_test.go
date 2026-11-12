package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
	"light-media/internal/plugins/larkmessager"
	"light-media/internal/plugins/serverstatus"
)

// Canonical SDK extension names and the older aliases must reach the same
// read-only host boundary, without forwarding caller-supplied configuration
// or input as a reverse-call payload.
func TestSDKViewCanonicalMethodsAndLegacyAliases(t *testing.T) {
	for _, tc := range []struct {
		manifest   plugins.Manifest
		handle     plugins.Handler
		capability string
		snapshot   json.RawMessage
	}{
		{autosignin.Manifest(), autosignin.Handle, autosignin.SnapshotCapability, json.RawMessage(`{"page":[]}`)},
		{larkmessager.Manifest(), larkmessager.Handle, larkmessager.PageCapability, json.RawMessage(`{"page":[]}`)},
		{SubscribeGroupManifest(), HandleSubscribeGroup, "subscriptions.rules.history", json.RawMessage(`[]`)},
		{SiteStatisticManifest(), HandleSiteStatistic, "site.statistics.read", json.RawMessage(`{}`)},
		{serverstatus.Manifest(), serverstatus.Handle, serverstatus.SnapshotCapability, json.RawMessage(`{}`)},
	} {
		t.Run(tc.manifest.PluginID, func(t *testing.T) {
			if err := tc.manifest.Validate(); err != nil {
				t.Fatal(err)
			}
			views := []string{"page"}
			if len(tc.manifest.Dashboards) > 0 {
				views = append(views, "dashboard")
			}
			for _, view := range views {
				t.Run(view, func(t *testing.T) {
					var previous json.RawMessage
					for _, method := range []string{view, "get_" + view} {
						if !slices.Contains(tc.manifest.Methods, method) {
							t.Fatalf("missing declared method %s", method)
						}
						raw := json.RawMessage(`{"action":"page","config":{"enable":true},"input":{"url":"https://untrusted.invalid","include_network_details":false}}`)
						called := 0
						host := func(_ context.Context, capability string, payload json.RawMessage) (json.RawMessage, error) {
							called++
							want := "{}"
							if tc.manifest.PluginID == "MPServerStatus" {
								if view == "page" {
									want = `{"include_network_details":true}`
								} else {
									want = `{"include_network_details":false}`
								}
							}
							if capability != tc.capability || string(payload) != want {
								t.Fatalf("host boundary changed: %s %s", capability, payload)
							}
							return tc.snapshot, nil
						}
						got, err := tc.handle(context.Background(), method, raw, host)
						if err != nil || called != 1 {
							t.Fatalf("%s calls=%d error=%v", method, called, err)
						}
						if previous != nil && !reflect.DeepEqual(got, previous) {
							t.Fatalf("canonical and legacy render differ: %s / %s", previous, got)
						}
						previous = got
						denied := errors.New("host denied")
						_, err = tc.handle(context.Background(), method, raw, func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, denied })
						if !errors.Is(err, denied) {
							t.Fatalf("host denial not preserved: %v", err)
						}
					}
				})
			}
		})
	}
}
