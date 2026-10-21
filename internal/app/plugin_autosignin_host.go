package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
	"light-media/internal/plugins/autosignin/durable"
)

const autoSignInSourceFailed = "AUTOSIGNIN_SOURCE_FAILED"

const autoSignInImplementation = "autosignin-native-2.9.14-durable-v1"

type pluginAutoSignInRuntime struct {
	mu      sync.Mutex
	owners  map[string]*pluginAutoSignInSlot
	closing bool
}
type pluginAutoSignInSlot struct {
	ready chan struct{}
	gate  chan struct{}
	owner *pluginAutoSignInOwner
	err   error
}

// A pending claim is published before IO. Neither flock acquisition nor an
// instance execution waits while the runtime map mutex is held.
func (a *App) autoSignInSlot(ctx context.Context, i PluginInstallation) (*pluginAutoSignInSlot, error) {
	return a.autoSignInGetSlot(ctx, i, true)
}
func (a *App) autoSignInGetSlot(ctx context.Context, i PluginInstallation, create bool) (*pluginAutoSignInSlot, error) {
	r := &a.pluginAutoSignIn
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return nil, durable.ErrStale
	}
	if r.owners == nil {
		r.owners = map[string]*pluginAutoSignInSlot{}
	}
	slot, exists := r.owners[i.InstanceID]
	if !exists && !create {
		r.mu.Unlock()
		return nil, durable.ErrStale
	}
	if !exists {
		slot = &pluginAutoSignInSlot{ready: make(chan struct{}), gate: make(chan struct{}, 1)}
		r.owners[i.InstanceID] = slot
	}
	r.mu.Unlock()
	if !exists {
		zone, err := autoBackupHostTimezone()
		if err == nil {
			var next *pluginAutoSignInOwner
			next, err = claimPluginAutoSignInOwner(ctx, a, i.InstanceID, autoSignInImplementation, zone)
			if err == nil {
				err = next.restoreRuntimeEpoch(ctx)
				if err != nil {
					_ = next.Close()
				} else {
					slot.owner = next
				}
			}
		}
		slot.err = err
		close(slot.ready)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-slot.ready:
	}
	if slot.err != nil {
		r.mu.Lock()
		if r.owners[i.InstanceID] == slot {
			delete(r.owners, i.InstanceID)
		}
		r.mu.Unlock()
		return nil, slot.err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case slot.gate <- struct{}{}:
	}
	r.mu.Lock()
	closing := r.closing
	r.mu.Unlock()
	if closing {
		<-slot.gate
		return nil, durable.ErrStale
	}
	if slot.owner.binding.Generation != uint64(i.Generation) {
		if !create {
			<-slot.gate
			return nil, durable.ErrStale
		}
		if err := slot.owner.Close(); err != nil {
			<-slot.gate
			return nil, err
		}
		zone, err := autoBackupHostTimezone()
		if err == nil {
			var next *pluginAutoSignInOwner
			next, err = claimPluginAutoSignInOwner(ctx, a, i.InstanceID, autoSignInImplementation, zone)
			if err == nil {
				err = next.restoreRuntimeEpoch(ctx)
				if err != nil {
					_ = next.Close()
				} else {
					slot.owner = next
				}
			}
		}
		if err != nil {
			<-slot.gate
			return nil, err
		}
	}
	return slot, nil
}
func (a *App) stopAutoSignInPlugins() {
	r := &a.pluginAutoSignIn
	r.mu.Lock()
	r.closing = true
	slots := make([]*pluginAutoSignInSlot, 0, len(r.owners))
	for _, s := range r.owners {
		slots = append(slots, s)
	}
	r.mu.Unlock()
	for _, s := range slots {
		<-s.ready
		s.gate <- struct{}{}
		if s.owner != nil {
			_ = s.owner.Close()
		}
		<-s.gate
	}
}

