package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"light-media/internal/plugins/uninstall"
)

func TestPluginAutoBackupOwnerRootGenesisRequiresRealHostAndCommitsAtomically(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"AutoBackup"}`, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	run := autoTestClaim(t, a)
	// The actual install transaction has already committed an authenticated empty
	// state+root. A read cannot later decide that missing history means genesis.
	state, blob, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, run.InstanceID, run.Generation)
	if err != nil || blob == nil || len(state.Runs) != 0 {
		t.Fatal("install did not atomically create empty owner", err)
	}
	root, _, err := a.pluginStore.autoBackupRootRead(ctx, a.Store.DB, run.InstanceID, run.Generation)
	if err != nil || root.Event != "install" || root.ParentGeneration != 0 || root.Sequence != 0 {
		t.Fatal("invalid first-use genesis", err)
	}
	called := false
	a.pluginAutoBackup.beforeOwnerAdmission = func(actual context.Context, active PluginRun) {
		called = true
		if _, _, err := a.pluginStore.autoBackupRead(actual, a.Store.DB, active.InstanceID, active.Generation); pluginErrorCode(err) != "AUTO_BACKUP_EVIDENCE_MISSING" {
			t.Fatal("ordinary reader inferred permission from started deferred", err)
		}
		method, _, _ := autoBackupDeferredSpec(active)
		if _, err := a.autoBackupAuthorizedAdmission(context.Background(), active, method); pluginErrorCode(err) != "PLUGIN_DEFERRED_REQUIRED" {
			t.Fatal("matching worker input forged actual Host admission", err)
		}
	}
	autoTestSuccess(t, a, run)
	if !called {
		t.Fatal("actual SDK never entered private Host admission")
	}
	a.pluginAutoBackup.beforeOwnerAdmission = nil
	if _, _, err = a.pluginStore.autoBackupRead(ctx, a.Store.DB, run.InstanceID, run.Generation); err != nil {
		t.Fatal("legitimate Host genesis/configuration rejected", err)
	}
	// A root-write failure cannot publish even the installation/model/first run.
	b := newTestApp(t)
	useBuiltInPluginProcess(t, b)
	secondCookie := setupTestAdmin(t, b)
	if _, err = b.Store.DB.Exec(`CREATE TRIGGER abort_owned_root_genesis BEFORE INSERT ON plugin_auto_backup_roots BEGIN SELECT RAISE(ABORT,'owned root genesis failure'); END`); err != nil {
		t.Fatal(err)
	}
	denied := callTest(t, b.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"AutoBackup"}`, secondCookie, "")
	var count int
	if denied.Code == 200 {
		t.Fatal("failed root genesis returned install success")
	}
	if err = b.Store.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_installations WHERE instance_id='AutoBackup')+(SELECT COUNT(*) FROM plugin_auto_backup_state)+(SELECT COUNT(*) FROM plugin_auto_backup_roots)+(SELECT COUNT(*) FROM plugin_runs WHERE instance_id='AutoBackup')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial genesis escaped SQL rollback", count, err)
	}
}

