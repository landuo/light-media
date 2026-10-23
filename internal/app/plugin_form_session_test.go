package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// Session tests use actual compiled pure-defaults declarations. The mechanical
// full-save helper exercises the shared transaction, not a fabricated public
// Form permission or an integrated business adapter.
func pluginFormSessionFixture(t *testing.T, manifest plugins.Manifest, raw json.RawMessage) (*PluginStore, context.Context, PluginInstallation) {
	t.Helper()
	_, p := pluginStoreFixture(t)
	p.store.DB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	installCloneSource(t, p, manifest)
	if raw != nil {
		if _, err := p.SaveConfig(ctx, manifest.PluginID, 0, raw); err != nil {
			t.Fatal(err)
		}
	}
	in, err := p.Installation(ctx, manifest.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	return p, ctx, in
}

func assertPluginFormSessionValue(t *testing.T, got, want PluginStoredValue) {
	t.Helper()
	if got.Revision != want.Revision || got.UpdatedAt != want.UpdatedAt || !bytes.Equal(got.Value, want.Value) {
		t.Fatalf("session image differs: revision %d/%d, timestamp %q/%q, value %s/%s", got.Revision, want.Revision, got.UpdatedAt, want.UpdatedAt, got.Value, want.Value)
	}
}

func TestPluginFormSessionInitialImagesAndCloneIsolation(t *testing.T) {
	for _, manifest := range []plugins.Manifest{builtin.SystemProcessManifest(), builtin.CategoryEditorManifest()} {
		t.Run(manifest.PluginID, func(t *testing.T) {
			p, ctx, source := pluginFormSessionFixture(t, manifest, json.RawMessage(`{"enable":false,"zero":0,"empty":"","null":null,"opaque":{"kept":true}}`))
			original, originalSealed := pluginDefaultsStoredConfig(t, p, source.InstanceID)
			clone, err := p.Clone(ctx, source.InstanceID, PluginCloneRequest{Suffix: "session"})
			if err != nil {
				t.Fatal(err)
			}
			before, sealed := pluginDefaultsStoredConfig(t, p, clone.InstanceID)
			requested := clone
			requested.Manifest = json.RawMessage(`{"caller":"not-authority"}`)
			requested.DesiredEnabled = true
			session, err := p.beginPluginFormSession(ctx, requested, pluginFormOriginInstalled)
			if err != nil {
				t.Fatal(err)
			}
			if session.resolution.mode != pluginFormInstalledCold || session.resolution.installation.InstanceID != clone.InstanceID || session.resolution.installation.Generation != clone.Generation || session.resolution.installation.DesiredEnabled || !session.configExists {
				t.Fatal("session did not capture authenticated cold clone", session.resolution)
			}
			assertPluginFormSessionValue(t, session.durable, before)
			assertPluginFormSessionValue(t, session.display, before)
			// Display is a detached render image, never the committed preimage.
			session.display.Value[0] = '!'
			assertPluginFormSessionValue(t, session.durable, before)
			assertPluginDefaultsConfigUnchanged(t, p, clone.InstanceID, before, sealed)
			assertPluginDefaultsConfigUnchanged(t, p, source.InstanceID, original, originalSealed)
			if err = session.validateFinal(ctx); err != nil {
				t.Fatal("display edits changed durable validation", err)
			}
		})
	}
}

func TestPluginFormSessionMissingConfigRemainsAbsentOnNoop(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), nil)
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil {
		t.Fatal(err)
	}
	if session.configExists || session.durable.Revision != 0 || string(session.durable.Value) != `{}` || session.display.Revision != 0 || string(session.display.Value) != `{}` {
		t.Fatal("absent config acquired a synthetic stored image")
	}
	value, err := p.NormalizeInstanceDefaults(withPluginFormSession(ctx, session), in, 0, nil)
	if err != nil || value.Revision != 0 || session.configExists {
		t.Fatal("empty normalization wrote absent config", value, err)
	}
	if _, err = p.Config(ctx, in.InstanceID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("no-op materialized missing config", err)
	}
	if err = session.validateFinal(ctx); err != nil {
		t.Fatal(err)
	}
	value, err = p.NormalizeInstanceDefaults(withPluginFormSession(ctx, session), in, 0, map[string]json.RawMessage{"enable": json.RawMessage(`false`)})
	if err != nil || value.Revision != 1 || !session.configExists {
		t.Fatal("first committed config did not advance absence tracking", value, err)
	}
	assertPluginFormSessionValue(t, session.durable, value)
	assertPluginFormSessionValue(t, session.display, value)
	if err = session.validateFinal(ctx); err != nil {
		t.Fatal("first commit invalidated its own session", err)
	}
}

