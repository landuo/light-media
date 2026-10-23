package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// The ordinary production server is the real SDK worker. The host still runs
// under the selected normal/race test instrumentation; no fake host dispatch.
func TestPluginExecutionLeaseSubscribeSnapshotReplacementAndReset(t *testing.T) {
	transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
	if transferServerBinary.err != nil {
		t.Fatal(transferServerBinary.err)
	}
	for _, clear := range []bool{false, true} {
		name := "unchanged_revision"
		if clear {
			name = "business_revision"
		}
		t.Run(name, func(t *testing.T) {
			a := newTestApp(t)
			p := a.pluginStore
			ctx := context.Background()
			a.pluginCommand = func(id string) (plugins.Command, error) {
				return plugins.Command{Path: transferServerBinary.path, Args: []string{"--plugin-worker", id}}, nil
			}
			manifest := builtin.SubscribeGroupManifest()
			raw, _ := json.Marshal(manifest)
			id := manifest.PluginID
			if err := p.Install(ctx, PluginInstallation{InstanceID: id, PluginID: id, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw}); err != nil {
				t.Fatal(err)
			}
			if _, err := p.SaveData(ctx, id, "history", 0, []string{"owned synthetic"}); err != nil {
				t.Fatal(err)
			}
			config, queued, err := p.SaveConfigAndQueue(ctx, id, 0, map[string]any{"enabled": true, "clear": clear, "category": false})
			if err != nil {
				t.Fatal(err)
			}
			claimed := executionLeaseClaim(t, p, queued)
			if err = a.executePluginRun(ctx, claimed); err != nil {
				t.Fatal("actual server configure could not release original claim", err)
			}
			actual, err := p.Run(ctx, claimed.ID)
			if err != nil || actual.State != "succeeded" {
				t.Fatal("configure not succeeded", actual.State, actual.ErrorCode, err)
			}
			after, err := p.Config(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if clear && (after.Revision <= config.Revision || actual.ConfigRevision != after.Revision) {
				t.Fatal("business revision was not retained", config.Revision, after.Revision, actual.ConfigRevision)
			}
			if !clear && after.Revision != config.Revision {
				t.Fatal("unexpected config change")
			}
			var decoded map[string]any
			if json.Unmarshal(after.Value, &decoded) != nil {
				t.Fatal("bad config")
			}
			if clear && decoded["clear"] != false {
				t.Fatal("one-shot clearing lost")
			}
			executionLeaseAssertCompletion(t, p, claimed)
			executionLeaseAssertCount(t, p, 0)
			resetCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err = p.Reset(resetCtx, id); err != nil {
				t.Fatal("reset could not join completed production worker", err)
			}
			executionLeaseAssertCount(t, p, 0)
		})
	}
}
