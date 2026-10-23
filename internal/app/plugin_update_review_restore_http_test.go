package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

func TestPluginReviewRestoreActualHTTPAndServer(t *testing.T) {
	for _, clone := range []bool{false, true} {
		name := "default"
		if clone {
			name = "clone"
		}
		t.Run(name, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			cookie := setupTestAdmin(t, a)
			p := a.pluginStore
			transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
			if transferServerBinary.err != nil {
				t.Fatal(transferServerBinary.err)
			}
			a.pluginCommand = func(id string) (plugins.Command, error) {
				return plugins.Command{Path: transferServerBinary.path, Args: []string{"--plugin-worker", id}}, nil
			}
			installCloneSource(t, p, builtin.CategoryEditorManifest())
			if _, err := p.SaveConfig(ctx, "CategoryEditor", 0, map[string]any{"enabled": false, "content": "movie: {}"}); err != nil {
				t.Fatal(err)
			}
			id := "CategoryEditor"
			if clone {
				in, err := p.Clone(ctx, id, PluginCloneRequest{Suffix: "restored", Name: "Held clone"})
				if err != nil {
					t.Fatal(err)
				}
				id = in.InstanceID
			}
			sourceBefore, _ := p.Config(ctx, "CategoryEditor")
			c, err := p.Config(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			c, err = p.SaveConfig(ctx, id, c.Revision, map[string]any{"enabled": true, "content": customJapaneseMovieCategory})
			if err != nil {
				t.Fatal(err)
			}
			r := updateOldCategory(t, p, id)
			if _, err = p.updateBuiltin(ctx, id, r); err != nil {
				t.Fatal(err)
			}
			if _, err = p.SaveData(ctx, id, "owned", 0, map[string]any{"retained": true}); err != nil {
				t.Fatal(err)
			}
			beforeData, _ := p.Data(ctx, id, "owned")
			if w := callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/uninstall", "{}", cookie, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var response struct {
				Data []pluginCard `json:"data"`
			}
			w := callTest(t, a.Handler(), "GET", "/api/plugins/market", "", cookie, "")
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			var offer *pluginReviewRestoreRequest
			count := 0
			for _, card := range response.Data {
				if card.InstanceID == id {
					count++
					offer = card.RestoreReview
					if card.Installed || !card.UpdateReviewPending || card.RestoreReviewBlockedReason != "" {
						t.Fatal(card)
					}
					if clone && card.Name != "Held clone" {
						t.Fatal("lineage presentation lost", card)
					}
				}
			}
			if count != 1 || offer == nil {
				t.Fatal("retained instance is not uniquely discoverable", count, offer)
			}
			body, _ := json.Marshal(offer)
			if w = callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/restore-reviewed", string(body), cookie, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			in := lifecycleInstallation(t, p, id)
			if !in.Installed || in.Generation != offer.Generation+1 {
				t.Fatal(in)
			}
			after, _ := p.Config(ctx, id)
			data, _ := p.Data(ctx, id, "owned")
			if after.Revision != c.Revision || !bytes.Equal(after.Value, c.Value) || data.Revision != beforeData.Revision || !bytes.Equal(data.Value, beforeData.Value) {
				t.Fatal("restore changed retained values")
			}
			if w = callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/restore-reviewed", string(body), cookie, ""); w.Code == 200 {
				t.Fatal("stale second restore accepted")
			}
			if err = a.initializeNativePlugins(ctx); err != nil {
				t.Fatal(err)
			}
			var runs int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, id).Scan(&runs); err != nil || runs != 0 {
				t.Fatal("restore queued old intent", runs, err)
			}
			if _, err = os.Stat(filepath.Join(a.Dir, "category.yaml")); !os.IsNotExist(err) {
				t.Fatal("restore executed", err)
			}
			if w = callTest(t, a.Handler(), "GET", "/api/plugins/"+id+"/form", "", cookie, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			save, _ := json.Marshal(map[string]any{"revision": after.Revision, "config": json.RawMessage(after.Value)})
			if w = callTest(t, a.Handler(), "PUT", "/api/plugins/"+id+"/config", string(save), cookie, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			run, err := p.ClaimRun(ctx)
			if err != nil || run.InstanceID != id {
				t.Fatal(run, err)
			}
			if err = a.executePluginRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			terminal, err := p.Run(ctx, run.ID)
			if err != nil || terminal.State != "succeeded" {
				t.Fatal(terminal.State, terminal.ErrorCode, err)
			}
			raw, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
			if err != nil || string(raw) != customJapaneseMovieCategory {
				t.Fatal("explicit save did not execute", err)
			}
			if clone {
				sourceAfter, _ := p.Config(ctx, "CategoryEditor")
				if sourceBefore.Revision != sourceAfter.Revision || !bytes.Equal(sourceBefore.Value, sourceAfter.Value) {
					t.Fatal("clone restore affected source")
				}
			}
			executionLeaseAssertCount(t, p, 0)
		})
	}
}