func TestPluginFormSessionCapturesCommittedDefaultsNoopAndEqualSave(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
	before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil {
		t.Fatal(err)
	}
	formCtx := withPluginFormSession(ctx, session)
	displayBefore := PluginStoredValue{Revision: before.Revision, UpdatedAt: before.UpdatedAt, Value: json.RawMessage(`{"display_only":true}`)}
	session.display = displayBefore
	value, err := p.NormalizeInstanceDefaults(formCtx, in, before.Revision, map[string]json.RawMessage{"enable": json.RawMessage(`true`)})
	if err != nil {
		t.Fatal(err)
	}
	assertPluginFormSessionValue(t, value, before)
	assertPluginFormSessionValue(t, session.durable, before)
	assertPluginFormSessionValue(t, session.display, displayBefore)
	assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
	value, err = p.NormalizeInstanceDefaults(formCtx, in, before.Revision, map[string]json.RawMessage{"dashboard_widget_size": json.RawMessage(`8`)})
	if err != nil || value.Revision != before.Revision+1 || !session.configExists {
		t.Fatal("committed defaults were not captured", value, err)
	}
	assertPluginFormSessionValue(t, session.durable, value)
	assertPluginFormSessionValue(t, session.display, value)
	committed, _ := pluginDefaultsStoredConfig(t, p, in.InstanceID)
	value.Value[0] = '!'
	assertPluginFormSessionValue(t, session.durable, committed)
	assertPluginFormSessionValue(t, session.display, committed)
	if err = session.validateFinal(ctx); err != nil {
		t.Fatal("own defaults commit made the session stale", err)
	}
	calls := 0
	equal, err := pluginFormMechanicalSave(t, p, formCtx, in, committed.Revision, committed.Value, func(ctx context.Context, tx PluginDefaultsTransaction, change PluginDefaultsChange) error {
		calls++
		assertPluginFormSessionValue(t, session.durable, committed)
		assertPluginFormSessionValue(t, change.Previous, committed)
		if change.Next.Revision != committed.Revision+1 || !bytes.Equal(change.Next.Value, committed.Value) {
			t.Fatal("equal source Save lost its actual transition")
		}
		return nil
	})
	if err != nil || calls != 1 || equal.Revision != committed.Revision+1 {
		t.Fatal("equal source Save did not commit exactly once", calls, equal, err)
	}
	assertPluginFormSessionValue(t, session.durable, equal)
	assertPluginFormSessionValue(t, session.display, equal)
	if err = session.validateFinal(ctx); err != nil {
		t.Fatal("own equal-save commit made the session stale", err)
	}
}