func TestPluginAutoBackupOwnerRootRejectsMissingReplacedAndRolledBackEvidence(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		for _, mutation := range []string{"delete_state", "delete_root", "delete_both", "foreign_state", "foreign_root", "rollback_state", "rollback_root", "rollback_pair", "rollback_pair_delete_deferred", "delete_deferred"} {
			kind := "confirmed"
			if unknown {
				kind = "unknown"
			}
			t.Run(kind+"/"+mutation, func(t *testing.T) {
				ctx := context.Background()
				a := newTestApp(t)
				cookie := autoTestInstall(t, a)
				var beforeState, beforeRoot []byte
				if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_auto_backup_state WHERE instance_id='AutoBackup' AND generation=1`).Scan(&beforeState); err != nil {
					t.Fatal(err)
				}
				if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_auto_backup_roots WHERE instance_id='AutoBackup' AND generation=1`).Scan(&beforeRoot); err != nil {
					t.Fatal(err)
				}
				if unknown {
					a.pluginAutoBackup.afterEffect = func(kind string) error {
						if kind == "archive_write" {
							return errors.New("owned root fixture lost acknowledgment")
						}
						return nil
					}
				}
				if _, err := a.pluginStore.EnqueueRun(ctx, "AutoBackup", "run", nil); err != nil {
					t.Fatal(err)
				}
				run := autoTestExecute(t, a, autoTestClaim(t, a))
				a.pluginAutoBackup.afterEffect = nil
				if unknown && run.State != "reconcile_pending" || !unknown && run.State != "succeeded" {
					t.Fatal("wrong fixture state", run.State)
				}
				var err error
				switch mutation {
				case "delete_state":
					_, err = a.Store.DB.Exec(`DELETE FROM plugin_auto_backup_state WHERE instance_id='AutoBackup' AND generation=1`)
				case "delete_root":
					_, err = a.Store.DB.Exec(`DELETE FROM plugin_auto_backup_roots WHERE instance_id='AutoBackup' AND generation=1`)
				case "delete_both":
					_, err = a.Store.DB.Exec(`DELETE FROM plugin_auto_backup_state WHERE instance_id='AutoBackup' AND generation=1;DELETE FROM plugin_auto_backup_roots WHERE instance_id='AutoBackup' AND generation=1`)
				case "foreign_state":
					foreign, e := a.pluginStore.seal("run_result", "AutoBackup.owned.other", autoBackupKey(1), json.RawMessage(`{"Version":1,"Instance":"AutoBackup.owned.other","Generation":1,"Runs":{}}`))
					if e != nil {
						t.Fatal(e)
					}
					_, err = a.Store.DB.Exec(`UPDATE plugin_auto_backup_state SET sealed=? WHERE instance_id='AutoBackup' AND generation=1`, foreign)
				case "foreign_root":
					foreign, e := a.pluginStore.seal("run_result", "AutoBackup.owned.other", autoBackupRootKey(1), json.RawMessage(`{"Version":1}`))
					if e != nil {
						t.Fatal(e)
					}
					_, err = a.Store.DB.Exec(`UPDATE plugin_auto_backup_roots SET sealed=? WHERE instance_id='AutoBackup' AND generation=1`, foreign)
				case "rollback_state":
					_, err = a.Store.DB.Exec(`UPDATE plugin_auto_backup_state SET sealed=? WHERE instance_id='AutoBackup' AND generation=1`, beforeState)
				case "rollback_root":
					_, err = a.Store.DB.Exec(`UPDATE plugin_auto_backup_roots SET sealed=? WHERE instance_id='AutoBackup' AND generation=1`, beforeRoot)
				case "rollback_pair":
					if _, err = a.Store.DB.Exec(`UPDATE plugin_auto_backup_state SET sealed=? WHERE instance_id='AutoBackup' AND generation=1`, beforeState); err == nil {
						_, err = a.Store.DB.Exec(`UPDATE plugin_auto_backup_roots SET sealed=? WHERE instance_id='AutoBackup' AND generation=1`, beforeRoot)
					}
				case "rollback_pair_delete_deferred":
					if _, err = a.Store.DB.Exec(`UPDATE plugin_auto_backup_state SET sealed=? WHERE instance_id='AutoBackup' AND generation=1`, beforeState); err == nil {
						_, err = a.Store.DB.Exec(`UPDATE plugin_auto_backup_roots SET sealed=? WHERE instance_id='AutoBackup' AND generation=1`, beforeRoot)
					}
					if err == nil {
						_, err = a.Store.DB.Exec(`DELETE FROM plugin_deferred_operations WHERE run_id=?`, run.ID)
					}
				case "delete_deferred":
					_, err = a.Store.DB.Exec(`DELETE FROM plugin_deferred_operations WHERE run_id=?`, run.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = a.pluginStore.autoBackupRead(ctx, a.Store.DB, run.InstanceID, run.Generation); pluginErrorCode(err) != "AUTO_BACKUP_EVIDENCE_MISSING" {
					t.Fatal("missing/replaced evidence was silently trusted", err)
				}
				w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/reset", "", cookie, "")
				if w.Code == 200 || !strings.Contains(w.Body.String(), "AUTO_BACKUP_EVIDENCE_MISSING") {
					t.Fatal("lifecycle bypassed missing authentication root", w.Code)
				}
				latest, _ := a.pluginStore.Installation(ctx, run.InstanceID)
				if latest.Generation != run.Generation || len(autoTestFiles(t, a)) != 1 {
					t.Fatal("new generation or effect after evidence loss")
				}
				if unknown && mutation != "delete_deferred" && mutation != "rollback_pair_delete_deferred" {
					dir := a.Dir
					if err = a.Close(); err != nil {
						t.Fatal(err)
					}
					b, e := New(Options{DataDir: dir})
					if e != nil {
						t.Fatal("evidence loss disabled entire App", e)
					}
					defer b.Close()
					old, e := b.pluginStore.Run(ctx, run.ID)
					if e != nil || old.State != "reconcile_pending" || old.ErrorCode != "AUTO_BACKUP_EVIDENCE_MISSING" {
						t.Fatal("lost evidence misreported as recovered success", old.State, e)
					}
					in, _ := b.pluginStore.Installation(ctx, run.InstanceID)
					diag, e := b.pluginStore.autoBackupDiagnostic(ctx, in, old)
					if e != nil || diag == nil || diag.AbandonAllowed || !strings.Contains(diag.Message, "执行证据缺失") {
						t.Fatal("missing evidence lacks safe visible diagnosis", e)
					}
				}
			})
		}
	}
}

