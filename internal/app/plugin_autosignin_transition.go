package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"light-media/internal/plugins/autosignin"
	signinjournal "light-media/internal/plugins/autosignin/durable"
)

// Archived admissions are immutable. Their original run scopes, credentials'
// HMAC identities and response receipts are not rewritten into the new scope.
const pluginAutoSignInTransitionSchema = `CREATE TABLE IF NOT EXISTS plugin_signin_archive_meta(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,admission INTEGER NOT NULL,run_id TEXT NOT NULL UNIQUE,
 sealed BLOB NOT NULL,digest BLOB NOT NULL,receipt BLOB NOT NULL,
 PRIMARY KEY(instance_id,generation,admission));
CREATE TABLE IF NOT EXISTS plugin_signin_archive_rows(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,admission INTEGER NOT NULL,collection TEXT NOT NULL,token TEXT NOT NULL,part INTEGER NOT NULL,
 sealed BLOB NOT NULL,digest BLOB NOT NULL,size INTEGER NOT NULL,
 PRIMARY KEY(instance_id,generation,admission,collection,token,part));
CREATE TABLE IF NOT EXISTS plugin_signin_archive_config(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,admission INTEGER NOT NULL,to_revision INTEGER NOT NULL,sealed BLOB NOT NULL,
 PRIMARY KEY(instance_id,generation,admission,to_revision));`

type pluginAutoSignInTransitionReceipt struct {
	Version                                         int
	From, To                                        signinjournal.Scope
	Epoch, RunID, Action, InputHash, ConfigHash     string
	EffectiveRevision                               uint64
	OriginalMetaDigest                              []byte
	Rows, RowBytes, ConfigReceipts, UnsettledOutbox int
}

// transitionConfig is a native lifecycle operation, not a worker capability.
// Existing core barriers must actually cancel/join the old schedules and runs.
// Pending outbox is retained sealed for a later receipt-authorized handoff;
// archiving it is not notification/event delivery or permission to resend.
func (o *pluginAutoSignInOwner) transitionConfig(ctx context.Context, run PluginRun) (*pluginAutoSignInOwner, error) {
	return o.transitionConfigCommit(ctx, run, nil)
}