func TestPluginFormSessionRejectsConfigAndResetDrift(t *testing.T) {
	for _, drift := range []string{"external-save", "same-revision-bytes", "reset"} {
		t.Run(drift, func(t *testing.T) {
			p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
			before, _ := pluginDefaultsStoredConfig(t, p, in.InstanceID)
			session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
			if err != nil {
				t.Fatal(err)
			}
			current, revision := in, before.Revision
			switch drift {
			case "external-save":
				changed, e := p.SaveConfig(ctx, in.InstanceID, revision, json.RawMessage(`{"external":true}`))
				if e != nil {
					t.Fatal(e)
				}
				revision = changed.Revision
			case "same-revision-bytes":
				changed, e := p.seal("config", in.InstanceID, "main", json.RawMessage(`{"external":true}`))
				if e != nil {
					t.Fatal(e)
				}
				if _, e = p.store.DB.ExecContext(ctx, `UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind='config' AND item_key='main'`, changed, in.InstanceID); e != nil {
					t.Fatal(e)
				}
			case "reset":
				if err = p.Reset(ctx, in.InstanceID); err != nil {
					t.Fatal(err)
				}
				current, err = p.Installation(ctx, in.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				revision = 0
			}
			if err = session.validateFinal(ctx); err == nil || (drift != "reset" && pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT") {
				t.Fatal("final validation accepted or misclassified drift", drift, err)
			}
			// Even a caller refreshed to the latest ordinary CAS snapshot cannot
			// reuse this older form session's authority.
			if _, err = p.NormalizeInstanceDefaults(withPluginFormSession(ctx, session), current, revision, map[string]json.RawMessage{"new_default": json.RawMessage(`false`)}); err == nil {
				t.Fatal("old session accepted refreshed CAS inputs", drift)
			}
			assertPluginFormSessionValue(t, session.durable, before)
			if drift == "reset" {
				if _, err = p.Config(ctx, in.InstanceID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("old session recreated reset config", err)
				}
			} else {
				after, e := p.Config(ctx, in.InstanceID)
				if e != nil || after.Revision != revision || string(after.Value) != `{"external":true}` {
					t.Fatal("old session changed the external edit", after, e)
				}
			}
		})
	}
}

func TestPluginFormSessionClaimBeforeNormalizationIsStale(t *testing.T) {
	for _, finish := range []bool{false, true} {
		name := "running"
		if finish {
			name = "finished"
		}
		t.Run(name, func(t *testing.T) {
			p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
			before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
			session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
			if err != nil || session.resolution.mode != pluginFormInstalledCold {
				t.Fatal("fixture was not cold", err)
			}
			queued, err := p.EnqueueRun(ctx, in.InstanceID, "page", nil)
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := p.ClaimRun(ctx)
			if err != nil || claimed.ID != queued.ID {
				t.Fatal(claimed, err)
			}
			if finish {
				if err = finishClaimedFixtureRun(ctx, p, claimed, "succeeded", "", nil); err != nil {
					t.Fatal(err)
				}
			}
			if err = session.validateFinal(ctx); pluginErrorCode(err) != "PLUGIN_SNAPSHOT_STALE" {
				t.Fatal("claim did not invalidate cold form decision", err)
			}
			if _, err = p.NormalizeInstanceDefaults(withPluginFormSession(ctx, session), in, before.Revision, map[string]json.RawMessage{"new_default": json.RawMessage(`false`)}); pluginErrorCode(err) != "PLUGIN_SNAPSHOT_STALE" {
				t.Fatal("old cold form was not rejected before normalization/busy handling", err)
			}
			assertPluginFormSessionValue(t, session.durable, before)
			assertPluginFormSessionValue(t, session.display, before)
			assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
		})
	}
}

func TestPluginFormSessionUnknownModeMayStayUnknown(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
	lifecycleEditManifest(t, p, in.InstanceID, func(fields map[string]json.RawMessage) { delete(fields, pluginLifecycleField) })
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil || session.resolution.mode != pluginFormInstalledUnknown {
		t.Fatal("legacy unknown state was not preserved", err)
	}
	value, err := p.NormalizeInstanceDefaults(withPluginFormSession(ctx, session), in, session.durable.Revision, map[string]json.RawMessage{"new_default": json.RawMessage(`false`)})
	if err != nil {
		t.Fatal("unchanged unknown mode was treated as drift", err)
	}
	assertPluginFormSessionValue(t, session.durable, value)
	if err = session.validateFinal(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycleWantMode(t, p, in, pluginFormInstalledUnknown)
}

func TestPluginFormSessionAdmitUsesSameTransactionReadView(t *testing.T) {
	for _, drift := range []string{"none", "mode", "config"} {
		t.Run(drift, func(t *testing.T) {
			p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
			before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
			session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := p.store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			previous := before
			if drift == "mode" {
				if err = p.writePluginLifecycleTx(ctx, tx, in, pluginLifecycleStarted); err != nil {
					t.Fatal(err)
				}
			}
			if drift == "config" {
				previous, err = p.saveValueTx(ctx, tx, in.InstanceID, "config", "main", before.Revision, json.RawMessage(`{"transaction_local":true}`))
				if err != nil {
					t.Fatal(err)
				}
			}
			// A nested DB reader would block on MaxOpenConns(1). The finite
			// deadline also distinguishes that bug from an ordinary rejection.
			readCtx, cancel := context.WithTimeout(ctx, time.Second)
			err = session.admit(readCtx, tx, in, previous)
			cancel()
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				t.Fatal("admission opened a second read view", err)
			}
			if (drift == "none") != (err == nil) {
				t.Fatal("admission ignored its transaction snapshot", drift, err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			assertPluginFormSessionValue(t, session.durable, before)
			assertPluginFormSessionValue(t, session.display, before)
			assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
			if err = session.validateFinal(ctx); err != nil {
				t.Fatal("rolled-back drift changed the session", err)
			}
		})
	}
}

func TestPluginFormSessionPreviewIsReadOnlyAndPreservesRetainedConfig(t *testing.T) {
	for _, tombstone := range []bool{false, true} {
		name := "absent"
		if tombstone {
			name = "tombstone"
		}
		t.Run(name, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			p.store.DB.SetMaxOpenConns(1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			manifest := builtin.SystemProcessManifest()
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			request := PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw}
			var retained PluginStoredValue
			var retainedSealed []byte
			var inactive PluginInstallation
			if tombstone {
				installCloneSource(t, p, manifest)
				if _, err = p.SaveConfig(ctx, manifest.PluginID, 0, json.RawMessage(`{"private":"retained-only"}`)); err != nil {
					t.Fatal(err)
				}
				if err = p.Uninstall(ctx, manifest.PluginID); err != nil {
					t.Fatal(err)
				}
				retained, retainedSealed = pluginDefaultsStoredConfig(t, p, manifest.PluginID)
				inactive, err = p.Installation(ctx, manifest.PluginID)
				if err != nil {
					t.Fatal(err)
				}
			}
			var before, after int64
			if err = p.store.DB.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			session, err := p.beginPluginFormSession(ctx, request, pluginFormOriginInstall)
			if err != nil {
				t.Fatal(err)
			}
			if session.resolution.mode != pluginFormPreview || session.resolution.installation.Installed || session.resolution.installation.Generation != 0 || session.configExists != tombstone {
				t.Fatal("preview acquired installed authority or lost retained-config existence")
			}
			want := PluginStoredValue{Value: json.RawMessage(`{}`)}
			if tombstone {
				want = retained
			}
			assertPluginFormSessionValue(t, session.durable, want)
			assertPluginFormSessionValue(t, session.display, want)
			if err = session.admit(ctx, p.store.DB, request, want); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
				t.Fatal("preview obtained write admission", err)
			}
			if err = session.validateFinal(ctx); err != nil {
				t.Fatal(err)
			}
			if err = p.store.DB.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil || after != before {
				t.Fatal("preview read wrote state", before, after, err)
			}
			if tombstone {
				assertPluginDefaultsConfigUnchanged(t, p, manifest.PluginID, retained, retainedSealed)
				current, e := p.Installation(ctx, manifest.PluginID)
				if e != nil || current.Installed || current.Generation != inactive.Generation || !bytes.Equal(current.Manifest, inactive.Manifest) {
					t.Fatal("preview changed the retained lifecycle", current, e)
				}
			} else if _, err = p.Config(ctx, manifest.PluginID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("absent preview materialized config", err)
			}
			if err = p.Install(ctx, request); err != nil {
				t.Fatal(err)
			}
			if err = session.validateFinal(ctx); err == nil {
				t.Fatal("preview survived a concurrent installation")
			}
			if _, err = p.beginPluginFormSession(ctx, request, pluginFormOriginInstall); pluginErrorCode(err) != "PLUGIN_ALREADY_INSTALLED" {
				t.Fatal("install origin silently switched to installed behavior", err)
			}
		})
	}
}

func TestPluginFormSessionFailedTransitionDoesNotAdvanceDurable(t *testing.T) {
	for _, stage := range []string{"config-write", "late-propagation", "commit"} {
		t.Run(stage, func(t *testing.T) {
			p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
			before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
			session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
			if err != nil {
				t.Fatal(err)
			}
			journal, err := p.SaveData(ctx, in.InstanceID, "session-journal", 0, map[string]bool{"original": true})
			if err != nil {
				t.Fatal(err)
			}
			switch stage {
			case "config-write":
				_, err = p.store.DB.ExecContext(ctx, `CREATE TRIGGER reject_session_config BEFORE UPDATE ON plugin_values WHEN NEW.kind='config' BEGIN SELECT RAISE(ABORT,'synthetic session config rejection'); END`)
			case "late-propagation":
				_, err = p.store.DB.ExecContext(ctx, `INSERT INTO plugin_event_batches(id,instance_id,handler_key,generation,config_revision,due_at,created_at) VALUES('session-rollback-batch',?,'synthetic',?,?,0,?)`, in.InstanceID, in.Generation, before.Revision, now())
				if err == nil {
					_, err = p.store.DB.ExecContext(ctx, `CREATE TRIGGER reject_session_propagation BEFORE UPDATE ON plugin_event_batches BEGIN SELECT RAISE(ABORT,'synthetic session late rejection'); END`)
				}
			case "commit":
				// Deferred FK failure is introduced by the config-write trigger;
				// both the write and projector succeed, but COMMIT must fail.
				_, err = p.store.DB.ExecContext(ctx, `CREATE TABLE session_commit_parent(id INTEGER PRIMARY KEY); CREATE TABLE session_commit_child(parent_id INTEGER REFERENCES session_commit_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER reject_session_commit AFTER UPDATE ON plugin_values WHEN NEW.kind='config' BEGIN INSERT INTO session_commit_child(parent_id) VALUES(1); END;`)
			}
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			_, err = pluginFormMechanicalSave(t, p, withPluginFormSession(ctx, session), in, before.Revision, json.RawMessage(`{"replacement":true}`), func(ctx context.Context, tx PluginDefaultsTransaction, change PluginDefaultsChange) error {
				calls++
				assertPluginFormSessionValue(t, session.durable, before)
				if stage == "commit" {
					var pending int
					if e := pluginProjectionSQLForTest(t, tx).QueryRowContext(ctx, `SELECT COUNT(*) FROM session_commit_child`).Scan(&pending); e != nil || pending != 1 {
						t.Fatal("deferred commit failure was not armed", pending, e)
					}
				}
				_, e := tx.SaveData(ctx, "session-journal", journal.Revision, map[string]int64{"revision": change.Next.Revision})
				return e
			})
			wantCalls := 1
			if stage == "config-write" {
				wantCalls = 0
			}
			if err == nil || calls != wantCalls {
				t.Fatal("transition did not fail at the expected stage", calls, err)
			}
			assertPluginFormSessionValue(t, session.durable, before)
			assertPluginFormSessionValue(t, session.display, before)
			assertPluginDefaultsConfigUnchanged(t, p, in.InstanceID, before, sealed)
			after, err := p.Data(ctx, in.InstanceID, "session-journal")
			if err != nil {
				t.Fatal(err)
			}
			assertPluginFormSessionValue(t, after, journal)
			if stage == "commit" {
				var count int
				if err = p.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_commit_child`).Scan(&count); err != nil || count != 0 {
					t.Fatal("failed commit left trigger data", count, err)
				}
			}
			if stage == "late-propagation" {
				var revision int64
				if err = p.store.DB.QueryRowContext(ctx, `SELECT config_revision FROM plugin_event_batches WHERE id='session-rollback-batch'`).Scan(&revision); err != nil || revision != before.Revision {
					t.Fatal("late failure did not roll back propagation", revision, err)
				}
			}
			if err = session.validateFinal(ctx); err != nil {
				t.Fatal("rolled-back transition advanced the session", err)
			}
		})
	}
}