func TestPluginAutoBackupOwnerRootLifecycleChainAndUnknownCannotAdvance(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	cookie := autoTestInstall(t, a)
	autoTestRun(t, a)
	oldRoot, _, err := a.pluginStore.autoBackupRootRead(ctx, a.Store.DB, "AutoBackup", 1)
	if err != nil {
		t.Fatal(err)
	}
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/reset", "", cookie, ""); w.Code != 200 {
		t.Fatal("known reset", w.Code)
	}
	root, _, err := a.pluginStore.autoBackupRootRead(ctx, a.Store.DB, "AutoBackup", 2)
	if err != nil || root.ParentSHA != tokenHash(string(mustCleanSeedJSON(oldRoot))) || root.ParentGeneration != 1 || root.Event != "reset" {
		t.Fatal("reset broke authenticated root chain", err)
	}
	w := callTest(t, a.Handler(), "PUT", "/api/plugins/AutoBackup/config", `{"revision":0,"config":{"enabled":false}}`, cookie, "")
	if w.Code != 200 {
		t.Fatal("new legitimate generation config", w.Code)
	}
	autoTestSuccess(t, a, autoTestClaim(t, a))
	if w = callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/uninstall", "", cookie, ""); w.Code != 200 {
		t.Fatal("known uninstall", w.Code)
	}
	if w = callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"AutoBackup"}`, cookie, ""); w.Code != 200 {
		t.Fatal("legitimate reinstall", w.Code)
	}
	autoTestSuccess(t, a, autoTestClaim(t, a))
	current, _ := a.pluginStore.Installation(ctx, "AutoBackup")
	if current.Generation != 4 {
		t.Fatal("unexpected lifecycle generation", current.Generation)
	}
	if _, _, err = a.pluginStore.autoBackupRead(ctx, a.Store.DB, "AutoBackup", 4); err != nil {
		t.Fatal("new generation cannot validate ancestry", err)
	}
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"AutoBackup"}, "clear_config": true, "clear_data": true}, true)
	if got := autoTestExecute(t, a, run); got.State != "succeeded" {
		t.Fatal("real bulk uninstall", got.ErrorCode)
	}
	root, _, err = a.pluginStore.autoBackupRootRead(ctx, a.Store.DB, "AutoBackup", 5)
	if err != nil || root.Event != "bulk_uninstall" || root.ParentGeneration != 4 {
		t.Fatal("bulk uninstall bypassed owner root transition", err)
	}
	if _, _, err = a.pluginStore.autoBackupRead(ctx, a.Store.DB, "AutoBackup", 5); err != nil {
		t.Fatal("post bulk chain invalid", err)
	}
	// Prior-state deletion remains visible through the new-generation chain.
	if _, err = a.Store.DB.Exec(`DELETE FROM plugin_auto_backup_state WHERE instance_id='AutoBackup' AND generation=1`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.pluginStore.autoBackupRead(ctx, a.Store.DB, "AutoBackup", 5); pluginErrorCode(err) != "AUTO_BACKUP_EVIDENCE_MISSING" {
		t.Fatal("ancestry silently discarded", err)
	}
	for _, operation := range []string{"reset", "uninstall", "bulk_uninstall"} {
		t.Run("unknown_"+operation, func(t *testing.T) {
			b := newTestApp(t)
			c := autoTestInstall(t, b)
			b.pluginAutoBackup.afterEffect = func(kind string) error {
				if kind == "archive_write" {
					return errors.New("owned uncertain prior effect")
				}
				return nil
			}
			if _, err := b.pluginStore.EnqueueRun(ctx, "AutoBackup", "run", nil); err != nil {
				t.Fatal(err)
			}
			lost := autoTestExecute(t, b, autoTestClaim(t, b))
			if lost.State != "reconcile_pending" {
				t.Fatal("fixture")
			}
			var rootBefore []byte
			b.Store.DB.QueryRow(`SELECT sealed FROM plugin_auto_backup_roots WHERE instance_id='AutoBackup' AND generation=1`).Scan(&rootBefore)
			if operation == "bulk_uninstall" {
				other := uninstallInstall(t, b, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"AutoBackup"}, "clear_config": true, "clear_data": true}, true)
				if got := autoTestExecute(t, b, other); got.State == "succeeded" {
					t.Fatal("bulk replaced unknown generation")
				}
			} else if w := callTest(t, b.Handler(), "POST", "/api/plugins/AutoBackup/"+operation, "", c, ""); w.Code == 200 {
				t.Fatal("unknown generation advanced", operation)
			}
			in, _ := b.pluginStore.Installation(ctx, "AutoBackup")
			var rootAfter []byte
			b.Store.DB.QueryRow(`SELECT sealed FROM plugin_auto_backup_roots WHERE instance_id='AutoBackup' AND generation=1`).Scan(&rootAfter)
			if in.Generation != 1 || !in.Installed || !bytes.Equal(rootBefore, rootAfter) || len(autoTestFiles(t, b)) != 1 {
				t.Fatal("unknown proof changed on rejected lifecycle")
			}
		})
	}
}

func TestPluginAutoBackupLegacyOwnerLayoutWithoutRootIsNotSilentlyUpgraded(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	cookie := autoTestInstall(t, a)
	known := autoTestRun(t, a)
	var stateBefore, deferredBefore []byte
	if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_auto_backup_state WHERE instance_id=? AND generation=?`, known.InstanceID, known.Generation).Scan(&stateBefore); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_deferred_operations WHERE run_id=?`, known.ID).Scan(&deferredBefore); err != nil {
		t.Fatal(err)
	}
	// This represents the prior source layout: authenticated v1 journal and
	// historical deferred confirmation exist, but the root table never existed.
	if _, err := a.Store.DB.Exec(`DROP TABLE plugin_auto_backup_roots`); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = b.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := b.pluginStore.Run(ctx, known.ID)
	if current.State != "succeeded" || !bytes.Equal(current.Input, known.Input) {
		t.Fatal("known historical confirmation discarded")
	}
	if _, _, err = b.pluginStore.autoBackupRead(ctx, b.Store.DB, known.InstanceID, known.Generation); pluginErrorCode(err) != "AUTO_BACKUP_EVIDENCE_MISSING" {
		t.Fatal("old layout acquired unproved trust", err)
	}
	if w := callTest(t, b.Handler(), "POST", "/api/plugins/AutoBackup/reload", "", cookie, ""); w.Code == 200 {
		t.Fatal("old source layout silently reinitialized")
	}
	var count int
	if err = b.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_auto_backup_roots`).Scan(&count); err != nil || count != 0 {
		t.Fatal("read/startup created a trust root", count, err)
	}
	var stateAfter, deferredAfter []byte
	b.Store.DB.QueryRow(`SELECT sealed FROM plugin_auto_backup_state WHERE instance_id=? AND generation=?`, known.InstanceID, known.Generation).Scan(&stateAfter)
	b.Store.DB.QueryRow(`SELECT sealed FROM plugin_deferred_operations WHERE run_id=?`, known.ID).Scan(&deferredAfter)
	if !bytes.Equal(stateBefore, stateAfter) || !bytes.Equal(deferredBefore, deferredAfter) || len(autoTestFiles(t, b)) != 1 {
		t.Fatal("old evidence or effects changed")
	}
}

