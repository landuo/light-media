package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	transfer "light-media/internal/plugins/torrenttransfer"
)

func TestTransferOriginalRetainedReinstallDoesNotClaimNativeNames(t *testing.T) {
	for _, id := range []string{"TorrentTransfer", "TorrentTransfermusic"} {
		t.Run(id, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			instance := id + ".original"
			m, e := p.BeginMigration(ctx, strings.Repeat("d", 64), id, instance)
			if e != nil {
				t.Fatal(e)
			}
			in := PluginInstallation{InstanceID: instance, PluginID: id, Version: "1.10.3", ManifestSchemaVersion: 1, Manifest: reviewedIYUUJSON(transfer.Manifest(id))}
			config := json.RawMessage(`{"enabled":true,"fromdownloader":"same-name-source","todownloader":"same-name-target"}`)
			if _, e = p.Import(ctx, PluginImport{RunID: m.ID, Installation: in, Config: config, Data: map[string]json.RawMessage{"owned-history": json.RawMessage(`{"to_download":"same-name-target"}`)}}); e != nil {
				t.Fatal(e)
			}
			if e = p.Uninstall(ctx, instance); e != nil {
				t.Fatal(e)
			}
			if e = p.Install(ctx, in); e != nil {
				t.Fatal(e)
			}
			if _, _, e = p.SaveConfigAndQueue(ctx, instance, 1, config); pluginErrorCodeInternal(e) != "TORRENT_TRANSFER_REFERENCES_UNCLAIMED" {
				t.Fatal("retained original names were silently promoted to current native names", e)
			}
			if _, e = p.Data(ctx, instance, "owned-history"); e != nil {
				t.Fatal("retained history lost", e)
			}
			if e = p.Reset(ctx, instance); e != nil {
				t.Fatal(e)
			}
			if _, _, e = p.SaveConfigAndQueue(ctx, instance, 0, config); e != nil {
				t.Fatal("explicit data reset did not permit a fresh native selection", e)
			}
		})
	}
}