func autoSignInDeferredSpec(run PluginRun) (string, json.RawMessage, error) {
	var inv plugins.Invocation
	if run.Version != autosignin.Version || cleanupJournalStrict(run.Input, &inv) != nil || inv.Action != run.Action {
		return "", nil, durable.ErrStale
	}
	method := ""
	switch run.Action {
	case "init", "reload":
		method = autosignin.ConfigureCapability
	case "service", "event", "api":
		method = autosignin.RunCapability
	case "site_deleted":
		method = autosignin.DeleteCapability
	default:
		return "", nil, durable.ErrStale
	}
	return method, json.RawMessage(`{}`), nil
}

func autoSignInInvocationCommand(run PluginRun) (durable.Command, plugins.Invocation, error) {
	var inv plugins.Invocation
	var config any
	if cleanupJournalStrict(run.Input, &inv) != nil || inv.Action != run.Action || autosignin.Decode(inv.Config, &config) != nil {
		return durable.Command{}, inv, durable.ErrStale
	}
	c := durable.Command{Action: run.Action}
	switch run.Action {
	case "init", "reload":
		c.Config = config
	case "api":
		var req plugins.HTTPRequest
		if cleanupJournalStrict(inv.Input, &req) != nil || req.Key != "signin_by_domain" || req.Method != "GET" || req.RequestID == "" || len(req.Query["url"]) != 1 {
			return c, inv, durable.ErrStale
		}
		c.Domain = req.Query["url"][0]
		c.APIKeyAccepted = req.CredentialMatches["apikey"]
	case "service", "event", "site_deleted":
	default:
		return c, inv, durable.ErrStale
	}
	return c, inv, nil
}