// commit is a private Go test seam for a lost acknowledgment after SQL commit.
// No protocol, config, environment variable or worker can select it.
func (o *pluginAutoSignInOwner) transitionConfigCommit(ctx context.Context, run PluginRun, commit func(*sql.Tx) error) (*pluginAutoSignInOwner, error) {
	if o == nil || run.ID == "" || run.InstanceID != o.binding.Instance || (run.Action != "init" && run.Action != "reload") {
		return nil, signinjournal.ErrStale
	}
	maintenance, err := o.backend(nil)
	if err != nil {
		return nil, err
	}
	release, err := maintenance.Hold(ctx, o.binding)
	if err != nil {
		return nil, err
	}
	defer release()
	o.a.pluginLifecycleMu.Lock()
	defer o.a.pluginLifecycleMu.Unlock()
	o.a.pluginMu.Lock()
	defer o.a.pluginMu.Unlock()
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, pluginAutoSignInTransitionSchema); err != nil {
		return nil, err
	}
	config, revision, err := o.transitionRunTx(ctx, tx, run)
	if err != nil {
		return nil, err
	}
	if next, found, e := o.readCommittedTransitionTx(ctx, tx, run, config, revision); e != nil {
		return nil, e
	} else if found {
		return o.transfer(next)
	}
	loaded, err := o.readTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if loaded.State.Scope != o.binding.Scope || loaded.State.Epoch != o.binding.Epoch {
		return nil, signinjournal.ErrStale
	}
	if err = o.transitionFenceTx(ctx, tx); err != nil {
		return nil, err
	}
	if loaded.State.ActiveRun != "" || len(loaded.State.Blocks) != 0 {
		return nil, signinjournal.ErrBusy
	}
	for _, r := range loaded.State.Runs {
		for _, entries := range r.Lanes {
			for _, e := range entries {
				if e.Phase == "intent" || e.Phase == "uncertain" {
					return nil, signinjournal.ErrUncertain
				}
			}
		}
	}
	var effective uint64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(to_revision),?) FROM plugin_signin_config_receipts WHERE instance_id=? AND generation=?`, o.binding.Revision, o.binding.Instance, o.binding.Generation).Scan(&effective); err != nil {
		return nil, err
	}
	if _, err = o.configChainTx(ctx, tx, effective, loaded.State.Config, loaded.State); err != nil {
		return nil, err
	}
	if revision <= effective {
		return nil, signinjournal.ErrStale
	}
	if err = o.transitionDrainedTx(ctx, tx, run); err != nil {
		return nil, err
	}
	to := o.binding.Scope
	to.Revision = revision
	next := &pluginAutoSignInOwner{a: o.a, binding: signinjournal.Binding{Scope: to, Epoch: o.binding.Epoch}, execution: make(chan struct{}, 1), testCapacity: o.testCapacity, testReserve: o.testReserve}
	newState, err := signinjournal.NewState(to, loaded.State.Runtime)
	if err != nil {
		return nil, err
	}
	newState.Epoch = o.binding.Epoch
	newState.RuntimeEpoch = loaded.State.RuntimeEpoch
	newState.Config = append(json.RawMessage(nil), config...)
	newState.History = loaded.State.History
	newState.Keys = loaded.State.Keys
	newState.Cache = loaded.State.Cache
	newState.CacheReady = loaded.State.CacheReady
	newState.Stats = loaded.State.Stats
	// Timer descriptors belong to the old admission. They have been proved
	// inactive in the actual core; source Init/Reload will publish new ones.
	newState.Services = json.RawMessage(`[]`)
	receipt := pluginAutoSignInTransitionReceipt{Version: 1, From: o.binding.Scope, To: to, Epoch: o.binding.Epoch, RunID: run.ID, Action: run.Action, InputHash: hex.EncodeToString(o.mac("transition-input", run.Input)), ConfigHash: hex.EncodeToString(o.mac("config-value", config)), EffectiveRevision: effective, OriginalMetaDigest: loaded.MetaDigest, Rows: len(loaded.Rows)}
	for _, r := range loaded.Rows {
		receipt.RowBytes += r.Size
	}
	for _, item := range loaded.State.Outbox {
		if item.Phase != "delivered" {
			receipt.UnsettledOutbox++
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_signin_config_receipts WHERE instance_id=? AND generation=?`, o.binding.Instance, o.binding.Generation).Scan(&receipt.ConfigReceipts); err != nil {
		return nil, err
	}
	raw, err := signinJSON(receipt)
	if err != nil {
		return nil, err
	}
	if len(raw) > pluginValueLimit {
		return nil, signinjournal.ErrCapacity
	}
	sealed, err := o.a.pluginStore.seal("signin_transition", o.binding.Instance, o.aad("archive", strconv.FormatUint(o.binding.Revision, 10), 0), raw)
	if err != nil {
		return nil, err
	}
	// Archive quota is checked before the first copy/publication. Current and
	// historical admissions share a conservative storage budget; quota is not a
	// claim that loading the entire closure meets a process memory target.
	currentBytes, totalBytes, err := o.transitionFootprintTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	capacity := pluginAutoSignInCapacity
	if o.testCapacity > 0 {
		capacity = o.testCapacity
	}
	// Existing + a full source archive + an upper bound for the replacement
	// namespace. Config may grow to the public 1MiB limit; account for JSON
	// base64/framing as well. This deliberately overestimates dropped old runs.
	extra := currentBytes*2 + 2*pluginValueLimit + 65536
	if totalBytes > capacity || extra > capacity-totalBytes {
		return nil, signinjournal.ErrCapacity
	}

	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_archive_meta(instance_id,generation,admission,run_id,sealed,digest,receipt) SELECT instance_id,generation,?, ?,sealed,digest,? FROM plugin_signin_meta WHERE instance_id=? AND generation=?`, o.binding.Revision, run.ID, sealed, o.binding.Instance, o.binding.Generation); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_archive_rows(instance_id,generation,admission,collection,token,part,sealed,digest,size) SELECT instance_id,generation,?,collection,token,part,sealed,digest,size FROM plugin_signin_rows WHERE instance_id=? AND generation=?`, o.binding.Revision, o.binding.Instance, o.binding.Generation); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_archive_config(instance_id,generation,admission,to_revision,sealed) SELECT instance_id,generation,?,to_revision,sealed FROM plugin_signin_config_receipts WHERE instance_id=? AND generation=?`, o.binding.Revision, o.binding.Instance, o.binding.Generation); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_signin_config_receipts WHERE instance_id=? AND generation=?`, o.binding.Instance, o.binding.Generation); err != nil {
		return nil, err
	}
	if err = next.writeTx(ctx, tx, loaded, newState); err != nil {
		return nil, err
	}
	proof, err := signinJSON(next.binding)
	if err != nil {
		return nil, err
	}
	ownerSealed, err := o.a.pluginStore.seal("signin_owner", o.binding.Instance, "current", proof)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_signin_owners SET sealed=? WHERE instance_id=? AND generation=?`, ownerSealed, o.binding.Instance, o.binding.Generation); err != nil {
		return nil, err
	}
	if commit == nil {
		commit = func(tx *sql.Tx) error { return tx.Commit() }
	}
	if err = commit(tx); err != nil {
		_ = tx.Rollback()
		checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		check, readErr := o.a.Store.DB.BeginTx(checkCtx, &sql.TxOptions{ReadOnly: true})
		if readErr != nil {
			return nil, signinjournal.ErrUncertain
		}
		defer check.Rollback()
		actual, found, readErr := o.readCommittedTransitionTx(checkCtx, check, run, config, revision)
		if readErr != nil {
			return nil, signinjournal.ErrUncertain
		}
		if !found {
			return nil, err
		}
		return o.transfer(actual)
	}
	return o.transfer(next)
}

func (o *pluginAutoSignInOwner) transfer(next *pluginAutoSignInOwner) (*pluginAutoSignInOwner, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.file == nil || next == nil || next.binding.Instance != o.binding.Instance {
		return nil, signinjournal.ErrStale
	}
	next.file = o.file
	o.file = nil
	o.closed = true
	return next, nil
}
func (o *pluginAutoSignInOwner) transitionFenceTx(ctx context.Context, tx *sql.Tx) error {
	o.mu.Lock()
	live := !o.closed && o.file != nil
	o.mu.Unlock()
	if !live {
		return signinjournal.ErrStale
	}
	var sealed []byte
	var generation uint64
	if err := tx.QueryRowContext(ctx, `SELECT generation,sealed FROM plugin_signin_owners WHERE instance_id=?`, o.binding.Instance).Scan(&generation, &sealed); err != nil {
		return err
	}
	raw, err := o.a.pluginStore.unseal("signin_owner", o.binding.Instance, "current", sealed)
	if err != nil {
		return err
	}
	var b signinjournal.Binding
	if cleanupJournalStrict(raw, &b) != nil || b != o.binding || generation != o.binding.Generation {
		return signinjournal.ErrStale
	}
	return nil
}
func (o *pluginAutoSignInOwner) transitionRunTx(ctx context.Context, tx *sql.Tx, run PluginRun) (json.RawMessage, uint64, error) {
	var plugin, version string
	var generation, revision uint64
	var installed, enabled bool
	var sealed []byte
	err := tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision,v.sealed FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, o.binding.Instance).Scan(&plugin, &version, &generation, &installed, &enabled, &revision, &sealed)
	if err != nil {
		return nil, 0, err
	}
	if plugin != "AutoSignIn" || version != autosignin.Version || !installed || !enabled || generation != o.binding.Generation || run.InstanceID != o.binding.Instance || run.Generation != int64(generation) || run.ConfigRevision != int64(revision) || run.Version != version {
		return nil, 0, signinjournal.ErrStale
	}
	config, err := o.a.pluginStore.unseal("config", o.binding.Instance, "main", sealed)
	if err != nil {
		return nil, 0, err
	}
	var actual PluginRun
	var inputSealed []byte
	err = tx.QueryRowContext(ctx, `SELECT id,instance_id,generation,config_revision,version,action,state,input FROM plugin_runs WHERE id=?`, run.ID).Scan(&actual.ID, &actual.InstanceID, &actual.Generation, &actual.ConfigRevision, &actual.Version, &actual.Action, &actual.State, &inputSealed)
	if err != nil {
		return nil, 0, err
	}
	if actual.InstanceID != run.InstanceID || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Version != run.Version || actual.Action != run.Action || actual.State != "running" {
		return nil, 0, signinjournal.ErrStale
	}
	input, err := o.a.pluginStore.unseal("run", o.binding.Instance, run.ID, inputSealed)
	if err != nil {
		return nil, 0, err
	}
	if !bytes.Equal(input, run.Input) {
		return nil, 0, signinjournal.ErrStale
	}
	var invocation struct {
		Action string          `json:"action"`
		Config json.RawMessage `json:"config"`
		Input  json.RawMessage `json:"input,omitempty"`
	}
	if cleanupJournalStrict(input, &invocation) != nil || invocation.Action != run.Action || !bytes.Equal(bytes.TrimSpace(invocation.Config), bytes.TrimSpace(config)) {
		return nil, 0, signinjournal.ErrStale
	}
	return config, revision, nil
}
func (o *pluginAutoSignInOwner) transitionDrainedTx(ctx context.Context, tx *sql.Tx, run PluginRun) error {
	for _, query := range []string{
		`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND active=1`,
		`SELECT COUNT(*) FROM plugin_event_subscriptions WHERE instance_id=? AND active=1`,
		`SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE instance_id=? AND state IN ('queued','running')`,
	} {
		var count int
		if err := tx.QueryRowContext(ctx, query, o.binding.Instance).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return signinjournal.ErrBusy
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND id<>? AND state IN ('pending','running','reconcile_pending')`, o.binding.Instance, run.ID).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return signinjournal.ErrBusy
	}
	return nil
}

