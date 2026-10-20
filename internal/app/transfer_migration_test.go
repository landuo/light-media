package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	transfer "light-media/internal/plugins/torrenttransfer"
)

func TestReviewedTransferRejectsUnreviewedSource(t *testing.T) {
	for _, v := range []string{"", "null", "{}", `{"version":1}`, `{"version":1,"config":null}`} {
		if ValidateReviewedTorrentTransferInput([]byte(v)) == nil {
			t.Fatal("unreviewed transfer source accepted")
		}
	}
}
func TestTransferReviewedOptionsKeepStableIDAndOriginalValue(t *testing.T) {
	b := &reviewedTransferBinding{Downloaders: []reviewedTransferDownloader{{"old-source", "qbittorrent", "source-id"}, {"old-target", "transmission", "target-id"}}}
	s := Settings{Downloaders: []DownloaderConfig{{Name: "renamed-source"}, {Name: "renamed-target"}}}
	s.Downloaders[0].ID, s.Downloaders[0].Kind = "source-id", "qbittorrent"
	s.Downloaders[1].ID, s.Downloaders[1].Kind = "target-id", "transmission"
	options, aliases, e := transferReferenceOptions(s, b)
	if e != nil || aliases["source-id"] != "old-source" || aliases["target-id"] != "old-target" || options[0]["title"] != "renamed-source" || options[1]["value"] != "old-target" {
		t.Fatal("renamed daemon was relabelled or rebound", e)
	}
	form, _, e := transfer.Form([]string{"renamed-source", "renamed-target"})
	if e != nil {
		t.Fatal(e)
	}
	transferSetFormReferenceOptions(form, options)
	raw := reviewedIYUUJSON(form)
	if !bytes.Contains(raw, []byte(`"value":"old-source"`)) || !bytes.Contains(raw, []byte(`"title":"renamed-target"`)) || bytes.Contains(raw, []byte(`"value":"renamed-source"`)) {
		t.Fatal("original form does not display migrated references")
	}
	s.Downloaders = append(s.Downloaders, DownloaderConfig{Name: "old-source"})
	s.Downloaders[2].ID, s.Downloaders[2].Kind = "reused-name-id", "qbittorrent"
	if _, _, e = transferReferenceOptions(s, b); e == nil {
		t.Fatal("another downloader reused original alias")
	}
}
func reviewedTransferOwned(t *testing.T, source reviewedTransferSnapshot) (*Store, *PluginStore, Settings) {
	t.Helper()
	s, p := pluginStoreFixture(t)
	settings := reviewedIYUUOwnedTargetSettings(t, s, reviewedIYUUSnapshot{Downloaders: source.Downloaders})
	return s, p, settings
}
func reviewedTransferRun(t *testing.T, p *PluginStore, raw []byte, id string) PluginMigrationRun {
	t.Helper()
	fp, e := ReviewedTorrentTransferFingerprint(raw)
	if e != nil {
		t.Fatal("source fingerprint invalid", e)
	}
	m, e := p.BeginMigration(context.Background(), fp, id, id)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestReviewedTransferSelectedStdin(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_REVIEWED_TRANSFER") != "owned-selected-stdin" {
		t.Skip("requires reviewed original source stdin; owned encrypted stores only")
	}
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, pluginImportLimit+1))
	if e != nil || len(raw) > pluginImportLimit {
		t.Fatal("source read failed")
	}
	var profiles []json.RawMessage
	if json.Unmarshal(raw, &profiles) != nil || len(profiles) != 2 {
		t.Fatal("two source instances required")
	}
	ctx := context.Background()
	for _, raw := range profiles {
		source, data, e := validateReviewedTransfer(raw)
		if e != nil {
			t.Fatal("source profile no longer matches reviewed snapshot", e)
		}
		t.Run(source.PluginID, func(t *testing.T) {
			for _, kind := range []string{"rename", "wrong-account", "wrong-endpoint", "missing-downloader", "duplicate-endpoint"} {
				t.Run("initial-"+kind, func(t *testing.T) {
					s, p, settings := reviewedTransferOwned(t, source)
					switch kind {
					case "rename":
						settings.Downloaders[0].Name = "Owned renamed source"
						settings.Downloaders[1].Name = "Owned renamed target"
					case "wrong-account":
						settings.Downloaders[1].Password = "owned-unmatched-password"
					case "wrong-endpoint":
						settings.Downloaders[1].BaseURL = "http://127.0.0.1:21993/transmission/rpc"
					case "missing-downloader":
						settings.Downloaders = settings.Downloaders[:1]
					case "duplicate-endpoint":
						x := settings.Downloaders[1]
						x.ID = "owned-duplicate"
						settings.Downloaders = append(settings.Downloaders, x)
					}
					reviewedIYUUOwnedSettings(t, s, settings)
					m := reviewedTransferRun(t, p, raw, source.PluginID)
					_, e := p.ImportReviewedTorrentTransfer(ctx, m.ID, raw)
					if (e == nil) != (kind == "rename") {
						t.Fatal("source connection identity validation mismatch", e)
					}
					if kind != "rename" {
						var count int
						if e = s.DB.QueryRow(`SELECT COUNT(*) FROM plugin_installations`).Scan(&count); e != nil || count != 0 {
							t.Fatal("rejected source installed partial state", e)
						}
					}
				})
			}
			t.Run("cold-replay-original-bytes-and-retained-lifecycle", func(t *testing.T) {
				s, p, settings := reviewedTransferOwned(t, source)
				defer func() { s.Close() }()
				m := reviewedTransferRun(t, p, raw, source.PluginID)
				if e = ValidateReviewedTorrentTransferTarget(ctx, s.DB, s.key, raw); e != nil {
					t.Fatal("read-only target validation", e)
				}
				receipt, e := p.ImportReviewedTorrentTransfer(ctx, m.ID, raw)
				if e != nil {
					t.Fatal(e)
				}
				in, e := p.Installation(ctx, source.PluginID)
				if e != nil || in.DesiredEnabled || in.Generation != 1 {
					t.Fatal("source instance was activated or replaced", e)
				}
				config, e := p.Config(ctx, source.PluginID)
				if e != nil || !bytes.Equal(config.Value, []byte(source.Config)) {
					t.Fatal("original config bytes changed", e)
				}
				for key, value := range data {
					stored, e := p.Data(ctx, source.PluginID, key)
					if e != nil || stored.Revision != 1 || !bytes.Equal(stored.Value, value) {
						t.Fatal("original history bytes changed", e)
					}
				}
				var runtime int
				if e = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs)+(SELECT COUNT(*) FROM plugin_schedules)+(SELECT COUNT(*) FROM plugin_event_subscriptions)`).Scan(&runtime); e != nil || runtime != 0 {
					t.Fatal("source flags replayed work", e)
				}
				dir := filepath.Dir(s.lock.Name())
				if e = s.Close(); e != nil {
					t.Fatal(e)
				}
				s, e = OpenStore(dir)
				if e != nil {
					t.Fatal(e)
				}
				p, e = NewPluginStore(ctx, s)
				if e != nil {
					t.Fatal(e)
				}
				a := &App{Store: s, pluginStore: p, config: settings}
				settings.Downloaders[0].Name = "Owned current source"
				settings.Downloaders[1].Name = "Owned current target"
				reviewedIYUUOwnedSettings(t, s, settings)
				a.config = settings
				bindings, e := a.transferBindingsFor(ctx, in, settings)
				if e != nil || len(bindings) != 2 || bindings[0].Service.Name != source.Downloaders[0].Name || bindings[1].Service.Ref != settings.Downloaders[1].ID || bindings[1].Service.Name != source.Downloaders[1].Name {
					t.Fatal("cold stable downloader mapping lost", e)
				}
				// Replaying cannot replace later configuration or core connections.
				if _, e = s.DB.Exec(`UPDATE plugin_installations SET desired_enabled=1 WHERE instance_id=?`, in.InstanceID); e != nil {
					t.Fatal(e)
				}
				cfg, e := transferSourceMap(config.Value)
				if e != nil {
					t.Fatal(e)
				}
				cfg["enabled"] = false
				updated, e := p.SaveConfig(ctx, in.InstanceID, 1, cfg)
				if e != nil {
					t.Fatal(e)
				}
				again, e := p.ImportReviewedTorrentTransfer(ctx, m.ID, raw)
				if e != nil || again.Digest != receipt.Digest || again.ImportedAt != receipt.ImportedAt {
					t.Fatal("same source receipt changed", e)
				}
				stored, e := p.Config(ctx, in.InstanceID)
				if e != nil || stored.Revision != updated.Revision || !bytes.Equal(stored.Value, updated.Value) {
					t.Fatal("replay overwrote user config", e)
				}
				if e = p.Uninstall(ctx, in.InstanceID); e != nil {
					t.Fatal(e)
				}
				if e = p.Install(ctx, in); e != nil {
					t.Fatal(e)
				}
				in, e = p.Installation(ctx, in.InstanceID)
				if e != nil || in.Generation != 3 {
					t.Fatal(e)
				}
				bindings, e = a.transferBindingsFor(ctx, in, settings)
				if e != nil || bindings[0].Service.Name != source.Downloaders[0].Name {
					t.Fatal("retained reinstall relabelled old config/history", e)
				}
				if e = p.Reset(ctx, in.InstanceID); e != nil {
					t.Fatal(e)
				}
				in, _ = p.Installation(ctx, in.InstanceID)
				bindings, e = a.transferBindingsFor(ctx, in, settings)
				if e != nil || bindings[0].Service.Name != settings.Downloaders[0].Name {
					t.Fatal("reset did not restore native choices", e)
				}
				for key := range data {
					if _, e = p.Data(ctx, in.InstanceID, key); e != sql.ErrNoRows {
						t.Fatal("reset retained source history", e)
					}
					break
				}
			})
			for _, defect := range []string{"binding", "final-receipt", "final-digest"} {
				t.Run("missing-proof-"+defect, func(t *testing.T) {
					s, p, _ := reviewedTransferOwned(t, source)
					m := reviewedTransferRun(t, p, raw, source.PluginID)
					if _, e = p.ImportReviewedTorrentTransfer(ctx, m.ID, raw); e != nil {
						t.Fatal(e)
					}
					query := `DELETE FROM records WHERE kind='plugin_original_transfer_references'`
					if defect == "final-receipt" {
						query = `DELETE FROM plugin_migration_receipts`
					} else if defect == "final-digest" {
						query = `UPDATE plugin_migration_receipts SET digest='owned-altered-digest'`
					}
					if _, e = s.DB.Exec(query); e != nil {
						t.Fatal(e)
					}
					if _, e = p.ImportReviewedTorrentTransfer(ctx, m.ID, raw); e == nil {
						t.Fatal("replay rebuilt missing or changed proof")
					}
					if _, _, e = p.SaveConfigAndQueue(ctx, source.PluginID, 1, json.RawMessage(source.Config)); e == nil {
						t.Fatal("unproven reference activated")
					}
				})
			}
			for _, point := range []struct{ name, trigger string }{
				{"config", `CREATE TRIGGER owned_fail BEFORE INSERT ON plugin_values WHEN NEW.kind='config' BEGIN SELECT RAISE(ABORT,'owned_config_failure'); END`},
				{"data", `CREATE TRIGGER owned_fail BEFORE INSERT ON plugin_values WHEN NEW.kind='data' BEGIN SELECT RAISE(ABORT,'owned_data_failure'); END`},
				{"binding", `CREATE TRIGGER owned_fail BEFORE INSERT ON records WHEN NEW.kind='plugin_original_transfer_references' BEGIN SELECT RAISE(ABORT,'owned_binding_failure'); END`},
				{"receipt", `CREATE TRIGGER owned_fail BEFORE INSERT ON plugin_migration_receipts BEGIN SELECT RAISE(ABORT,'owned_receipt_failure'); END`},
			} {
				t.Run("rollback-"+point.name, func(t *testing.T) {
					s, p, _ := reviewedTransferOwned(t, source)
					if _, e = s.DB.Exec(point.trigger); e != nil {
						t.Fatal(e)
					}
					m := reviewedTransferRun(t, p, raw, source.PluginID)
					if _, e = p.ImportReviewedTorrentTransfer(ctx, m.ID, raw); e == nil {
						t.Fatal("injected failure committed")
					}
					var count int
					if e = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_installations)+(SELECT COUNT(*) FROM plugin_values)+(SELECT COUNT(*) FROM plugin_migration_receipts)+(SELECT COUNT(*) FROM records WHERE kind='plugin_original_transfer_references')`).Scan(&count); e != nil || count != 0 {
						t.Fatal("failed migration left partial owner or data", e)
					}
					var roots int
					if e = s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='plugin_transfer_roots'`).Scan(&roots); e != nil {
						t.Fatal(e)
					}
					if roots > 0 {
						if e = s.DB.QueryRow(`SELECT COUNT(*) FROM plugin_transfer_roots`).Scan(&count); e != nil || count != 0 {
							t.Fatal("failed migration retained an owner root", e)
						}
					}
				})
			}
			t.Run("offline-cli", func(t *testing.T) { reviewedTransferSelectedCLI(t, source, raw) })
			t.Run("actual-sdk", func(t *testing.T) { reviewedTransferSelectedSDK(t, source, raw, false) })
			t.Run("actual-sdk-lost-ack", func(t *testing.T) { reviewedTransferSelectedSDK(t, source, raw, true) })
		})
	}
	t.Run("both-instances-remain-independent", func(t *testing.T) {
		first, _, e := validateReviewedTransfer(profiles[0])
		if e != nil {
			t.Fatal(e)
		}
		s, p, settings := reviewedTransferOwned(t, first)
		for _, raw := range profiles {
			source, _, e := validateReviewedTransfer(raw)
			if e != nil {
				t.Fatal(e)
			}
			m := reviewedTransferRun(t, p, raw, source.PluginID)
			if _, e = p.ImportReviewedTorrentTransfer(ctx, m.ID, raw); e != nil {
				t.Fatal("joint source import", e)
			}
			for key, expected := range source.Data {
				actual, e := p.Data(ctx, source.PluginID, key)
				if e != nil || !bytes.Equal(actual.Value, []byte(expected)) {
					t.Fatal("joint source history lost identity", e)
				}
			}
			in, _ := p.Installation(ctx, source.PluginID)
			a := &App{Store: s, pluginStore: p, config: settings}
			if _, e = a.transferBindingsFor(ctx, in, settings); e != nil {
				t.Fatal("joint source reference unavailable", e)
			}
		}
		var instances, values, receipts int
		if e = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_installations),(SELECT COUNT(*) FROM plugin_values WHERE kind='data'),(SELECT COUNT(*) FROM plugin_migration_receipts)`).Scan(&instances, &values, &receipts); e != nil || instances != 2 || values != 458 || receipts != 2 {
			t.Fatal("original and music instance merged or overwrote each other", e)
		}
	})
}
