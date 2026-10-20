package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sr "light-media/internal/plugins/seedrescue"
)

// This fixture reads the selected original only through stdin, imports into an
// owned encrypted store and performs no site/downloader/network operations.
func TestSeedRescueHistoryImportSelectedStdin(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_SEED_RESCUE_HISTORY") != "owned-selected-stdin" {
		t.Skip("requires selected source stdin; owned temporary stores only")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, pluginImportLimit+1))
	if err != nil || len(raw) > pluginImportLimit {
		t.Fatal("selected history input unavailable")
	}
	var input struct {
		SourceSHA string            `json:"source_sha256"`
		Installed int               `json:"installed_count"`
		Config    string            `json:"config"`
		Data      map[string]string `json:"data"`
		Files     int               `json:"file_count"`
	}
	if cleanupJournalStrict(raw, &input) != nil || input.SourceSHA != "e379ec20d09fbd3bc461b1b8f51ec508d0270a63a841763cc965c39adb98d375" || input.Installed != 1 || input.Files != 0 || tokenHash(input.Config) != "62a36d3f29fc1c72b83a413ab301b16a5fe63777f41a685fbcd099b060d53756" {
		t.Fatal("selected source does not match reviewed snapshot")
	}
	want := map[string]struct {
		bytes int
		sha   string
	}{
		sr.JobsKey:         {2923784, "c14324cce121787b13244445caf817b741ee77cf7d9efa62b3b720e61cd2dc7d"},
		sr.RSSSnapshotsKey: {10126, "d8b418d013527785fb7510285fc096a7e53b15f58baf03df5811a2cbceffca31"},
		sr.ScanStatusKey:   {228, "3b3ae5d0e7ba3069a2c3fb45575f3b9fae7f299b877ce2fd9fc0ce1f329f41f4"},
	}
	if len(input.Data) != len(want) {
		t.Fatal("selected KV set changed")
	}
	for key, proof := range want {
		if value := input.Data[key]; len(value) != proof.bytes || tokenHash(value) != proof.sha {
			t.Fatal("selected KV differs from reviewed snapshot")
		}
	}
	ctx := context.Background()
	s, p := pluginStoreFixture(t)
	run, err := p.BeginMigration(ctx, strings.Repeat("5", 64), "SeedRescue", "SeedRescue")
	if err != nil {
		t.Fatal(err)
	}
	in := seedRescueImportInput(t, run.ID)
	in.Config = json.RawMessage(input.Config)
	in.Data = map[string]json.RawMessage{}
	for key, value := range input.Data {
		in.Data[key] = json.RawMessage(value)
	}
	receipt, err := p.Import(ctx, in)
	if err != nil {
		t.Fatal("selected history import failed", err)
	}
	installation, err := p.Installation(ctx, "SeedRescue")
	if err != nil || installation.DesiredEnabled {
		t.Fatal("history import activated business")
	}
	state, _, err := p.seedRescueRead(ctx, s.DB, "SeedRescue", installation.Generation)
	if err != nil || len(state.Values) != 3 || len(state.Runs) != 0 || len(state.Sources) != 0 || state.AppliedRevision != 0 {
		t.Fatal("selected owner history invalid", err)
	}
	var jobs map[string]json.RawMessage
	if json.Unmarshal(in.Data[sr.JobsKey], &jobs) != nil || len(jobs) != 1140 {
		t.Fatal("selected task history changed")
	}
	for key, value := range in.Data {
		stored, err := p.Data(ctx, "SeedRescue", key)
		if err != nil || !bytes.Equal(stored.Value, value) {
			t.Fatal("selected history bytes changed")
		}
		var sealed []byte
		if err = s.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='SeedRescue' AND kind='data' AND item_key=?`, key).Scan(&sealed); err != nil || bytes.Contains(sealed, value) {
			t.Fatal("selected history not encrypted")
		}
	}
	dir := filepath.Dir(s.lock.Name())
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err = NewPluginStore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.seedRescueRead(ctx, s.DB, "SeedRescue", installation.Generation); err != nil {
		t.Fatal("selected cold history rejected", err)
	}
	if replay, err := p.Import(ctx, in); err != nil || replay != receipt {
		t.Fatal("selected cold replay changed receipt")
	}
	config, err := seedRescueConfig(in.Config)
	if err != nil {
		t.Fatal("selected source config invalid")
	}
	projection := sr.NewReadProjection(ctx, seedRescueImportedHistoryReader{p: p}, config, sr.Object{"state": "idle"}, false)
	seen := map[string]bool{}
	for _, view := range []string{"current", "history"} {
		for page := 1; ; page++ {
			response, err := projection.GetStatus(ctx, sr.StatusFilter{View: view, Page: page, PageSize: 100})
			if err != nil || !response.Success {
				t.Fatal("selected source view failed")
			}
			body := response.Data.(sr.Object)
			summary := body["summary"].(sr.Object)
			if summary["total"] != 1140 || summary["downloading"] != 5 || summary["waiting_iyuu"] != 7 || summary["awaiting_selection"] != 104 {
				t.Fatal("selected view lost source task states")
			}
			for _, row := range body["jobs"].([]sr.Object) {
				hash, ok := row["target_hash"].(string)
				if !ok || jobs[hash] == nil || seen[hash] {
					t.Fatal("selected view duplicated or lost task identity")
				}
				seen[hash] = true
			}
			if page == body["pagination"].(sr.Object)["pages"].(int) {
				break
			}
		}
	}
	if len(seen) != 1140 {
		t.Fatal("selected paginated views omitted tasks")
	}
	var count int
	if err = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs)+(SELECT COUNT(*) FROM plugin_schedules)+(SELECT COUNT(*) FROM plugin_event_subscriptions)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("selected history read executed business")
	}
	t.Log("selected_source_history_verified: KV=3, tasks=1140, cold_owner=valid, current_and_history_views=1140, external_effects=0; reference bindings not tested here")
}

type seedRescueImportedHistoryReader struct {
	sr.Host // Any unexpected mutation/IO fails instead of contacting a service.
	p       *PluginStore
}

func (h seedRescueImportedHistoryReader) Guard(ctx context.Context) error { return ctx.Err() }
func (h seedRescueImportedHistoryReader) LoadData(ctx context.Context, key string) (json.RawMessage, error) {
	v, err := h.p.Data(ctx, "SeedRescue", key)
	return v.Value, err
}
func (h seedRescueImportedHistoryReader) Options(context.Context) (sr.Object, error) {
	return sr.Object{"sites": []sr.Object{}, "downloaders": []sr.Object{}}, nil
}