func (a *App) pluginAutoSignInHost(ctx context.Context, i PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if i.PluginID != "AutoSignIn" || i.Version != autosignin.Version || !deferredPayloadEqual(raw, json.RawMessage(`{}`)) {
		return nil, durable.ErrStale
	}
	if method == autosignin.OptionsCapability {
		if run != nil {
			return nil, durable.ErrStale
		}
		inventory, err := a.autoSignInFormInventory(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(inventory)
	}
	if !i.Installed || !i.DesiredEnabled {
		return nil, durable.ErrStale
	}
	current, err := a.pluginStore.Installation(ctx, i.InstanceID)
	if err != nil || current.PluginID != i.PluginID || current.Version != i.Version || current.Generation != i.Generation || !current.Installed || !current.DesiredEnabled {
		return nil, durable.ErrStale
	}
	readOnly := method == autosignin.SnapshotCapability
	if readOnly {
		if run != nil {
			return nil, durable.ErrStale
		}
		return a.autoSignInReadOnlyPage(ctx, i)
	} else {
		if run == nil || run.InstanceID != i.InstanceID || run.Generation != i.Generation || run.Version != i.Version {
			return nil, durable.ErrStale
		}
		want, _, err := autoSignInDeferredSpec(*run)
		if err != nil || want != method {
			return nil, durable.ErrStale
		}
		if err := a.requireDeferredExecution(ctx, run, method); err != nil {
			return nil, err
		}
		if err := a.pluginStore.requirePluginExecutionHandoff(ctx, i.InstanceID); err != nil {
			return nil, err
		}
	}
	slot, err := a.autoSignInGetSlot(ctx, i, !readOnly)
	if err != nil {
		return nil, err
	}
	defer func() { <-slot.gate }()
	o := slot.owner
	b, err := o.backend(run)
	if err != nil {
		return nil, err
	}
	// The backend validates the actual immutable SQL run and the owner's receipt
	// chain, not generic revision equality (source config writes advance it).
	err = b.View(ctx, o.binding, func(*durable.State) error { return nil })
	if err != nil && run != nil && (run.Action == "init" || run.Action == "reload") {
		next, e := o.transitionConfig(ctx, *run)
		if e != nil {
			return nil, e
		}
		slot.owner = next
		o = next
		b, err = o.backend(run)
	}
	if err != nil {
		return nil, err
	}
	zone, err := autoBackupHostTimezone()
	if err != nil {
		return nil, err
	}
	j, err := newPluginAutoSignInTypedJournal(b)
	if err != nil {
		return nil, err
	}
	state, err := j.State(ctx, o.binding)
	if err != nil {
		return nil, err
	}
	command, inv, err := autoSignInInvocationCommand(*run)
	if err != nil {
		return nil, err
	}
	if run.Action == "api" {
		if err = a.validatePluginHTTPRun(ctx, *run); err != nil {
			return nil, err
		}
	}
	if run.Action == "service" || run.Action == "event" || run.Action == "site_deleted" {
		command, err = a.autoSignInTrustedCommand(ctx, *run, inv)
		if err != nil {
			return nil, err
		}
	}
	if old := state.Runs[run.ID]; old != nil {
		if old.Action == "cold_init" && run.Action == "init" {
			command.Action = "cold_init"
			command.Config = nil
		}
	} else if state.RuntimeEpoch != "" && state.RuntimeEpoch != o.binding.Epoch {
		return nil, durable.ErrStale
	}
	// A translated event plan is bound only after autoSignInTrustedCommand has
	// authenticated the native batch. Worker arguments can never set this field.
	if run.Action == "event" && command.Action == "site_deleted" {
		b.sourcePlan, err = json.Marshal(command)
		if err != nil {
			return nil, err
		}
	}
	deps, err := a.autoSignInSiteDependencies(ctx, o, *run)
	if err != nil {
		return nil, err
	}
	deps.Timezone = zone
	deps.Source = newAutoSignInSourcePort(b)
	controller := durable.Controller{Journal: j, Binding: o.binding, Dependencies: deps}
	copy(controller.DigestKey[:], o.mac("controller-digest", []byte(autoSignInImplementation)))
	result, err := controller.Execute(ctx, run.ID, command)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if run.Action == "api" && err == nil {
		return json.Marshal(plugins.HTTPResponse{Status: 200, Body: encoded, Headers: map[string]string{"Content-Type": "application/json"}})
	}
	return encoded, err
}

// Historical outcome inspection never claims an owner or resumes source work.
func (p *PluginStore) autoSignInDeferredOutcome(ctx context.Context, run PluginRun, method string) (json.RawMessage, string, bool, error) {
	want, _, err := autoSignInDeferredSpec(run)
	if err != nil || want != method {
		return nil, "", false, durable.ErrStale
	}
	var exists int
	if err = p.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='plugin_signin_owners'`).Scan(&exists); err != nil || exists == 0 {
		return nil, "", false, err
	}
	tx, err := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", false, err
	}
	defer tx.Rollback()
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_signin_owners WHERE instance_id=? AND generation=?`, run.InstanceID, run.Generation).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	raw, err := p.unseal("signin_owner", run.InstanceID, "current", sealed)
	if err != nil {
		return nil, "", false, err
	}
	var binding durable.Binding
	if cleanupJournalStrict(raw, &binding) != nil || binding.Instance != run.InstanceID || binding.Generation != uint64(run.Generation) || binding.Implementation != autoSignInImplementation {
		return nil, "", false, durable.ErrStore
	}
	o := &pluginAutoSignInOwner{a: &App{Store: p.store, pluginStore: p}, binding: binding}
	loaded, err := o.readTx(ctx, tx)
	if err != nil {
		return nil, "", false, err
	}
	actual, e := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, run.ID))
	if e != nil || actual.InstanceID != run.InstanceID || actual.Generation != run.Generation || actual.Version != run.Version || actual.Action != run.Action || actual.ConfigRevision != run.ConfigRevision || !bytes.Equal(actual.Input, run.Input) {
		return nil, "", false, durable.ErrStale
	}
	var effective uint64
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(to_revision),?) FROM plugin_signin_config_receipts WHERE instance_id=? AND generation=?`, binding.Revision, binding.Instance, binding.Generation).Scan(&effective); e != nil {
		return nil, "", false, e
	}
	admissions, e := o.configChainTx(ctx, tx, effective, loaded.State.Config, loaded.State)
	if e != nil || !admissions[uint64(run.ConfigRevision)] {
		return nil, "", false, durable.ErrStale
	}

	r := loaded.State.Runs[run.ID]
	if r == nil || r.Scope != binding.Scope || (r.Phase != "completed" && r.Phase != "failed_source") {
		return nil, "", false, nil
	}
	command, _, err := autoSignInInvocationCommand(run)
	if err != nil {
		return nil, "", false, err
	}
	if run.Action == "event" || run.Action == "site_deleted" || run.Action == "service" {
		// These plans are validated against their sealed origin by the host before
		// Start. Historical inspection additionally binds the exact stored run input.
		actual, e := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, run.ID))
		if e != nil || !bytes.Equal(actual.Input, run.Input) || actual.ConfigRevision != run.ConfigRevision {
			return nil, "", false, durable.ErrStale
		}
	} else {
		if r.Action == "cold_init" && run.Action == "init" {
			command.Action = "cold_init"
			command.Config = nil
		}
		plan, _ := json.Marshal(command)
		if !bytes.Equal(plan, r.Plan) {
			return nil, "", false, durable.ErrStale
		}
	}
	for _, entries := range r.Lanes {
		for _, e := range entries {
			if e.Kind == "source.notify" || e.Kind == "source.emit" {
				var envelope durable.SourceEnvelope
				if e.Phase != "receipt" || cleanupJournalStrict(e.Value, &envelope) != nil {
					return nil, "", false, durable.ErrUncertain
				}
				if _, found, e := p.autoSignInSourceResultHistoricalTx(ctx, tx, r, envelope.Request.Identity); e != nil || !found {
					return nil, "", false, durable.ErrUncertain
				}
			}
			if e.Phase == "intent" || e.Phase == "uncertain" {
				return nil, "", false, nil
			}
		}
	}
	result := append(json.RawMessage(nil), r.Result...)
	code := ""
	if r.Phase == "failed_source" {
		var final durable.SourceFailureFinal
		if cleanupJournalStrict(r.Result, &final) != nil || final.Version != durable.SourceProtocolVersion || !json.Valid(final.Result) {
			return nil, "", false, durable.ErrStore
		}
		proof, found, e := p.autoSignInSourceResultHistoricalTx(ctx, tx, r, final.Failure)
		if e != nil || !found || proof.Outcome != durable.SourceException {
			return nil, "", false, durable.ErrUncertain
		}
		result = append(json.RawMessage(nil), final.Result...)
		code = autoSignInSourceFailed
	}
	if run.Action == "api" {
		result, err = json.Marshal(plugins.HTTPResponse{Status: 200, Body: result, Headers: map[string]string{"Content-Type": "application/json"}})
	}
	return result, code, err == nil, err
}

// autoSignInRestorableTx authenticates quiescence, including independent source
// receipts. Elapsed time and an empty execution gate are not recovery evidence.
func (p *PluginStore) autoSignInRestorableTx(ctx context.Context, tx *sql.Tx, state durable.State) error {
	if state.ActiveRun != "" || len(state.Blocks) != 0 {
		return durable.ErrUncertain
	}
	for _, run := range state.Runs {
		if run == nil || (run.Phase != "completed" && run.Phase != "failed_source") {
			return durable.ErrUncertain
		}
		for _, entries := range run.Lanes {
			for _, entry := range entries {
				if entry.Phase == "intent" || entry.Phase == "uncertain" {
					return durable.ErrUncertain
				}
				if entry.Kind == "source.notify" || entry.Kind == "source.emit" {
					var envelope durable.SourceEnvelope
					if entry.Phase != "receipt" || cleanupJournalStrict(entry.Value, &envelope) != nil {
						return durable.ErrUncertain
					}
					if _, found, err := p.autoSignInSourceResultHistoricalTx(ctx, tx, run, envelope.Request.Identity); err != nil || !found {
						return durable.ErrUncertain
					}
				}
			}
		}
		if run.Phase == "failed_source" {
			var final durable.SourceFailureFinal
			if cleanupJournalStrict(run.Result, &final) != nil || final.Version != durable.SourceProtocolVersion || !json.Valid(final.Result) {
				return durable.ErrStore
			}
			result, found, err := p.autoSignInSourceResultHistoricalTx(ctx, tx, run, final.Failure)
			if err != nil || !found || result.Outcome != durable.SourceException {
				return durable.ErrUncertain
			}
		}
	}
	for _, item := range state.Outbox {
		if item.Phase == "intent" || item.Phase == "uncertain" {
			return durable.ErrUncertain
		}
	}
	return nil
}

// A process owner takeover is not source init. Preserve all source runtime,
// entropy, date jobs and services; only the authenticated runtime epoch changes.
func (o *pluginAutoSignInOwner) restoreRuntimeEpoch(ctx context.Context) error {
	b, err := o.backend(nil)
	if err != nil {
		return err
	}
	release, err := b.Hold(ctx, o.binding)
	if err != nil {
		return err
	}
	defer release()
	a := o.a
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	loaded, err := o.readTx(ctx, tx)
	if err != nil {
		return err
	}
	if err = b.guardTx(ctx, tx, o.binding, loaded.State); err != nil {
		return err
	}
	if loaded.State.RuntimeEpoch == "" {
		if len(loaded.State.Runs) != 0 || loaded.State.ActiveRun != "" || len(loaded.State.Blocks) != 0 {
			return durable.ErrUncertain
		}
		return nil
	}
	if loaded.State.RuntimeEpoch == o.binding.Epoch {
		return nil
	}
	if err = a.pluginStore.autoSignInRestorableTx(ctx, tx, loaded.State); err != nil {
		return err
	}
	loaded.State.RuntimeEpoch = o.binding.Epoch
	if err = o.writeTx(ctx, tx, loaded, loaded.State); err != nil {
		return err
	}
	return tx.Commit()
}

// Called by startup while pluginMu is held: read-only, no owner claim and no
// nested global lock. Existing initialized source state must never enqueue init.
func (a *App) shouldInitializeAutoSignIn(ctx context.Context, i PluginInstallation, config PluginStoredValue) (bool, error) {
	if i.PluginID != "AutoSignIn" || i.Version != autosignin.Version || !i.Installed || !i.DesiredEnabled {
		return false, durable.ErrStale
	}
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='plugin_signin_meta'`).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		return true, nil
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_signin_meta WHERE instance_id=? AND generation=?`, i.InstanceID, i.Generation).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		return true, nil
	}
	handled, state, err := a.pluginStore.autoSignInScheduleStateTx(ctx, tx, i.InstanceID, i.Generation, config.Revision)
	if err != nil {
		return false, err
	}
	if !handled || state.Scope.Implementation != autoSignInImplementation {
		return false, durable.ErrStale
	}
	if err = a.pluginStore.autoSignInRestorableTx(ctx, tx, state); err != nil {
		return false, err
	}
	return state.RuntimeEpoch == "", nil
}

// A page reads authenticated persisted state without claiming an execution
// owner. Cold startup intentionally has no process-local owner yet.
func (a *App) autoSignInReadOnlyPage(ctx context.Context, i PluginInstallation) (json.RawMessage, error) {
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, i.InstanceID).Scan(&revision); err != nil {
		return nil, err
	}
	handled, state, err := a.pluginStore.autoSignInScheduleStateTx(ctx, tx, i.InstanceID, i.Generation, revision)
	if err != nil {
		return nil, err
	}
	if !handled || state.Scope.Instance != i.InstanceID || state.Scope.Generation != uint64(i.Generation) || state.Scope.Implementation != autoSignInImplementation || state.RuntimeEpoch == "" || len(state.Runtime) == 0 {
		return nil, durable.ErrStale
	}
	// End the SQL snapshot before inventory acquires settings locks.
	if err = tx.Rollback(); err != nil {
		return nil, err
	}
	zone, err := autoBackupHostTimezone()
	if err != nil {
		return nil, err
	}
	var runtime autosignin.Object
	history := autosignin.Object{}
	if autosignin.Decode(state.Runtime, &runtime) != nil {
		return nil, durable.ErrStore
	}
	for key, raw := range state.History {
		var v any
		if autosignin.Decode(raw, &v) != nil {
			return nil, durable.ErrStore
		}
		history[key] = v
	}
	inventory, e := a.autoSignInFormInventory(ctx)
	if e != nil {
		return nil, e
	}
	engine, e := autosignin.NewAtZone(inventory, time.Now, zone)
	if e != nil {
		return nil, e
	}
	if e = engine.Restore(runtime, history); e != nil {
		return nil, e
	}
	page, e := engine.Page()
	if e != nil {
		return nil, e
	}
	return json.Marshal(page)
}