func TestPluginAutoBackupOwnerRootAndJournalUpdateRollbackTogether(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	autoTestInstall(t, a)
	in, _ := a.pluginStore.Installation(ctx, "AutoBackup")
	owner, err := a.autoBackupOwner(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	var stateBefore, rootBefore []byte
	a.Store.DB.QueryRow(`SELECT sealed FROM plugin_auto_backup_state WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&stateBefore)
	a.Store.DB.QueryRow(`SELECT sealed FROM plugin_auto_backup_roots WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&rootBefore)
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER ignore_owned_root_update BEFORE UPDATE ON plugin_auto_backup_roots BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	if err = owner.lock(ctx); err != nil {
		t.Fatal(err)
	}
	err = owner.update(ctx, func(s *autoBackupState) error { s.AppliedRevision++; return nil })
	owner.unlock()
	if pluginErrorCode(err) != "AUTO_BACKUP_EVIDENCE_MISSING" {
		t.Fatal("ignored root CAS accepted", err)
	}
	var stateAfter, rootAfter []byte
	a.Store.DB.QueryRow(`SELECT sealed FROM plugin_auto_backup_state WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&stateAfter)
	a.Store.DB.QueryRow(`SELECT sealed FROM plugin_auto_backup_roots WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&rootAfter)
	if !bytes.Equal(stateBefore, stateAfter) || !bytes.Equal(rootBefore, rootAfter) {
		t.Fatal("journal/root update was not atomic")
	}
	if _, _, err = a.pluginStore.autoBackupRead(ctx, a.Store.DB, in.InstanceID, in.Generation); err != nil {
		t.Fatal("rolled-back owner no longer readable", err)
	}
}

type autoBackupRootFaultReader struct {
	q        pluginRemoveLinkJournalReader
	cancel   context.CancelFunc
	sqlFault bool
}

func (r autoBackupRootFaultReader) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return r.q.QueryContext(ctx, query, args...)
}
func (r autoBackupRootFaultReader) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if strings.Contains(query, "FROM plugin_deferred_operations WHERE run_id") {
		if r.sqlFault {
			return r.q.QueryRowContext(ctx, `SELECT no_owned_column FROM plugin_deferred_operations`)
		}
		r.cancel()
	}
	return r.q.QueryRowContext(ctx, query, args...)
}
func TestPluginAutoBackupOwnerRootPreservesContextAndSQLFailures(t *testing.T) {
	for _, sqlFault := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "sql"}[sqlFault], func(t *testing.T) {
			a := newTestApp(t)
			autoTestInstall(t, a)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, _, err = a.pluginStore.autoBackupRead(ctx, autoBackupRootFaultReader{q: tx, cancel: cancel, sqlFault: sqlFault}, "AutoBackup", 1)
			if err == nil || pluginErrorCode(err) == "AUTO_BACKUP_EVIDENCE_MISSING" {
				t.Fatal("real SQL/context failure reclassified as missing evidence", err)
			}
			if !sqlFault && !errors.Is(err, context.Canceled) {
				t.Fatal("context cancellation swallowed", err)
			}
		})
	}
}
