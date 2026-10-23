package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// finishClaimedFixtureRun completes synthetic Store setup using the actual claim
// capability. Negative/late/SQL-fault tests deliberately retain one explicit live
// execution context instead of using this positive-only setup adapter.
func finishClaimedFixtureRun(ctx context.Context, p *PluginStore, run PluginRun, state, code string, result json.RawMessage) error {
	execution, err := p.activateSupervisedRun(ctx, run)
	if err != nil {
		return err
	}
	defer run.executionLease.runCleanup()
	if err = p.FinishRun(execution, run.ID, state, code, result); err != nil {
		return err
	}
	return p.releaseSupervisedRun(run)
}

func TestPluginRunsAtomicSaveAndEncryptedSnapshot(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	installStoredPlugin(t, p, "one")
	secret := "synthetic-private-run-config-19273"
	value, run, err := p.SaveConfigAndQueue(ctx, "one", 0, map[string]any{"password": secret, "enabled": true})
	if err != nil || value.Revision != 1 || run.ConfigRevision != 1 || run.State != "pending" {
		t.Fatal(value, run, err)
	}
	public, _ := json.Marshal(run)
	if bytes.Contains(public, []byte(secret)) || bytes.Contains(public, []byte("input")) || bytes.Contains(public, []byte("result")) {
		t.Fatal("public run metadata leaked private snapshot", string(public))
	}
	var sealed []byte
	if err := s.DB.QueryRow(`SELECT input FROM plugin_runs WHERE id=?`, run.ID).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte(secret)) {
		t.Fatal("run snapshot was not encrypted", err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER synthetic_run_failure BEFORE INSERT ON plugin_runs BEGIN SELECT RAISE(ABORT,'synthetic run insert failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.SaveConfigAndQueue(ctx, "one", 1, map[string]string{"password": "changed"}); err == nil {
		t.Fatal("run insert failure reported success")
	}
	current, err := p.Config(ctx, "one")
	if err != nil || current.Revision != 1 || !bytes.Equal(current.Value, value.Value) {
		t.Fatal("queue failure committed a config without its invocation", current, err)
	}
}

func TestPluginRunsClaimOnceAndCancelStaleSnapshots(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	installStoredPlugin(t, p, "one")
	_, stale, err := p.SaveConfigAndQueue(ctx, "one", 0, map[string]string{"value": "old"})
	if err != nil {
		t.Fatal(err)
	}
	_, current, err := p.SaveConfigAndQueue(ctx, "one", 1, map[string]string{"value": "current"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	claimed := []string{}
	var winningClaim PluginRun
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := p.ClaimRun(ctx)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				t.Error(err)
			}
			if err == nil {
				mu.Lock()
				claimed = append(claimed, run.ID)
				winningClaim = run
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	old, _ := p.Run(ctx, stale.ID)
	if len(claimed) != 1 || claimed[0] != current.ID || old.State != "canceled" || old.ErrorCode != "PLUGIN_SNAPSHOT_STALE" {
		t.Fatal("stale or duplicate invocation was claimed", claimed, old.State, old.ErrorCode)
	}
	runContext := executionLeaseActivate(t, p, winningClaim)
	if err := p.FinishRun(runContext, current.ID, "succeeded", "", json.RawMessage(`{"private":"synthetic-worker-result"}`)); err != nil {
		t.Fatal(err)
	}
	if err := p.FinishRun(runContext, current.ID, "failed", "LATE", nil); pluginErrorCode(err) != "PLUGIN_RUN_TERMINAL" {
		t.Fatal("terminal run was overwritten", err)
	}
	if err := p.releaseSupervisedRun(winningClaim); err != nil {
		t.Fatal(err)
	}
	items, err := p.Runs(ctx, "one", 50)
	public, _ := json.Marshal(items)
	if err != nil || len(items) != 2 || bytes.Contains(public, []byte("synthetic-worker-result")) {
		t.Fatal("run list failed or disclosed worker result", string(public), err)
	}
}

func TestPluginRunsGenerationSurvivesResetAndReinstall(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	installStoredPlugin(t, p, "one")
	_, old, err := p.SaveConfigAndQueue(ctx, "one", 0, map[string]bool{"enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Reset(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	_, fresh, err := p.SaveConfigAndQueue(ctx, "one", 0, map[string]bool{"enabled": true})
	if err != nil || fresh.ConfigRevision != old.ConfigRevision || fresh.Generation <= old.Generation {
		t.Fatal("reset permits an old revision ABA", fresh, old, err)
	}
	claimed, err := p.ClaimRun(ctx)
	if err != nil || claimed.ID != fresh.ID {
		t.Fatal("old generation was executed after reset", claimed, err)
	}
	if err := p.Uninstall(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	installation := storedPluginFixture("one")
	installation.DesiredEnabled = true
	if err := p.Install(ctx, installation); err != nil {
		t.Fatal(err)
	}
	newInstall, _ := p.Installation(ctx, "one")
	if newInstall.Generation <= fresh.Generation {
		t.Fatal("reinstall reused the previous execution generation")
	}
}

func TestPluginRunsInternalPatchPreservesQueuedEventAndClearsOneShot(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	installStoredPlugin(t, p, "one")
	_, first, err := p.SaveConfigAndQueue(ctx, "one", 0, map[string]any{"onlyonce": true, "value": "synthetic-private-config"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := p.ClaimRun(ctx)
	if err != nil || active.ID != first.ID {
		t.Fatal("initial invocation missing", err)
	}
	event, err := p.EnqueueRun(ctx, "one", "event", json.RawMessage(`{"event_id":"fixture-event"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.patchRunConfig(ctx, &active, map[string]json.RawMessage{"onlyonce": json.RawMessage(`false`)}); err != nil {
		t.Fatal(err)
	}
	if err := finishClaimedFixtureRun(ctx, p, active, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	claimed, err := p.ClaimRun(ctx)
	var invocation struct {
		Config struct {
			OnlyOnce bool   `json:"onlyonce"`
			Value    string `json:"value"`
		} `json:"config"`
		Input struct {
			EventID string `json:"event_id"`
		} `json:"input"`
	}
	if err != nil || claimed.ID != event.ID || claimed.ConfigRevision != active.ConfigRevision || json.Unmarshal(claimed.Input, &invocation) != nil || invocation.Config.OnlyOnce || invocation.Config.Value != "synthetic-private-config" || invocation.Input.EventID != "fixture-event" {
		t.Fatal("internal patch canceled event, replayed one-shot or lost payload", claimed.ID, err)
	}
	if err := finishClaimedFixtureRun(ctx, p, claimed, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	stale, err := p.EnqueueRun(ctx, "one", "event", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, fresh, err := p.SaveConfigAndQueue(ctx, "one", active.ConfigRevision, map[string]any{"onlyonce": false, "value": "user-edit"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = p.ClaimRun(ctx)
	old, _ := p.Run(ctx, stale.ID)
	if err != nil || claimed.ID != fresh.ID || old.State != "canceled" {
		t.Fatal("user edit no longer invalidates old work", claimed.ID, old.State, err)
	}
}

func TestPluginRunsRestartRecoversOnlyKnownIdempotentOperations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPluginStore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"CategoryEditor", "SystemProcess", "SyntheticPlugin"} {
		in := storedPluginFixture(id)
		in.PluginID = id
		if err := p.Install(ctx, in); err != nil {
			t.Fatal(err)
		}
		if _, _, err := p.SaveConfigAndQueue(ctx, id, 0, map[string]bool{"enabled": true}); err != nil {
			t.Fatal(err)
		}
		if _, err := p.ClaimRun(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err = NewPluginStore(ctx, s)
	if err != nil || p.recoverPluginRuns(ctx) != nil {
		t.Fatal("restart failed", err)
	}
	// These are synthetic v1.2.3 manifests borrowing compiled names, not
	// authenticated completed runs or certified no-effects retry attempts.
	for _, id := range []string{"CategoryEditor", "SystemProcess", "SyntheticPlugin"} {
		runs, err := p.Runs(ctx, id, 50)
		if err != nil || len(runs) != 1 || runs[0].State != "reconcile_pending" || runs[0].ErrorCode != "PLUGIN_ATTEMPT_UNRESOLVED" {
			t.Fatal("unproven attempt borrowed compiled name to replay", id, runs, err)
		}
	}
	if _, err := p.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unproven restart work became executable", err)
	}
}

func TestPluginStoreSupportsActualLargeMigratingHistory(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	installStoredPlugin(t, p, "BrushFlowLanduo")
	// Live read-only inventory found one 7.16MB history value. The old 1MiB
	// arbitrary limit would have made a complete migration impossible.
	history := strings.Repeat("synthetic-history-", 450000)
	value, err := p.SaveData(ctx, "BrushFlowLanduo", "history", 0, []string{history})
	if err != nil || len(value.Value) < 7<<20 {
		t.Fatal("actual-sized history cannot be stored", len(value.Value), err)
	}
	loaded, err := p.Data(ctx, "BrushFlowLanduo", "history")
	if err != nil || !bytes.Equal(value.Value, loaded.Value) {
		t.Fatal("large encrypted value cannot be read", err)
	}
	for _, suffix := range []string{"", "-wal"} {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(s.lock.Name()), "light-media.db"+suffix))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("synthetic-history-")) {
			t.Fatal("large plugin history was stored in plaintext")
		}
	}
}
