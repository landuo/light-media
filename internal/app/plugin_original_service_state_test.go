package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	iyuu "light-media/internal/plugins/iyuuautoseed"
	transfer "light-media/internal/plugins/torrenttransfer"
)

type originalTransferInitHost struct {
	transfer.Host // Any unexpected business effect is a nil-interface panic.
	existsError   error
	writes        int
	messages      int
}

func (h *originalTransferInitHost) Exists(context.Context, string) (bool, error) {
	return true, h.existsError
}
func (h *originalTransferInitHost) SaveConfig(context.Context, map[string]any) error {
	h.writes++
	return nil
}
func (h *originalTransferInitHost) SystemMessage(context.Context, string, string) error {
	h.messages++
	return nil
}
func originalTransferServiceConfig() map[string]any {
	return map[string]any{"enabled": true, "cron": "0 3 * * *", "fromdownloader": "owned-from", "todownloader": "owned-to", "fromtorrentpath": "/owned-synthetic", "add_torrent_tags": "owned"}
}
func originalIYUUServiceConfig() map[string]any {
	return map[string]any{"enabled": true, "cron": "0 3 * * *", "token": "owned-synthetic-token", "downloaders": []any{"owned-downloader"}, "sites": []any{1}, "size": json.Number("0")}
}

// This is Config checkpoint equivalence, not full native downloader execution.
func TestPluginOriginalTransferAppliedServiceCheckpoint(t *testing.T) {
	for _, name := range []string{"nil", "empty", "disabled", "enabled-cron", "onlyonce", "exists-error-partial", "invalid-tags-partial", "invalid-cron"} {
		t.Run(name, func(t *testing.T) {
			input := originalTransferServiceConfig()
			h := &originalTransferInitHost{}
			switch name {
			case "nil":
				input = nil
			case "empty":
				input = map[string]any{}
			case "disabled":
				input["enabled"] = false
			case "onlyonce":
				input["onlyonce"] = true
			case "exists-error-partial":
				h.existsError = errors.New("owned exists failure")
			case "invalid-tags-partial":
				input["add_torrent_tags"] = true
			case "invalid-cron":
				input["cron"] = "not a cron"
			}
			cfg := transfer.NewConfig()
			result, initErr := cfg.Init(context.Background(), input, h)
			wantInitErr := name == "exists-error-partial" || name == "invalid-tags-partial"
			if (initErr != nil) != wantInitErr {
				t.Fatalf("init error=%v want error=%v", initErr, wantInitErr)
			}
			snap, err := cfg.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			restored, err := transfer.RestoreConfig(snap)
			if name == "invalid-tags-partial" {
				if err == nil {
					t.Fatal("invalid partial tags accepted as valid checkpoint")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.State() != restored.State() {
				t.Fatal("restored state changed")
			}
			before, beforeErr := cfg.Services("UTC")
			after, afterErr := restored.Services("UTC")
			if !reflect.DeepEqual(before, after) || (beforeErr != nil) != (afterErr != nil) {
				t.Fatalf("service checkpoint differs: %v/%v vs %v/%v", before, beforeErr, after, afterErr)
			}
			if name == "invalid-cron" && beforeErr == nil {
				t.Fatal("invalid cron accepted")
			}
			if name == "onlyonce" {
				if snap["onlyonce"] != false || !result.SourceOnlyOnce || h.writes != 1 || len(result.Jobs) != 1 || result.Jobs[0].Kind != "date" {
					t.Fatal("onlyonce native mutation not preserved")
				}
			}
			if name == "exists-error-partial" && (!cfg.State() || len(result.Jobs) != 0 || h.writes != 0) {
				t.Fatal("unexpected error partial state")
			}
		})
	}
}

func TestPluginOriginalIYUUAppliedServiceCheckpoint(t *testing.T) {
	for _, name := range []string{"nil", "empty", "disabled", "enabled-cron", "onlyonce", "invalid-size-partial", "invalid-cron"} {
		t.Run(name, func(t *testing.T) {
			input := originalIYUUServiceConfig()
			switch name {
			case "nil":
				input = nil
			case "empty":
				input = map[string]any{}
			case "disabled":
				input["enabled"] = false
			case "onlyonce":
				input["onlyonce"] = true
			case "invalid-size-partial":
				input["size"] = "owned-not-number"
			case "invalid-cron":
				input["cron"] = "not a cron"
			}
			cfg := iyuu.NewConfig()
			result, initErr := cfg.Init(input, []any{1})
			if (initErr != nil) != (name == "invalid-size-partial") {
				t.Fatalf("init: %v", initErr)
			}
			snap, err := cfg.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			restored, err := iyuu.RestoreConfig(snap)
			if name == "invalid-size-partial" {
				if err == nil {
					t.Fatal("partial nil cache members accepted as completed checkpoint")
				}
				if !cfg.State() {
					t.Fatal("partial native state unexpectedly discarded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.State() != restored.State() {
				t.Fatal("restored state changed")
			}
			before, beforeErr := cfg.Services("UTC")
			after, afterErr := restored.Services("UTC")
			if !reflect.DeepEqual(before, after) || (beforeErr != nil) != (afterErr != nil) {
				t.Fatalf("service checkpoint differs: %v/%v vs %v/%v", before, beforeErr, after, afterErr)
			}
			if name == "invalid-cron" && beforeErr == nil {
				t.Fatal("invalid cron accepted")
			}
			if name == "onlyonce" {
				if snap["onlyonce"] != false || !result.SourceOnlyOnce || len(result.Jobs) != 2 || result.Jobs[0].Kind != "date" || result.Jobs[1].Kind != "interval" {
					t.Fatal("onlyonce and private recheck decisions changed")
				}
			}
		})
	}
}

// Deliberately isolates the applied-object getter contract. It does not claim
// the whole App Init path has run: real Config.Init supplies the snapshot.
func TestPluginOriginalServicesIgnoreUnappliedStoredConfiguration(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, id := range []string{"TorrentTransfer", "IYUUAutoSeed"} {
		t.Run(id, func(t *testing.T) {
			var snapshot map[string]any
			if id == "TorrentTransfer" {
				installCloneSource(t, a.pluginStore, transfer.Manifest(id))
				cfg := transfer.NewConfig()
				if _, err := cfg.Init(ctx, originalTransferServiceConfig(), &originalTransferInitHost{}); err != nil {
					t.Fatal(err)
				}
				snapshot, _ = cfg.Snapshot()
			} else {
				installCloneSource(t, a.pluginStore, iyuu.Manifest())
				cfg := iyuu.NewConfig()
				if _, err := cfg.Init(originalIYUUServiceConfig(), []any{1}); err != nil {
					t.Fatal(err)
				}
				snapshot, _ = cfg.Snapshot()
			}
			stored, err := a.pluginStore.SaveConfig(ctx, id, 0, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			in, err := a.pluginStore.Installation(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			object := &originalPluginInstance{app: a, installation: in, appliedConfig: raw}
			before, err := object.getServices(ctx)
			if err != nil || len(before) != 1 {
				t.Fatalf("initial services=%v error=%v", before, err)
			}
			snapshot["enabled"] = false
			snapshot["cron"] = "invalid-unapplied-cron"
			if _, err = a.pluginStore.SaveConfig(ctx, id, stored.Revision, snapshot); err != nil {
				t.Fatal(err)
			}
			after, err := object.getServices(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("unapplied store mutated loaded services: %v %v", after, err)
			}
		})
	}
}

func TestPluginOriginalPartialNativeObjectDoesNotRequireCheckpointRestore(t *testing.T) {
	for _, id := range []string{"TorrentTransfer", "IYUUAutoSeed"} {
		t.Run(id, func(t *testing.T) {
			p := &originalPluginInstance{installation: PluginInstallation{PluginID: id}}
			var want bool
			switch id {
			case "TorrentTransfer":
				input := originalTransferServiceConfig()
				input["add_torrent_tags"] = true
				cfg := transfer.NewConfig()
				_, err := cfg.Init(context.Background(), input, &originalTransferInitHost{})
				if err == nil {
					t.Fatal("expected original partial init error")
				}
				snap, err := cfg.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				p.appliedConfig, _ = json.Marshal(snap)
				p.nativeState = cfg
				want = cfg.State()
				if _, err = transfer.RestoreConfig(snap); err == nil {
					t.Fatal("invalid checkpoint unexpectedly accepted")
				}
			case "IYUUAutoSeed":
				input := originalIYUUServiceConfig()
				input["size"] = "bad-size"
				cfg := iyuu.NewConfig()
				_, err := cfg.InitContext(context.Background(), input, []any{1})
				if err == nil {
					t.Fatal("expected original partial init error")
				}
				snap, err := cfg.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				p.appliedConfig, _ = json.Marshal(snap)
				p.nativeState = cfg
				want = cfg.State()
				if _, err = iyuu.RestoreConfig(snap); err == nil {
					t.Fatal("invalid checkpoint unexpectedly accepted")
				}
			}
			got, err := p.getState(context.Background())
			if err != nil || got != want {
				t.Fatalf("actual partial state changed: got=%v want=%v err=%v", got, want, err)
			}
			if _, err = p.getServices(context.Background()); err != nil {
				t.Fatalf("actual partial service getter must not decode checkpoint: %v", err)
			}
		})
	}
}
