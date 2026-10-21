package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
	source "light-media/internal/plugins/autosignin"
	"light-media/internal/plugins/autosignin/durable"
)

const autoSignInDeletedHandler = "site_deleted"

// Subscription authority is the committed source initialization receipt, not
// public configuration (which may have been edited before init has completed).
func autoSignInDeletedInitialized(state durable.State) bool {
	if state.RuntimeEpoch == "" || state.RuntimeEpoch != state.Epoch {
		return false
	}
	initialized := false
	for _, run := range state.Runs {
		if run != nil && run.Scope == state.Scope && run.Phase == "completed" && (run.Action == "init" || run.Action == "reload" || run.Action == "cold_init") && signinAppliedKind(run, "service.definitions") {
			initialized = true
		}
	}
	return initialized
}
func autoSignInDeletedReady(state durable.State) (bool, error) {
	if !autoSignInDeletedInitialized(state) {
		return false, nil
	}
	var runtime source.Object
	if source.Decode(state.Runtime, &runtime) != nil {
		return false, durable.ErrStore
	}
	return source.Truth(runtime["enabled"]), nil
}

// Called after projectConfigTx, in the same authenticated state transaction.
// Already bound invocations keep their immutable config/admission. Collecting
// batches can advance along this source-owned config chain before binding.
func (b *pluginAutoSignInBackend) projectSiteDeletedSubscriptionTx(ctx context.Context, tx *sql.Tx, before map[string]pluginAutoSignInCell, state durable.State) error {
	if b.run == nil {
		return nil
	}
	run := state.Runs[b.run.ID]
	if run == nil {
		return nil
	}
	active, err := autoSignInDeletedReady(state)
	if err != nil {
		return err
	}
	p := b.owner.a.pluginStore
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, b.run.InstanceID).Scan(&revision); err != nil {
		return err
	}
	var generation, oldRevision, cursor int64
	var wasActive bool
	err = tx.QueryRowContext(ctx, `SELECT generation,config_revision,active,cursor FROM plugin_event_subscriptions WHERE instance_id=? AND handler_key=?`, b.run.InstanceID, autoSignInDeletedHandler).Scan(&generation, &oldRevision, &wasActive, &cursor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	exists := err == nil
	// Do not create a subscription from a partial init, even for enabled config.
	if !exists && !active {
		return nil
	}
	sameAdmission := exists && generation == b.run.Generation && oldRevision >= int64(state.Scope.Revision)
	if sameAdmission {
		if _, err = p.autoSignInScheduleAdmissionTx(ctx, tx, b.run.InstanceID, generation, oldRevision); err != nil {
			return err
		}
	}
	if !sameAdmission || (!wasActive && active) {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM domain_events`).Scan(&cursor); err != nil {
			return err
		}
		if run.Phase == "completed" && (run.Action == "init" || run.Action == "reload" || run.Action == "cold_init") {
			var inv plugins.Invocation
			if cleanupJournalStrict(b.run.Input, &inv) != nil {
				return durable.ErrStore
			}
			cursor, err = autoSignInDeletedCursor(inv.Input, cursor)
			if err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_event_subscriptions VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,handler_key) DO UPDATE SET generation=excluded.generation,config_revision=excluded.config_revision,event_types=excluded.event_types,delay_ms=0,active=excluded.active,cursor=excluded.cursor`, b.run.InstanceID, autoSignInDeletedHandler, b.run.Generation, revision, `["site.deleted"]`, 0, active, cursor); err != nil {
		return err
	}
	if sameAdmission && oldRevision != revision {
		_, err = tx.ExecContext(ctx, `UPDATE plugin_event_batches SET config_revision=? WHERE instance_id=? AND handler_key=? AND generation=? AND config_revision=? AND run_id IS NULL AND state='collecting'`, revision, b.run.InstanceID, autoSignInDeletedHandler, b.run.Generation, oldRevision)
	}
	return err
}

// The generic event dispatcher creates action=event; only a sealed batch token
// can translate it into the source's site_deleted entrypoint. A raw SDK event,
// numeric ID or caller-supplied invocation never confers deletion authority.
func (a *App) autoSignInTrustedSiteDeleted(ctx context.Context, run PluginRun, inv plugins.Invocation) (durable.Command, error) {
	deny := durable.ErrStale
	var original plugins.Invocation
	var token struct {
		BatchID string `json:"batch_id"`
	}
	if (run.Action != "event" && run.Action != "site_deleted") || cleanupJournalStrict(run.Input, &original) != nil || original.Action != run.Action || inv.Action != original.Action || !bytes.Equal(inv.Input, original.Input) || !bytes.Equal(inv.Config, original.Config) || cleanupJournalStrict(original.Input, &token) != nil || !pluginStorageID.MatchString(token.BatchID) {
		return durable.Command{}, deny
	}
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return durable.Command{}, err
	}
	defer tx.Rollback()
	handled, state, err := a.pluginStore.autoSignInScheduleStateTx(ctx, tx, run.InstanceID, run.Generation, run.ConfigRevision)
	if err != nil {
		return durable.Command{}, err
	}
	if !handled {
		return durable.Command{}, deny
	}
	if !autoSignInDeletedInitialized(state) {
		return durable.Command{}, deny
	}
	actual, err := a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, run.ID))
	if err != nil || actual.State != "running" || actual.InstanceID != run.InstanceID || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Version != run.Version || actual.Action != run.Action || !bytes.Equal(actual.Input, run.Input) {
		return durable.Command{}, deny
	}
	var seq int64
	var eventID string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT d.event_seq,d.event_id,e.payload FROM plugin_event_batches b JOIN plugin_event_deliveries d ON d.batch_id=b.id AND d.instance_id=b.instance_id AND d.handler_key=b.handler_key JOIN domain_events e ON e.seq=d.event_seq AND e.event_id=d.event_id WHERE b.id=? AND b.run_id=? AND b.instance_id=? AND b.generation=? AND b.config_revision=? AND b.state='queued' AND b.handler_key=? AND b.item_count=1`, token.BatchID, run.ID, run.InstanceID, run.Generation, run.ConfigRevision, autoSignInDeletedHandler).Scan(&seq, &eventID, &raw)
	if err != nil {
		return durable.Command{}, deny
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_event_deliveries WHERE batch_id=?`, token.BatchID).Scan(&count); err != nil {
		return durable.Command{}, err
	}
	if count != 1 {
		return durable.Command{}, deny
	}
	var event DomainEvent
	if json.Unmarshal(raw, &event) != nil || event.EventID != eventID || event.Type != "site.deleted" {
		return durable.Command{}, deny
	}
	event.Sequence = seq
	data, err := a.Store.siteDeletedSourceData(ctx, tx, event)
	if err != nil {
		return durable.Command{}, err
	}
	return durable.Command{Action: "site_deleted", DeletedID: data["site_id"]}, nil
}

// An omitted install input and JSON null both mean no explicit replay boundary.
// Whitespace-only, malformed and non-object inputs are not missing input.
func autoSignInDeletedCursor(raw json.RawMessage, highWater int64) (int64, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return highWater, nil
	}
	var input struct {
		EventAfter *int64 `json:"event_after"`
	}
	if cleanupJournalStrict(raw, &input) != nil {
		return 0, durable.ErrStore
	}
	if input.EventAfter == nil {
		return highWater, nil
	}
	if *input.EventAfter < 0 || *input.EventAfter > highWater {
		return 0, durable.ErrStore
	}
	return *input.EventAfter, nil
}
