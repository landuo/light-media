package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/builtin"
)

// These are owned synthetic storage/event fixtures, not plugin business-owner
// admission claims. Real actor lifecycle and actual SDK gates remain separate.
func eventAdmissionHost(t *testing.T) *App {
	t.Helper()
	s, p := pluginStoreFixture(t)
	eventAdmissionInstall(t, p, "admission")
	if _, err := p.SaveConfig(context.Background(), "admission", 0, map[string]any{"enabled": true, "test_once": true}); err != nil {
		t.Fatal(err)
	}
	return &App{Store: s, pluginStore: p}
}

func eventAdmissionInstall(t *testing.T, p *PluginStore, instance string) {
	t.Helper()
	in := storedPluginFixture(instance)
	in.DesiredEnabled = true
	if err := p.Install(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func eventAdmissionQueue(t *testing.T, a *App, instance, action string, input json.RawMessage) PluginRun {
	t.Helper()
	queued, err := a.pluginStore.EnqueueRun(context.Background(), instance, action, input)
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.claimRunForInstance(context.Background(), instance)
	if err != nil || run.ID != queued.ID {
		t.Fatal("claim admission", run.ID, queued.ID, err)
	}
	return run
}

func eventAdmissionSubscribe(t *testing.T, p *PluginStore, run PluginRun, handler string) {
	t.Helper()
	if err := p.configureEventSubscription(context.Background(), run, handler, []string{"import.completed"}, 0, true); err != nil {
		t.Fatal("subscribe", handler, err)
	}
}

func eventAdmissionCursor(t *testing.T, p *PluginStore, instance, handler string) int64 {
	t.Helper()
	var cursor int64
	if err := p.store.DB.QueryRow(`SELECT cursor FROM plugin_event_subscriptions WHERE instance_id=? AND handler_key=?`, instance, handler).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	return cursor
}

func eventAdmissionSealed(t *testing.T, p *PluginStore, run PluginRun) []byte {
	t.Helper()
	var sealed []byte
	if err := p.store.DB.QueryRow(`SELECT sealed FROM plugin_event_admissions WHERE run_id=?`, run.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	return sealed
}

func eventAdmissionInput(t *testing.T, p *PluginStore, run PluginRun) []byte {
	t.Helper()
	var sealed []byte
	if err := p.store.DB.QueryRow(`SELECT input FROM plugin_runs WHERE id=?`, run.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestPluginEventAdmissionQueuedBoundaryIgnoresWorkerPayload(t *testing.T) {
	for _, action := range []string{"init", "reload"} {
		for _, payload := range []string{"", `{}`, `{"event_after":0}`, `{"event_after":999999}`, `{"event_after":"forged"}`, `{"other":true}`} {
			t.Run(action+"/"+payload, func(t *testing.T) {
				a := eventAdmissionHost(t)
				p := a.pluginStore
				commitDomainFixture(t, a, domainFixture("import.completed", "before-admission"))
				run := eventAdmissionQueue(t, a, "admission", action, json.RawMessage(payload))
				commitDomainFixture(t, a, domainFixture("import.completed", "after-admission"))
				eventAdmissionSubscribe(t, p, run, "synthetic")
				if got := eventAdmissionCursor(t, p, run.InstanceID, "synthetic"); got != 1 {
					t.Fatal("worker data changed frozen enqueue boundary", got)
				}
				var decoded plugins.Invocation
				if !pluginStrictJSON(run.Input, &decoded) || !bytes.Equal(decoded.Input, json.RawMessage(payload)) {
					t.Fatal("storage marker changed strict worker invocation", string(run.Input))
				}
				if err := finishClaimedFixtureRun(context.Background(), p, run, "succeeded", "", nil); err != nil {
					t.Fatal(err)
				}
				if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil || pluginEventCount(t, a, "plugin_event_deliveries") != 1 {
					t.Fatal("queued gap lost or history replayed", err)
				}
			})
		}
	}
}

func TestPluginEventAdmissionReloadHandlerCursorsAndProgress(t *testing.T) {
	a := eventAdmissionHost(t)
	p := a.pluginStore
	ctx := context.Background()
	first := eventAdmissionQueue(t, a, "admission", "init", nil)
	for _, handler := range []string{"alpha", "beta", "disabled"} {
		eventAdmissionSubscribe(t, p, first, handler)
	}
	if err := finishClaimedFixtureRun(ctx, p, first, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		commitDomainFixture(t, a, domainFixture("import.completed", fmt.Sprintf("before-reload-%d", i)))
	}
	if _, err := a.Store.DB.Exec(`UPDATE plugin_event_subscriptions SET cursor=CASE handler_key WHEN 'alpha' THEN 1 WHEN 'beta' THEN 2 ELSE 0 END,active=CASE handler_key WHEN 'disabled' THEN 0 ELSE 1 END`); err != nil {
		t.Fatal(err)
	}
	run := eventAdmissionQueue(t, a, "admission", "reload", json.RawMessage(`{"event_after":0}`))
	proof, err := p.readEventAdmissionTx(ctx, p.store.DB, run)
	if err != nil || proof == nil || len(proof.PerHandlerReloadCursor) != 2 || proof.HighWater != 3 || proof.PerHandlerReloadCursor["alpha"] != 1 || proof.PerHandlerReloadCursor["beta"] != 2 {
		t.Fatal("reload did not capture distinct active handler boundaries", proof, err)
	}
	commitDomainFixture(t, a, domainFixture("import.completed", "after-reload"))
	// Alpha advanced while the run was queued; registration must not rewind it.
	// Beta was deactivated meanwhile and must use its own trusted admission.
	if _, err = a.Store.DB.Exec(`UPDATE plugin_event_subscriptions SET cursor=4 WHERE handler_key='alpha'; UPDATE plugin_event_subscriptions SET active=0 WHERE handler_key='beta'`); err != nil {
		t.Fatal(err)
	}
	for handler, want := range map[string]int64{"alpha": 4, "beta": 2, "disabled": 3, "new_handler": 3} {
		eventAdmissionSubscribe(t, p, run, handler)
		if got := eventAdmissionCursor(t, p, run.InstanceID, handler); got != want {
			t.Fatal("per-handler reload boundary", handler, got, want)
		}
	}
}

func TestPluginEventAdmissionRejectsForgedCallerAndProof(t *testing.T) {
	for _, mode := range []string{"caller-action", "caller-input", "caller-version", "caller-generation", "caller-revision", "caller-instance", "stored-action", "stored-input", "missing", "tampered", "cross-instance", "cross-run", "config-content", "installed-plugin", "installed-version", "installed-generation", "disabled", "manifest-corrupt"} {
		t.Run(mode, func(t *testing.T) {
			a := eventAdmissionHost(t)
			p := a.pluginStore
			run := eventAdmissionQueue(t, a, "admission", "init", nil)
			var err error
			switch mode {
			case "caller-action":
				run.Action = "reload"
			case "caller-input":
				run.Input = json.RawMessage(`{"action":"init","config":{},"input":{"event_after":0}}`)
			case "caller-version":
				run.Version = "forged"
			case "caller-generation":
				run.Generation++
			case "caller-revision":
				run.ConfigRevision++
			case "caller-instance":
				run.InstanceID = "other"
			case "stored-action":
				_, err = a.Store.DB.Exec(`UPDATE plugin_runs SET action='reload' WHERE id=?`, run.ID)
				run.Action = "reload"
			case "stored-input":
				run.Input = json.RawMessage(`{"action":"init","config":{},"input":{"event_after":0}}`)
				var sealed []byte
				sealed, err = p.sealEventAdmissionRun(run.InstanceID, run.ID, "SyntheticPlugin", run.Version, run.Input)
				if err == nil {
					_, err = a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?`, sealed, run.ID)
				}
			case "missing":
				_, err = a.Store.DB.Exec(`DELETE FROM plugin_event_admissions WHERE run_id=?`, run.ID)
			case "tampered":
				sealed := eventAdmissionSealed(t, p, run)
				sealed[len(sealed)-1] ^= 1
				_, err = a.Store.DB.Exec(`UPDATE plugin_event_admissions SET sealed=? WHERE run_id=?`, sealed, run.ID)
			case "cross-instance", "cross-run":
				otherInstance := run.InstanceID
				if mode == "cross-instance" {
					otherInstance = "other"
					eventAdmissionInstall(t, p, otherInstance)
					if _, err = p.SaveConfig(context.Background(), otherInstance, 0, map[string]bool{"enabled": true}); err != nil {
						t.Fatal(err)
					}
				}
				var other PluginRun
				other, err = p.EnqueueRun(context.Background(), otherInstance, "init", nil)
				if err == nil {
					_, err = a.Store.DB.Exec(`UPDATE plugin_event_admissions SET sealed=? WHERE run_id=?`, eventAdmissionSealed(t, p, other), run.ID)
				}
			case "config-content":
				var sealed []byte
				sealed, err = p.seal("config", run.InstanceID, "main", []byte(`{"enabled":false}`))
				if err == nil {
					_, err = a.Store.DB.Exec(`UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind='config' AND item_key='main'`, sealed, run.InstanceID)
				}
			case "installed-plugin":
				_, err = a.Store.DB.Exec(`UPDATE plugin_installations SET plugin_id='OtherPlugin' WHERE instance_id=?`, run.InstanceID)
			case "installed-version":
				_, err = a.Store.DB.Exec(`UPDATE plugin_installations SET version='other' WHERE instance_id=?`, run.InstanceID)
			case "installed-generation":
				_, err = a.Store.DB.Exec(`UPDATE plugin_installations SET generation=generation+1 WHERE instance_id=?`, run.InstanceID)
			case "disabled":
				_, err = a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=0 WHERE instance_id=?`, run.InstanceID)
			case "manifest-corrupt":
				_, err = a.Store.DB.Exec(`UPDATE plugin_installations SET manifest=x'00' WHERE instance_id=?`, run.InstanceID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = p.configureEventSubscription(context.Background(), run, "forbidden", []string{"import.completed"}, 0, true); err == nil {
				t.Fatal("forged event authority accepted", mode)
			}
			if pluginEventCount(t, a, "plugin_event_subscriptions") != 0 {
				t.Fatal("invalid registration partially committed")
			}
		})
	}
}

func TestPluginEventAdmissionCloneMetadataCannotBeDeleted(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	installCloneSource(t, p, builtin.SystemProcessManifest())
	clone, err := p.Clone(ctx, "SystemProcess", PluginCloneRequest{Suffix: "EventProof"})
	if err != nil {
		t.Fatal(err)
	}
	config, err := p.Config(ctx, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.SaveConfigAndQueue(ctx, clone.InstanceID, config.Revision, map[string]bool{"enable": false}); err != nil {
		t.Fatal(err)
	}
	run, err := p.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.store.DB.Exec(`DELETE FROM plugin_instance_metadata WHERE instance_id=?`, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	if err = p.configureEventSubscription(ctx, run, "synthetic", []string{"import.completed"}, 0, true); pluginErrorCode(err) != "PLUGIN_INSTANCE_METADATA_INVALID" {
		t.Fatal("deleted clone metadata accepted", err)
	}
}

func TestPluginEventAdmissionMarkerCannotDowngradeToLegacy(t *testing.T) {
	for _, mode := range []string{"stripped", "large-marker", "legacy-brush-marker", "legacy-unmarked"} {
		t.Run(mode, func(t *testing.T) {
			a := eventAdmissionHost(t)
			p := a.pluginStore
			run := eventAdmissionQueue(t, a, "admission", "init", nil)
			input := eventAdmissionInput(t, p, run)
			if !bytes.HasPrefix(input, []byte(pluginEventAdmissionRunPrefix)) {
				t.Fatal("new init lacks authenticated admission marker")
			}
			payload := input[len(pluginEventAdmissionRunPrefix):]
			var err error
			switch mode {
			case "stripped":
				input = payload
			case "large-marker":
				input = append([]byte(pluginEventAdmissionBrushRunPrefix), payload...)
			case "legacy-brush-marker":
				input = append([]byte(brushFlowSnapshotPrefix), payload...)
			case "legacy-unmarked":
				// An explicit synthetic pre-upgrade ciphertext, not an admission
				// downgrade performed by production. Legacy remains readable.
				input, err = p.seal("run", run.InstanceID, run.ID, run.Input)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?; DELETE FROM plugin_event_admissions WHERE run_id=?`, input, run.ID, run.ID); err != nil {
				t.Fatal(err)
			}
			commitDomainFixture(t, a, domainFixture("import.completed", "after-queue"))
			err = p.configureEventSubscription(context.Background(), run, "synthetic", []string{"import.completed"}, 0, true)
			if mode == "legacy-unmarked" {
				if err != nil || eventAdmissionCursor(t, p, run.InstanceID, "synthetic") != 1 {
					t.Fatal("historical unmarked boundary behavior changed", err)
				}
			} else if err == nil || pluginEventCount(t, a, "plugin_event_subscriptions") != 0 {
				t.Fatal("format manipulation downgraded missing proof to legacy", err)
			}
		})
	}
}

func TestPluginEventAdmissionPendingDefaultsPreserveImmutableProof(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			s, p := pluginStoreFixture(t)
			a := &App{Store: s, pluginStore: p}
			ctx := context.Background()
			installCloneSource(t, p, builtin.SystemProcessManifest())
			commitDomainFixture(t, a, domainFixture("import.completed", "before-enqueue"))
			_, queued, err := p.SaveConfigAndQueue(ctx, "SystemProcess", 0, map[string]bool{"enable": false})
			if err != nil {
				t.Fatal(err)
			}
			before := eventAdmissionSealed(t, p, queued)
			if legacy {
				sealed, err := p.seal("run", queued.InstanceID, queued.ID, queued.Input)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?; DELETE FROM plugin_event_admissions WHERE run_id=?`, sealed, queued.ID, queued.ID); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := p.Installation(ctx, queued.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			revision := queued.ConfigRevision
			for _, key := range []string{"synthetic_one", "synthetic_two"} {
				value, err := p.NormalizeInstanceDefaults(ctx, snapshot, revision, map[string]json.RawMessage{key: json.RawMessage(`false`)})
				if err != nil {
					t.Fatal(err)
				}
				revision = value.Revision
			}
			if marked := eventAdmissionRunMarked(eventAdmissionInput(t, p, queued)); marked == legacy {
				t.Fatal("pending defaults changed admission storage family", marked)
			}
			if !legacy && !bytes.Equal(before, eventAdmissionSealed(t, p, queued)) {
				t.Fatal("defaults rewrote immutable event admission")
			}
			commitDomainFixture(t, a, domainFixture("import.completed", "after-enqueue"))
			run, err := p.ClaimRun(ctx)
			if err != nil || run.ConfigRevision != revision || bytes.Equal(run.Input, queued.Input) {
				t.Fatal("pending snapshot did not advance", err)
			}
			eventAdmissionSubscribe(t, p, run, "synthetic")
			want := int64(1)
			if legacy {
				want = 2
			}
			if got := eventAdmissionCursor(t, p, run.InstanceID, "synthetic"); got != want {
				t.Fatal("pending defaults lost boundary", got, want)
			}
			var count int
			if err = s.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_config_transitions WHERE run_id=?`, run.ID).Scan(&count); err != nil || count != 2 {
				t.Fatal("pending transitions missing", count, err)
			}
		})
	}
}

func TestPluginEventAdmissionActiveConfigChainAndSuccessorRejection(t *testing.T) {
	for _, mode := range []string{"valid", "missing-edge", "tampered-edge", "config-replay", "head-replay"} {
		t.Run(mode, func(t *testing.T) {
			a := eventAdmissionHost(t)
			p := a.pluginStore
			ctx := context.Background()
			commitDomainFixture(t, a, domainFixture("import.completed", "before-enqueue"))
			run := eventAdmissionQueue(t, a, "admission", "init", nil)
			oldRun := run
			admission := eventAdmissionSealed(t, p, run)
			var oldConfig []byte
			if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, run.InstanceID).Scan(&oldConfig); err != nil {
				t.Fatal(err)
			}
			for _, patch := range []map[string]json.RawMessage{{"test_once": json.RawMessage(`false`)}, {"synthetic_default": json.RawMessage(`8`)}} {
				if err := p.patchRunConfig(ctx, &run, patch); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(admission, eventAdmissionSealed(t, p, run)) || !bytes.Equal(oldRun.Input, run.Input) {
				t.Fatal("active normalization rewrote admission or input")
			}
			var err error
			switch mode {
			case "missing-edge":
				_, err = a.Store.DB.Exec(`DELETE FROM plugin_run_config_transitions WHERE run_id=? AND from_revision=?`, run.ID, oldRun.ConfigRevision)
			case "tampered-edge":
				_, err = a.Store.DB.Exec(`UPDATE plugin_run_config_transitions SET sealed=x'00' WHERE run_id=?`, run.ID)
			case "config-replay":
				_, err = a.Store.DB.Exec(`UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind='config' AND item_key='main'`, oldConfig, run.InstanceID)
			case "head-replay":
				_, err = a.Store.DB.Exec(`UPDATE plugin_values SET sealed=?,revision=? WHERE instance_id=? AND kind='config' AND item_key='main'; UPDATE plugin_runs SET config_revision=? WHERE id=?`, oldConfig, oldRun.ConfigRevision, run.InstanceID, oldRun.ConfigRevision, run.ID)
				run = oldRun
			}
			if err != nil {
				t.Fatal(err)
			}
			commitDomainFixture(t, a, domainFixture("import.completed", "after-enqueue"))
			err = p.configureEventSubscription(ctx, run, "synthetic", []string{"import.completed"}, 0, true)
			if mode == "valid" {
				if err != nil || eventAdmissionCursor(t, p, run.InstanceID, "synthetic") != 1 {
					t.Fatal("valid active revision chain lost admission", err)
				}
			} else if err == nil || pluginEventCount(t, a, "plugin_event_subscriptions") != 0 {
				t.Fatal("invalid config chain registered event authority", err)
			}
		})
	}
}

func TestPluginEventAdmissionInsertFailureRollsBack(t *testing.T) {
	for _, operation := range []string{"enqueue", "save", "install"} {
		t.Run(operation, func(t *testing.T) {
			a := eventAdmissionHost(t)
			p := a.pluginStore
			ctx := context.Background()
			original, err := p.Config(ctx, "admission")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`CREATE TRIGGER synthetic_admission_fail BEFORE INSERT ON plugin_event_admissions BEGIN SELECT RAISE(ABORT,'synthetic event admission failure'); END`); err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "enqueue":
				_, err = p.EnqueueRun(ctx, "admission", "init", nil)
			case "save":
				_, _, err = p.SaveConfigAndQueue(ctx, "admission", original.Revision, map[string]bool{"enabled": false})
			case "install":
				_, err = p.InstallAndQueue(ctx, storedPluginFixture("atomic-install"), map[string]bool{"enabled": true})
			}
			if err == nil {
				t.Fatal("failed admission committed")
			}
			for _, table := range []string{"plugin_runs", "plugin_event_admissions", "plugin_run_origins"} {
				if pluginEventCount(t, a, table) != 0 {
					t.Fatal("partial admission persisted", table)
				}
			}
			current, err := p.Config(ctx, "admission")
			if err != nil || current.Revision != original.Revision || !bytes.Equal(current.Value, original.Value) {
				t.Fatal("failed save changed config", err)
			}
			var installed int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_installations WHERE instance_id='atomic-install'`).Scan(&installed); err != nil || installed != 0 {
				t.Fatal("failed install persisted", installed, err)
			}
		})
	}
}

func TestPluginEventAdmissionColdReopenRetainsQueuedBoundary(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	p, err := NewPluginStore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Store: s, pluginStore: p}
	installStoredPlugin(t, p, "admission")
	commitDomainFixture(t, a, domainFixture("import.completed", "before-enqueue"))
	_, queued, err := p.SaveConfigAndQueue(ctx, "admission", 0, map[string]bool{"enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	original := eventAdmissionSealed(t, p, queued)
	commitDomainFixture(t, a, domainFixture("import.completed", "queued-gap"))
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err = NewPluginStore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	a = &App{Store: s, pluginStore: p}
	run, err := p.ClaimRun(ctx)
	if err != nil || run.ID != queued.ID || !bytes.Equal(original, eventAdmissionSealed(t, p, run)) {
		t.Fatal("cold admission changed", err)
	}
	eventAdmissionSubscribe(t, p, run, "synthetic")
	if got := eventAdmissionCursor(t, p, run.InstanceID, "synthetic"); got != 1 {
		t.Fatal("cold queue gap lost", got)
	}
	if err = finishClaimedFixtureRun(ctx, p, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	if err = a.dispatchPluginEvents(ctx, time.Now()); err != nil || pluginEventCount(t, a, "plugin_event_deliveries") != 1 {
		t.Fatal("cold delivery lost or replayed", err)
	}
}

func TestPluginEventAdmissionBrushFlowMarkersAndOrdinaryLimits(t *testing.T) {
	_, p := pluginStoreFixture(t)
	for _, large := range []bool{false, true} {
		t.Run(fmt.Sprintf("large=%v", large), func(t *testing.T) {
			config := json.RawMessage(`{}`)
			if large {
				config = json.RawMessage(`{"synthetic":"` + strings.Repeat("x", pluginValueLimit) + `"}`)
			}
			raw, err := json.Marshal(plugins.Invocation{Action: "init", Config: config})
			if err != nil {
				t.Fatal(err)
			}
			sealed, err := p.sealEventAdmissionRun("synthetic-brush", "run", bf.PluginID, bf.Version, raw)
			if err != nil {
				t.Fatal(err)
			}
			prefix := pluginEventAdmissionRunPrefix
			other := pluginEventAdmissionBrushRunPrefix
			if large {
				prefix, other = other, prefix
			}
			if !bytes.HasPrefix(sealed, []byte(prefix)) {
				t.Fatal("wrong admitted snapshot format")
			}
			decoded, err := p.unseal("run", "synthetic-brush", "run", sealed)
			var invocation plugins.Invocation
			if err != nil || !bytes.Equal(decoded, raw) || !pluginStrictJSON(decoded, &invocation) {
				t.Fatal("strict original invocation changed", err)
			}
			for _, attack := range [][]byte{sealed[len(prefix):], append([]byte(other), sealed[len(prefix):]...), append([]byte(brushFlowSnapshotPrefix), sealed[len(prefix):]...)} {
				if _, err = p.unseal("run", "synthetic-brush", "run", attack); err == nil {
					t.Fatal("stripping/swapping snapshot marker accepted")
				}
			}
			legacy, err := p.sealPluginSnapshot("run", "synthetic-brush", "run", bf.PluginID, bf.Version, raw)
			if err != nil {
				t.Fatal(err)
			}
			legacyPayload := legacy
			if large {
				legacyPayload = legacy[len(brushFlowSnapshotPrefix):]
			}
			if _, err = p.unseal("run", "synthetic-brush", "run", append([]byte(prefix), legacyPayload...)); err == nil {
				t.Fatal("legacy input promoted by marker alone")
			}
			for _, previous := range [][]byte{sealed, legacy} {
				resealed, err := p.resealRunSnapshot("synthetic-brush", "run", bf.PluginID, bf.Version, previous, raw)
				if err != nil || eventAdmissionRunMarked(resealed) != eventAdmissionRunMarked(previous) {
					t.Fatal("pending reseal changed admission family", err)
				}
				decoded, err = p.unseal("run", "synthetic-brush", "run", resealed)
				if err != nil || !bytes.Equal(decoded, raw) {
					t.Fatal("pending reseal lost original bytes", err)
				}
			}
			if large {
				for _, owner := range [][2]string{{"SyntheticPlugin", bf.Version}, {bf.PluginID, "other"}} {
					if _, err = p.sealEventAdmissionRun("ordinary", "run", owner[0], owner[1], raw); err == nil {
						t.Fatal("ordinary snapshot limit relaxed", owner)
					}
				}
				tooLarge, err := p.seal(pluginEventAdmissionRunPurpose, "ordinary", "run", raw)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = p.unseal("run", "ordinary", "run", append([]byte(pluginEventAdmissionRunPrefix), tooLarge...)); err == nil {
					t.Fatal("ordinary marked reader exceeded size bound")
				}
			}
		})
	}
}

func TestPluginEventAdmissionNonInitRunsRemainLegacy(t *testing.T) {
	a := eventAdmissionHost(t)
	p := a.pluginStore
	run := eventAdmissionQueue(t, a, "admission", "manual", nil)
	if eventAdmissionRunMarked(eventAdmissionInput(t, p, run)) || pluginEventCount(t, a, "plugin_event_admissions") != 0 {
		t.Fatal("unrelated run silently upgraded")
	}
	var proof *pluginEventAdmission
	var err error
	proof, err = p.readEventAdmissionTx(context.Background(), p.store.DB, run)
	if err != nil || proof != nil {
		t.Fatal("legacy run required event admission", proof, err)
	}
}

var _ PluginProofReader = (*sql.DB)(nil)

func TestPluginEventAdmissionPendingLargeBrushFlowPropagation(t *testing.T) {
	// Exercise the shared host transaction primitive, not a claim that the
	// BrushFlow business owner has opted into form/default projection.
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			s, p := pluginStoreFixture(t)
			ctx := context.Background()
			installCloneSource(t, p, bf.NativeManifest())
			config := bf.DefaultConfig()
			config["synthetic_owned"] = strings.Repeat("x", pluginValueLimit)
			stored, queued, err := p.SaveConfigAndQueue(ctx, bf.PluginID, 0, config)
			if err != nil {
				t.Fatal(err)
			}
			admission := eventAdmissionSealed(t, p, queued)
			if legacy {
				sealed, err := p.sealPluginSnapshot("run", queued.InstanceID, queued.ID, bf.PluginID, bf.Version, queued.Input)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?; DELETE FROM plugin_event_admissions WHERE run_id=?`, sealed, queued.ID, queued.ID); err != nil {
					t.Fatal(err)
				}
			}
			prefix := pluginEventAdmissionBrushRunPrefix
			if legacy {
				prefix = brushFlowSnapshotPrefix
			}
			if !bytes.HasPrefix(eventAdmissionInput(t, p, queued), []byte(prefix)) {
				t.Fatal("large initial storage format lost")
			}
			config["synthetic_default"] = false
			next, err := pluginSnapshotJSON(bf.PluginID, bf.Version, config, true)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := s.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			value, err := p.saveValueTx(ctx, tx, queued.InstanceID, "config", "main", stored.Revision, next)
			if err == nil {
				err = p.propagateInstanceConfigTx(ctx, tx, queued.InstanceID, queued.Generation, stored.Revision, value.Revision, stored.Value, next)
			}
			if err != nil {
				t.Fatal("shared host pending propagation", err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(eventAdmissionInput(t, p, queued), []byte(prefix)) {
				t.Fatal("large pending rewrite changed format")
			}
			if !legacy && !bytes.Equal(admission, eventAdmissionSealed(t, p, queued)) {
				t.Fatal("large pending rewrite changed admission")
			}
			current, err := p.Run(ctx, queued.ID)
			if err != nil || current.State != "pending" || current.ConfigRevision != value.Revision || bytes.Equal(current.Input, queued.Input) {
				t.Fatal("large pending snapshot did not advance", err)
			}
			configSHA, err := p.verifyRunConfigChainTx(ctx, s.DB, bf.PluginID, current, queued.ConfigRevision, deferredInputSHA(queued.Input), deferredInputSHA(stored.Value))
			if err != nil || configSHA != deferredInputSHA(next) {
				t.Fatal("large pending chain lost original identity", err)
			}
			var invocation plugins.Invocation
			if !pluginStrictJSON(current.Input, &invocation) || !bytes.Equal(invocation.Config, next) || len(current.Input) <= pluginValueLimit {
				t.Fatal("large pending invocation no longer original strict wire format")
			}
		})
	}
}

func TestPluginEventAdmissionAuthenticLegacyCipherCannotReplaceNewRun(t *testing.T) {
	a := eventAdmissionHost(t)
	p := a.pluginStore
	ctx := context.Background()
	old := eventAdmissionQueue(t, a, "admission", "init", nil)
	// Construct an authentic pre-upgrade row, bound to its own historical ID.
	// This is not a fresh signature for the target new run's ID.
	legacy, err := p.seal("run", old.InstanceID, old.ID, old.Input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?; DELETE FROM plugin_event_admissions WHERE run_id=?`, legacy, old.ID, old.ID); err != nil {
		t.Fatal(err)
	}
	if err = finishClaimedFixtureRun(ctx, p, old, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	current := eventAdmissionQueue(t, a, old.InstanceID, "init", nil)
	if !bytes.Equal(old.Input, current.Input) || current.ID == old.ID {
		t.Fatal("substitution fixture must have identical plaintext and distinct IDs")
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?; DELETE FROM plugin_event_admissions WHERE run_id=?`, legacy, current.ID, current.ID); err != nil {
		t.Fatal(err)
	}
	if err = p.configureEventSubscription(ctx, current, "forbidden", []string{"import.completed"}, 0, true); err == nil {
		t.Fatal("valid legacy ciphertext from another run downgraded new admission")
	}
	if pluginEventCount(t, a, "plugin_event_subscriptions") != 0 {
		t.Fatal("legacy substitution partially registered authority")
	}
}

func TestPluginEventAdmissionActiveFloorRejectsCapturedRewind(t *testing.T) {
	for _, action := range []string{"init", "reload"} {
		for _, cursor := range []int64{-1, 1, 2, 3, 4} {
			t.Run(fmt.Sprintf("%s/cursor=%d", action, cursor), func(t *testing.T) {
				a := eventAdmissionHost(t)
				p := a.pluginStore
				ctx := context.Background()
				first := eventAdmissionQueue(t, a, "admission", "init", nil)
				eventAdmissionSubscribe(t, p, first, "captured")
				if err := finishClaimedFixtureRun(ctx, p, first, "succeeded", "", nil); err != nil {
					t.Fatal(err)
				}
				for i := 1; i <= 3; i++ {
					commitDomainFixture(t, a, domainFixture("import.completed", fmt.Sprintf("floor-event-%d", i)))
				}
				if _, err := a.Store.DB.Exec(`UPDATE plugin_event_subscriptions SET cursor=2 WHERE handler_key='captured'`); err != nil {
					t.Fatal(err)
				}
				run := eventAdmissionQueue(t, a, "admission", action, nil)
				proof, err := p.readEventAdmissionTx(ctx, a.Store.DB, run)
				if err != nil || proof == nil || len(proof.ActiveCursorFloor) != 1 || proof.ActiveCursorFloor["captured"] != 2 {
					t.Fatal("active enqueue cursor not sealed", proof, err)
				}
				if action == "init" && len(proof.PerHandlerReloadCursor) != 0 {
					t.Fatal("init floor acquired reload fallback authority")
				}
				if _, err = a.Store.DB.Exec(`UPDATE plugin_event_subscriptions SET cursor=? WHERE handler_key='captured'`, cursor); err != nil {
					t.Fatal(err)
				}
				err = p.configureEventSubscription(ctx, run, "captured", []string{"import.completed"}, 0, true)
				if cursor < 2 || cursor > 3 {
					if pluginErrorCode(err) != "PLUGIN_RUN_PROOF_INVALID" {
						t.Fatal("captured active rewind or out-of-bounds cursor accepted", cursor, err)
					}
				} else if err != nil {
					t.Fatal("genuine active progress rejected", cursor, err)
				}
				if got := eventAdmissionCursor(t, p, run.InstanceID, "captured"); got != cursor {
					t.Fatal("validation rewrote active row", got, cursor)
				}
			})
		}
	}
}

func TestPluginEventAdmissionInitFloorDoesNotChangeInactiveFallback(t *testing.T) {
	for _, mode := range []string{"inactive", "missing"} {
		t.Run(mode, func(t *testing.T) {
			a := eventAdmissionHost(t)
			p := a.pluginStore
			ctx := context.Background()
			first := eventAdmissionQueue(t, a, "admission", "init", nil)
			eventAdmissionSubscribe(t, p, first, "captured")
			if err := finishClaimedFixtureRun(ctx, p, first, "succeeded", "", nil); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 3; i++ {
				commitDomainFixture(t, a, domainFixture("import.completed", fmt.Sprintf("fallback-event-%d", i)))
			}
			if _, err := a.Store.DB.Exec(`UPDATE plugin_event_subscriptions SET cursor=1 WHERE handler_key='captured'`); err != nil {
				t.Fatal(err)
			}
			run := eventAdmissionQueue(t, a, "admission", "init", nil)
			proof, err := p.readEventAdmissionTx(ctx, a.Store.DB, run)
			if err != nil || proof == nil || proof.ActiveCursorFloor["captured"] != 1 || len(proof.PerHandlerReloadCursor) != 0 || proof.HighWater != 3 {
				t.Fatal("init floor contaminated reload map", proof, err)
			}
			statement := `UPDATE plugin_event_subscriptions SET active=0 WHERE handler_key='captured'`
			if mode == "missing" {
				statement = `DELETE FROM plugin_event_subscriptions WHERE handler_key='captured'`
			}
			if _, err = a.Store.DB.Exec(statement); err != nil {
				t.Fatal(err)
			}
			commitDomainFixture(t, a, domainFixture("import.completed", "after-fallback-enqueue"))
			eventAdmissionSubscribe(t, p, run, "captured")
			if got := eventAdmissionCursor(t, p, run.InstanceID, "captured"); got != 3 {
				t.Fatal("init floor caused replay or skipped queued gap", got)
			}
		})
	}
}

func TestPluginEventAdmissionAbsentHandlerPreservesLateInitRegistration(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			a := eventAdmissionHost(t)
			p := a.pluginStore
			ctx := context.Background()
			commitDomainFixture(t, a, domainFixture("import.completed", "before-older-init"))
			older := eventAdmissionQueue(t, a, "admission", "init", json.RawMessage(`{"event_after":1}`))
			if legacy {
				// Owned synthetic pre-upgrade ciphertext for this historical ID.
				sealed, err := p.seal("run", older.InstanceID, older.ID, older.Input)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?; DELETE FROM plugin_event_admissions WHERE run_id=?`, sealed, older.ID, older.ID); err != nil {
					t.Fatal(err)
				}
			}
			commitDomainFixture(t, a, domainFixture("import.completed", "before-newer-init"))
			queued, err := p.EnqueueRun(ctx, "admission", "init", nil)
			if err != nil {
				t.Fatal(err)
			}
			// The new admission saw no handler. An older, still-live init now
			// legitimately installs its earlier boundary before the new run starts.
			eventAdmissionSubscribe(t, p, older, "late")
			if err = finishClaimedFixtureRun(ctx, p, older, "succeeded", "", nil); err != nil {
				t.Fatal(err)
			}
			run, err := p.ClaimRun(ctx)
			if err != nil || run.ID != queued.ID {
				t.Fatal("newer init claim", err)
			}
			proof, err := p.readEventAdmissionTx(ctx, a.Store.DB, run)
			if err != nil || proof == nil || len(proof.ActiveCursorFloor) != 0 || proof.HighWater != 2 {
				t.Fatal("absent handler acquired a synthetic floor", proof, err)
			}
			eventAdmissionSubscribe(t, p, run, "late")
			if got := eventAdmissionCursor(t, p, run.InstanceID, "late"); got != 1 {
				t.Fatal("newer init skipped older legitimate active boundary", got)
			}
		})
	}
}

func TestPluginEventAdmissionUncapturedActiveCursorStillChecksBounds(t *testing.T) {
	for _, cursor := range []int64{-1, 2} {
		t.Run(fmt.Sprintf("cursor=%d", cursor), func(t *testing.T) {
			a := eventAdmissionHost(t)
			p := a.pluginStore
			commitDomainFixture(t, a, domainFixture("import.completed", "before-enqueue"))
			run := eventAdmissionQueue(t, a, "admission", "init", nil)
			if _, err := a.Store.DB.Exec(`INSERT INTO plugin_event_subscriptions VALUES(?,?,?,?,?,?,?,?)`, run.InstanceID, "uncaptured", run.Generation, run.ConfigRevision, `["import.completed"]`, 0, true, cursor); err != nil {
				t.Fatal(err)
			}
			if err := p.configureEventSubscription(context.Background(), run, "uncaptured", []string{"import.completed"}, 0, true); pluginErrorCode(err) != "PLUGIN_RUN_PROOF_INVALID" {
				t.Fatal("uncaptured active cursor outside committed event range accepted", err)
			}
		})
	}
}

func TestPluginEventAdmissionSchemaOneWithoutActiveFloor(t *testing.T) {
	for _, action := range []string{"init", "reload"} {
		t.Run(action, func(t *testing.T) {
			a := eventAdmissionHost(t)
			p := a.pluginStore
			ctx := context.Background()
			first := eventAdmissionQueue(t, a, "admission", "init", nil)
			eventAdmissionSubscribe(t, p, first, "captured")
			if err := finishClaimedFixtureRun(ctx, p, first, "succeeded", "", nil); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 3; i++ {
				commitDomainFixture(t, a, domainFixture("import.completed", fmt.Sprintf("old-schema-event-%d", i)))
			}
			if _, err := a.Store.DB.Exec(`UPDATE plugin_event_subscriptions SET cursor=2 WHERE handler_key='captured'`); err != nil {
				t.Fatal(err)
			}
			run := eventAdmissionQueue(t, a, "admission", action, nil)
			inputBefore := eventAdmissionInput(t, p, run)
			if !eventAdmissionRunMarked(inputBefore) {
				t.Fatal("schema compatibility fixture must retain marked run input")
			}
			proof, err := p.readEventAdmissionTx(ctx, a.Store.DB, run)
			if err != nil || proof == nil || proof.Schema != 1 || proof.ActiveCursorFloor["captured"] != 2 || proof.HighWater != 3 {
				t.Fatal("new producer fixture did not capture floor", proof, err)
			}
			raw, err := p.unseal("event_admission", run.InstanceID, run.ID, eventAdmissionSealed(t, p, run))
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if _, present := fields["active_cursor_floor"]; !present {
				t.Fatal("fixture did not start with the new field")
			}
			delete(fields, "active_cursor_floor")
			oldRaw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			// Re-sign owned synthetic data with this fixture's legitimate key to
			// emulate an authenticated old schema-1 producer that omitted the field.
			// This is neither successful keyless tampering nor legacy run-input
			// conversion: the marked run and its mandatory admission stay intact.
			oldSealed, err := p.seal("event_admission", run.InstanceID, run.ID, oldRaw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`UPDATE plugin_event_admissions SET sealed=? WHERE run_id=?`, oldSealed, run.ID); err != nil {
				t.Fatal(err)
			}
			proof, err = p.readEventAdmissionTx(ctx, a.Store.DB, run)
			if err != nil || proof == nil || proof.Schema != 1 || proof.ActiveCursorFloor != nil {
				t.Fatal("authenticated old schema rejected or given an invented floor", proof, err)
			}
			if (action == "init" && len(proof.PerHandlerReloadCursor) != 0) || (action == "reload" && proof.PerHandlerReloadCursor["captured"] != 2) {
				t.Fatal("old schema compatibility changed reload fallback", proof)
			}
			// The old producer proved no active lower floor, so registration must
			// not invent one from highWater or the distinct reload fallback map.
			if _, err = a.Store.DB.Exec(`UPDATE plugin_event_subscriptions SET cursor=1 WHERE handler_key='captured'`); err != nil {
				t.Fatal(err)
			}
			eventAdmissionSubscribe(t, p, run, "captured")
			if got := eventAdmissionCursor(t, p, run.InstanceID, "captured"); got != 1 {
				t.Fatal("old schema registration invented a lower floor", got)
			}
			if !bytes.Equal(eventAdmissionInput(t, p, run), inputBefore) || !bytes.Equal(eventAdmissionSealed(t, p, run), oldSealed) {
				t.Fatal("old schema read or registration rewrote authenticated storage")
			}
			for _, cursor := range []int64{-1, 4} {
				if _, err = a.Store.DB.Exec(`UPDATE plugin_event_subscriptions SET cursor=? WHERE handler_key='captured'`, cursor); err != nil {
					t.Fatal(err)
				}
				err = p.configureEventSubscription(ctx, run, "captured", []string{"import.completed"}, 0, true)
				if pluginErrorCode(err) != "PLUGIN_RUN_PROOF_INVALID" {
					t.Fatal("old schema bypassed current active cursor bounds", cursor, err)
				}
				if got := eventAdmissionCursor(t, p, run.InstanceID, "captured"); got != cursor {
					t.Fatal("failed old schema validation rewrote subscription", got, cursor)
				}
			}
			if _, err = a.Store.DB.Exec(`UPDATE plugin_event_subscriptions SET cursor=1 WHERE handler_key='captured'; DELETE FROM plugin_event_admissions WHERE run_id=?`, run.ID); err != nil {
				t.Fatal(err)
			}
			if missing, readErr := p.readEventAdmissionTx(ctx, a.Store.DB, run); missing != nil || pluginErrorCode(readErr) != "PLUGIN_RUN_PROOF_INVALID" {
				t.Fatal("omitted optional field allowed omitted mandatory admission", missing, readErr)
			}
			if err = p.configureEventSubscription(ctx, run, "captured", []string{"import.completed"}, 0, true); pluginErrorCode(err) != "PLUGIN_RUN_PROOF_INVALID" {
				t.Fatal("missing old schema admission downgraded registration to legacy", err)
			}
		})
	}
}
