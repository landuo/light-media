package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/plugins/builtin"
	sr "light-media/internal/plugins/seedrescue"
	"path/filepath"
	"strings"
	"testing"
)

func seedRescueImportInput(t *testing.T, run string) PluginImport {
	t.Helper()
	manifest, _, ok := builtin.Lookup("SeedRescue")
	if !ok {
		t.Fatal("compiled source plugin missing")
	}
	raw, e := json.Marshal(manifest)
	if e != nil {
		t.Fatal(e)
	}
	jobs := map[string]any{}
	for n := 0; n < 1140; n++ {
		id := fmt.Sprintf("owned-%04d", n)
		jobs[id] = map[string]any{"id": id, "state": "waiting", "target_site_id": 7, "title": strings.Repeat("owned synthetic ", 170)}
	}
	data, e := json.Marshal(jobs)
	if e != nil {
		t.Fatal(e)
	}
	return PluginImport{RunID: run, Installation: PluginInstallation{InstanceID: "SeedRescue", PluginID: "SeedRescue", Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw}, Config: json.RawMessage(`{"enabled":true,"onlyonce":true,"notify":false}`), Data: map[string]json.RawMessage{sr.JobsKey: data, sr.RSSSnapshotsKey: json.RawMessage(` { "owned-feed": { "site_id":7,"url":"https://owned.invalid/?x=1&y=2" } } `), sr.ScanStatusKey: json.RawMessage(` { "state": "idle", "detail":"owned <value> & content" } `)}}
}
func TestSeedRescueImportedHistoryHasAuthenticatedColdOwnerState(t *testing.T) {
	ctx := context.Background()
	s, p := pluginStoreFixture(t)
	run, e := p.BeginMigration(ctx, strings.Repeat("9", 64), "SeedRescue", "SeedRescue")
	if e != nil {
		t.Fatal(e)
	}
	in := seedRescueImportInput(t, run.ID)
	receipt, e := p.Import(ctx, in)
	if e != nil {
		t.Fatal("data import failed", e)
	}
	installation, e := p.Installation(ctx, "SeedRescue")
	if e != nil || installation.DesiredEnabled {
		t.Fatal("ordinary import activated plugin")
	}
	state, _, e := p.seedRescueRead(ctx, s.DB, "SeedRescue", installation.Generation)
	if e != nil {
		t.Fatal("imported data missing authenticated owner proof", e)
	}
	if len(state.Values) != 3 || len(state.Runs) != 0 || len(state.Sources) != 0 {
		t.Fatal("owner import scope changed")
	}
	if len(in.Data[sr.JobsKey]) < 2_900_000 {
		t.Fatal("fixture no longer exercises source-sized history")
	}
	for key, raw := range in.Data {
		v, e := p.Data(ctx, "SeedRescue", key)
		if e != nil || !bytes.Equal(v.Value, raw) || state.Values[key].Revision != v.Revision || state.Values[key].SHA != tokenHash(string(v.Value)) {
			t.Fatal("imported value or proof changed", key)
		}
	}
	var count int
	if e = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs)+(SELECT COUNT(*) FROM plugin_schedules)+(SELECT COUNT(*) FROM plugin_event_subscriptions)`).Scan(&count); e != nil || count != 0 {
		t.Fatal("import executed business")
	}
	config, e := p.Config(ctx, "SeedRescue")
	if e != nil || !bytes.Equal(config.Value, in.Config) {
		t.Fatal("source enabled or one-shot flags changed")
	}
	edited, e := p.SaveConfig(ctx, "SeedRescue", config.Revision, map[string]any{"enabled": false, "onlyonce": false, "notify": true})
	if e != nil {
		t.Fatal(e)
	}
	var ownerBefore []byte
	if e = s.DB.QueryRow(`SELECT sealed FROM plugin_seed_rescue_state WHERE instance_id='SeedRescue'`).Scan(&ownerBefore); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Dir(s.lock.Name())
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e = NewPluginStore(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = p.seedRescueRead(ctx, s.DB, "SeedRescue", installation.Generation); e != nil {
		t.Fatal("cold owner cannot read imported history", e)
	}
	replay, e := p.Import(ctx, in)
	if e != nil || replay != receipt {
		t.Fatal("cold replay altered receipt")
	}
	current, e := p.Config(ctx, "SeedRescue")
	if e != nil || !bytes.Equal(current.Value, edited.Value) || current.Revision != edited.Revision {
		t.Fatal("receipt replay reverted user edits")
	}
	var ownerAfter []byte
	if e = s.DB.QueryRow(`SELECT sealed FROM plugin_seed_rescue_state WHERE instance_id='SeedRescue'`).Scan(&ownerAfter); e != nil || !bytes.Equal(ownerBefore, ownerAfter) {
		t.Fatal("receipt replay rewrote owner state")
	}
}

func TestSeedRescueHistoryImportAtomicFailuresAndDamagedGenesis(t *testing.T) {
	for name, trigger := range map[string]string{
		"data":          `BEFORE INSERT ON plugin_values WHEN NEW.kind='data' BEGIN SELECT RAISE(ABORT,'owned data failure'); END`,
		"owner":         `BEFORE UPDATE ON plugin_seed_rescue_state BEGIN SELECT RAISE(ABORT,'owned owner failure'); END`,
		"root":          `BEFORE UPDATE ON plugin_seed_rescue_roots BEGIN SELECT RAISE(ABORT,'owned root failure'); END`,
		"receipt":       `BEFORE INSERT ON plugin_migration_receipts BEGIN SELECT RAISE(ABORT,'owned receipt failure'); END`,
		"missing_state": `AFTER INSERT ON plugin_seed_rescue_roots BEGIN DELETE FROM plugin_seed_rescue_state; END`,
		"missing_root":  `AFTER INSERT ON plugin_seed_rescue_roots BEGIN DELETE FROM plugin_seed_rescue_roots; END`,
		"corrupt_state": `AFTER INSERT ON plugin_seed_rescue_roots BEGIN UPDATE plugin_seed_rescue_state SET sealed=x'00'; END`,
		"corrupt_root":  `AFTER INSERT ON plugin_seed_rescue_roots BEGIN UPDATE plugin_seed_rescue_roots SET sealed=x'00'; END`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, p := pluginStoreFixture(t)
			run, e := p.BeginMigration(ctx, strings.Repeat("8", 64), "SeedRescue", "SeedRescue")
			if e != nil {
				t.Fatal(e)
			}
			in := seedRescueImportInput(t, run.ID)
			if _, e = s.DB.Exec(`CREATE TRIGGER owned_import_failure ` + trigger); e != nil {
				t.Fatal(e)
			}
			if _, e = p.Import(ctx, in); e == nil {
				t.Fatal("failed import committed")
			}
			for _, table := range []string{"plugin_installations", "plugin_values", "plugin_seed_rescue_state", "plugin_seed_rescue_roots", "plugin_migration_receipts", "plugin_runs", "plugin_schedules", "plugin_event_subscriptions"} {
				var count int
				if e = s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); e != nil || count != 0 {
					t.Fatal("partial import survived", table, count, e)
				}
			}
			if current, e := p.Migration(ctx, run.ID); e != nil || current.State != "planned" {
				t.Fatal("failed import advanced migration", e)
			}
			if _, e = s.DB.Exec(`DROP TRIGGER owned_import_failure`); e != nil {
				t.Fatal(e)
			}
			if _, e = p.Import(ctx, in); e != nil {
				t.Fatal("clean retry failed", e)
			}
			installation, e := p.Installation(ctx, "SeedRescue")
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = p.seedRescueRead(ctx, s.DB, "SeedRescue", installation.Generation); e != nil {
				t.Fatal("retry left invalid owner", e)
			}
		})
	}
}

func TestSeedRescueHistoryImportKeepsOtherInstancesAndEmptyOwner(t *testing.T) {
	ctx := context.Background()
	s, p := pluginStoreFixture(t)
	unrelated := seedRescueImportInput(t, "unused")
	unrelated.Installation.InstanceID = "other-seed"
	unrelated.Data = nil
	run, e := p.BeginMigration(ctx, strings.Repeat("7", 64), "SeedRescue", "other-seed")
	if e != nil {
		t.Fatal(e)
	}
	unrelated.RunID = run.ID
	if _, e = p.Import(ctx, unrelated); e != nil {
		t.Fatal(e)
	}
	state, before, e := p.seedRescueRead(ctx, s.DB, "other-seed", 1)
	if e != nil || len(state.Values) != 0 {
		t.Fatal("empty import owner changed", e)
	}
	root, rootBefore, e := p.seedRescueRootRead(ctx, s.DB, "other-seed", 1)
	if e != nil || root.Sequence != 0 {
		t.Fatal("empty import advanced root", e)
	}
	run, e = p.BeginMigration(ctx, strings.Repeat("6", 64), "SeedRescue", "SeedRescue")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Import(ctx, seedRescueImportInput(t, run.ID)); e != nil {
		t.Fatal(e)
	}
	_, after, e := p.seedRescueRead(ctx, s.DB, "other-seed", 1)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("another instance owner changed", e)
	}
	_, rootAfter, e := p.seedRescueRootRead(ctx, s.DB, "other-seed", 1)
	if e != nil || !bytes.Equal(rootBefore, rootAfter) {
		t.Fatal("another instance root changed", e)
	}
}