func (o *pluginAutoSignInOwner) readCommittedTransitionTx(ctx context.Context, tx *sql.Tx, run PluginRun, config []byte, revision uint64) (*pluginAutoSignInOwner, bool, error) {
	var admission uint64
	var rawReceipt, metaSealed, metaDigest []byte
	err := tx.QueryRowContext(ctx, `SELECT admission,receipt,sealed,digest FROM plugin_signin_archive_meta WHERE instance_id=? AND generation=? AND run_id=?`, o.binding.Instance, o.binding.Generation, run.ID).Scan(&admission, &rawReceipt, &metaSealed, &metaDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	raw, err := o.a.pluginStore.unseal("signin_transition", o.binding.Instance, o.aad("archive", strconv.FormatUint(admission, 10), 0), rawReceipt)
	if err != nil {
		return nil, false, err
	}
	var r pluginAutoSignInTransitionReceipt
	if cleanupJournalStrict(raw, &r) != nil || r.Version != 1 || r.From != o.binding.Scope || r.To.Instance != o.binding.Instance || r.To.Generation != o.binding.Generation || r.To.Implementation != o.binding.Implementation || r.To.Revision != revision || r.Epoch != o.binding.Epoch || r.RunID != run.ID || r.Action != run.Action || r.InputHash != hex.EncodeToString(o.mac("transition-input", run.Input)) || r.ConfigHash != hex.EncodeToString(o.mac("config-value", config)) || admission != o.binding.Revision || !hmac.Equal(r.OriginalMetaDigest, metaDigest) {
		return nil, false, signinStoreError()
	}
	metaRaw, err := o.a.pluginStore.unseal("signin_journal", o.binding.Instance, o.aad("meta", "", 0), metaSealed)
	if err != nil || !hmac.Equal(o.mac("meta", metaRaw), metaDigest) {
		return nil, false, signinStoreError()
	}
	next := &pluginAutoSignInOwner{a: o.a, binding: signinjournal.Binding{Scope: r.To, Epoch: r.Epoch}, execution: make(chan struct{}, 1)}
	current, err := next.readTx(ctx, tx)
	if err != nil {
		return nil, false, err
	}
	if current.State.Scope != r.To || current.State.Epoch != r.Epoch {
		return nil, false, signinStoreError()
	}
	if _, _, err = o.archivedStateTx(ctx, tx, admission); err != nil {
		return nil, false, err
	}
	var sealed []byte
	if err = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_signin_owners WHERE instance_id=? AND generation=?`, o.binding.Instance, o.binding.Generation).Scan(&sealed); err != nil {
		return nil, false, err
	}
	proofRaw, err := o.a.pluginStore.unseal("signin_owner", o.binding.Instance, "current", sealed)
	if err != nil {
		return nil, false, err
	}
	var proof signinjournal.Binding
	if cleanupJournalStrict(proofRaw, &proof) != nil || proof != next.binding {
		return nil, false, signinjournal.ErrStale
	}
	return next, true, nil
}

// readArchived is a native audit helper. It does not expose historical replies
// or enqueue an event. Future historical handoff must additionally verify the
// original producer effect and supply a deduplicated committed core receipt.
func (o *pluginAutoSignInOwner) readArchived(ctx context.Context, admission uint64) (signinjournal.State, pluginAutoSignInTransitionReceipt, error) {
	b, err := o.backend(nil)
	if err != nil {
		return signinjournal.State{}, pluginAutoSignInTransitionReceipt{}, err
	}
	release, err := b.retain(ctx, o.binding)
	if err != nil {
		return signinjournal.State{}, pluginAutoSignInTransitionReceipt{}, err
	}
	defer release()
	o.a.pluginMu.Lock()
	defer o.a.pluginMu.Unlock()
	tx, err := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return signinjournal.State{}, pluginAutoSignInTransitionReceipt{}, err
	}
	defer tx.Rollback()
	loaded, err := o.readTx(ctx, tx)
	if err != nil {
		return signinjournal.State{}, pluginAutoSignInTransitionReceipt{}, err
	}
	if err = b.guardTx(ctx, tx, o.binding, loaded.State); err != nil {
		return signinjournal.State{}, pluginAutoSignInTransitionReceipt{}, err
	}
	return o.archivedStateTx(ctx, tx, admission)
}
func (o *pluginAutoSignInOwner) archivedStateTx(ctx context.Context, tx *sql.Tx, admission uint64) (signinjournal.State, pluginAutoSignInTransitionReceipt, error) {
	var state signinjournal.State
	var receipt pluginAutoSignInTransitionReceipt
	var metaSealed, metaDigest, receiptSealed []byte
	var runID string
	err := tx.QueryRowContext(ctx, `SELECT run_id,sealed,digest,receipt FROM plugin_signin_archive_meta WHERE instance_id=? AND generation=? AND admission=?`, o.binding.Instance, o.binding.Generation, admission).Scan(&runID, &metaSealed, &metaDigest, &receiptSealed)
	if err != nil {
		return state, receipt, err
	}
	raw, err := o.a.pluginStore.unseal("signin_transition", o.binding.Instance, o.aad("archive", strconv.FormatUint(admission, 10), 0), receiptSealed)
	if err != nil {
		return state, receipt, err
	}
	if cleanupJournalStrict(raw, &receipt) != nil || receipt.Version != 1 || receipt.From.Instance != o.binding.Instance || receipt.From.Generation != o.binding.Generation || receipt.From.Revision != admission || receipt.To.Instance != o.binding.Instance || receipt.To.Generation != o.binding.Generation || receipt.To.Implementation != receipt.From.Implementation || receipt.To.Revision <= admission || receipt.RunID != runID || !hmac.Equal(receipt.OriginalMetaDigest, metaDigest) {
		return state, receipt, signinStoreError()
	}
	raw, err = o.a.pluginStore.unseal("signin_journal", o.binding.Instance, o.aad("meta", "", 0), metaSealed)
	if err != nil {
		return state, receipt, err
	}
	if !hmac.Equal(o.mac("meta", raw), metaDigest) {
		return state, receipt, signinStoreError()
	}
	var meta pluginAutoSignInMeta
	if cleanupJournalStrict(raw, &meta) != nil || meta.Version != 1 || meta.Scope != receipt.From || meta.Epoch != receipt.Epoch || meta.Count < 0 || meta.Count > pluginAutoSignInRowLimit || meta.Bytes < 0 || meta.Bytes > pluginAutoSignInCapacity || meta.Count != receipt.Rows || meta.Bytes != receipt.RowBytes {
		return state, receipt, signinStoreError()
	}
	rows, err := tx.QueryContext(ctx, `SELECT collection,token,part,sealed,digest,size FROM plugin_signin_archive_rows WHERE instance_id=? AND generation=? AND admission=? ORDER BY collection,token,part`, o.binding.Instance, o.binding.Generation, admission)
	if err != nil {
		return state, receipt, err
	}
	physical := map[string]pluginAutoSignInPhysical{}
	groups := map[string][]pluginAutoSignInRow{}
	total := 0
	for rows.Next() {
		var p pluginAutoSignInPhysical
		var cipher []byte
		if err = rows.Scan(&p.Collection, &p.Token, &p.Part, &cipher, &p.Digest, &p.Size); err != nil {
			break
		}
		if len(physical) >= pluginAutoSignInRowLimit || p.Part < 0 || p.Size < 1 || p.Size > pluginValueLimit || len(cipher) > pluginValueLimit+64 {
			err = signinStoreError()
			break
		}
		raw, err = o.a.pluginStore.unseal("signin_journal", o.binding.Instance, o.aad(p.Collection, p.Token, p.Part), cipher)
		if err != nil {
			break
		}
		if len(raw) != p.Size || !hmac.Equal(o.mac("row", raw), p.Digest) {
			err = signinStoreError()
			break
		}
		var row pluginAutoSignInRow
		if cleanupJournalStrict(raw, &row) != nil || row.Version != 1 || row.Instance != o.binding.Instance || row.Generation != o.binding.Generation || row.Collection != p.Collection || row.Token != p.Token || row.Part != p.Part || row.Parts < 1 || row.Part >= row.Parts || len(row.Data) > pluginAutoSignInChunk || row.Token != o.token(row.Collection, row.Key) {
			err = signinStoreError()
			break
		}
		total += p.Size
		if total > pluginAutoSignInCapacity {
			err = signinjournal.ErrCapacity
			break
		}
		physical[signinPhysicalKey(p)] = p
		key := signinKey(row.Collection, row.Key)
		groups[key] = append(groups[key], row)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return state, receipt, err
	}
	aggregate, size := o.aggregate(physical)
	if len(physical) != meta.Count || size != meta.Bytes || !hmac.Equal(aggregate, meta.Digest) {
		return state, receipt, signinStoreError()
	}
	cells := map[string]pluginAutoSignInCell{}
	for key, parts := range groups {
		first := parts[0]
		if len(parts) != first.Parts {
			return state, receipt, signinStoreError()
		}
		var data []byte
		if !first.Nil {
			data = []byte{}
		}
		for i, p := range parts {
			if p.Part != i || p.Parts != first.Parts || p.Nil != first.Nil || p.Key != first.Key || p.Collection != first.Collection || p.Nil && (p.Parts != 1 || len(p.Data) != 0) || i < len(parts)-1 && len(p.Data) != pluginAutoSignInChunk {
				return state, receipt, signinStoreError()
			}
			data = append(data, p.Data...)
		}
		cells[key] = pluginAutoSignInCell{first.Collection, first.Key, data}
	}
	state, err = signinInflate(meta, cells)
	if err != nil {
		return state, receipt, err
	}
	if state.Scope != receipt.From {
		return state, receipt, signinStoreError()
	}
	if err = o.archiveConfigChainTx(ctx, tx, admission, state, receipt); err != nil {
		return state, receipt, err
	}
	unsettled := 0
	for _, item := range state.Outbox {
		if item.Phase != "delivered" {
			unsettled++
		}
	}
	if unsettled != receipt.UnsettledOutbox {
		return state, receipt, signinStoreError()
	}
	return state, receipt, nil
}
func (o *pluginAutoSignInOwner) archiveConfigChainTx(ctx context.Context, tx *sql.Tx, admission uint64, state signinjournal.State, transition pluginAutoSignInTransitionReceipt) error {
	rows, err := tx.QueryContext(ctx, `SELECT to_revision,sealed FROM plugin_signin_archive_config WHERE instance_id=? AND generation=? AND admission=? ORDER BY to_revision`, o.binding.Instance, o.binding.Generation, admission)
	if err != nil {
		return err
	}
	from := admission
	previous := ""
	lastHash := ""
	count := 0
	admissions := map[uint64]bool{admission: true}
	for rows.Next() {
		var to uint64
		var sealed []byte
		if err = rows.Scan(&to, &sealed); err != nil {
			break
		}
		count++
		if count > pluginAutoSignInRowLimit {
			err = signinjournal.ErrCapacity
			break
		}
		raw, e := o.a.pluginStore.unseal("signin_config", o.binding.Instance, o.aad("config", strconv.FormatUint(to, 10), 0), sealed)
		if e != nil {
			err = e
			break
		}
		var r pluginAutoSignInConfigReceipt
		if cleanupJournalStrict(raw, &r) != nil || r.Scope != state.Scope || r.From != from || r.To != to || to != from+1 || r.Previous != previous || !admissions[r.Admission] {
			err = signinStoreError()
			break
		}
		run := state.Runs[r.RunID]
		if run == nil || run.Scope != state.Scope || r.Index < 0 || r.Index >= len(run.Lanes[r.Lane]) {
			err = signinStoreError()
			break
		}
		entry := run.Lanes[r.Lane][r.Index]
		if entry.Kind != "config" || entry.Phase != "applied" || entry.Digest != r.Digest || r.ConfigHash != hex.EncodeToString(o.mac("config-value", entry.Value)) {
			err = signinStoreError()
			break
		}
		from = to
		admissions[to] = true
		lastHash = r.ConfigHash
		previous = hex.EncodeToString(o.mac("config-receipt", raw))
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if count != transition.ConfigReceipts || from != transition.EffectiveRevision || lastHash != "" && lastHash != hex.EncodeToString(o.mac("config-value", state.Config)) {
		return signinStoreError()
	}
	return nil
}

// Account for all ciphertext tables, including historical config chains and
// metadata, before copying any namespace. The fixed per-row allowance covers
// public identifiers/index framing, without retaining ciphertext in memory.
func (o *pluginAutoSignInOwner) transitionFootprintTx(ctx context.Context, tx *sql.Tx) (int, int, error) {
	queries := []struct {
		table, expression string
		current           bool
	}{
		{"plugin_signin_meta", "LENGTH(sealed)+LENGTH(digest)+256", true},
		{"plugin_signin_rows", "LENGTH(sealed)+LENGTH(digest)+256", true},
		{"plugin_signin_owners", "LENGTH(sealed)+256", true},
		{"plugin_signin_config_receipts", "LENGTH(sealed)+256", true},
		{"plugin_signin_archive_meta", "LENGTH(sealed)+LENGTH(digest)+LENGTH(receipt)+256", false},
		{"plugin_signin_archive_rows", "LENGTH(sealed)+LENGTH(digest)+256", false},
		{"plugin_signin_archive_config", "LENGTH(sealed)+256", false},
	}
	current, total := 0, 0
	for _, q := range queries {
		var bytes int
		query := "SELECT COALESCE(SUM(" + q.expression + "),0) FROM " + q.table + " WHERE instance_id=?"
		if err := tx.QueryRowContext(ctx, query, o.binding.Instance).Scan(&bytes); err != nil {
			return 0, 0, err
		}
		if bytes < 0 || bytes > pluginAutoSignInCapacity {
			return 0, 0, signinjournal.ErrCapacity
		}
		total += bytes
		if total > pluginAutoSignInCapacity {
			return 0, 0, signinjournal.ErrCapacity
		}
		if q.current {
			current += bytes
		}
	}
	return current, total, nil
}
