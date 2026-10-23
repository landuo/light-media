package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
)

// PluginInitializationProof describes an existing initialized business journal,
// not authority to initialize, repair, or advance an invocation. Installation
// identity is supplied only by the framework's verified transaction snapshot.
// InitializedRevision is the original init/reload head, not today's Form head.
type PluginInitializationProof struct {
	RunID                string
	Action               string
	Method               string
	InitializedRevision  int64
	InitializedConfigSHA string
	OriginRevision       int64
	OriginInputSHA       string
	OriginConfigSHA      string
	OriginAckSHA         string
	OriginRawSHA         string
}

// All copies of a facade share revocation and typed-proof failure. A callback
// cannot swallow a failed Config/VerifyInitialization call and commit a write.
// Hooks are synchronous trusted Go, not a concurrency or native-code sandbox.
type pluginDefaultsProjectionState struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	active  bool
	failure error
}

func newPluginDefaultsTransaction(ctx context.Context, tx *sql.Tx, store *PluginStore, installation PluginInstallation) pluginDefaultsTransaction {
	bound, cancel := context.WithCancel(ctx)
	return pluginDefaultsTransaction{
		tx: tx, store: store, instance: installation.InstanceID,
		installation: pluginFormInstallationCopy(installation),
		projection:   &pluginDefaultsProjectionState{ctx: bound, cancel: cancel, active: true},
	}
}

func invokePluginDefaultsProjector(ctx context.Context, transaction pluginDefaultsTransaction, change PluginDefaultsChange, projector PluginDefaultsProjector) error {
	defer transaction.revokeProjection()
	if err := projector(ctx, transaction, change); err != nil {
		return err
	}
	if err := transaction.projection.ctx.Err(); err != nil {
		return err
	}
	transaction.projection.mu.Lock()
	defer transaction.projection.mu.Unlock()
	return transaction.projection.failure
}

func (t pluginDefaultsTransaction) checkProjection(ctx context.Context) error {
	if ctx == nil || t.tx == nil || t.store == nil || t.projection == nil || t.projection.ctx == nil || !t.installation.Installed || t.installation.Generation < 1 || t.instance != t.installation.InstanceID || !pluginStorageID.MatchString(t.instance) || t.installation.PluginID == "" || t.installation.Version == "" {
		return bad("PLUGIN_CAPABILITY_DENIED", "插件投影事务不可用")
	}
	t.projection.mu.Lock()
	defer t.projection.mu.Unlock()
	if !t.projection.active {
		return bad("PLUGIN_CAPABILITY_DENIED", "插件投影事务不可用")
	}
	if err := t.projection.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return t.projection.failure
}

func (t pluginDefaultsTransaction) revokeProjection() {
	t.projection.mu.Lock()
	t.projection.active = false
	t.projection.mu.Unlock()
	t.projection.cancel()
}

func (t pluginDefaultsTransaction) projectionContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if err := t.checkProjection(ctx); err != nil {
		return nil, nil, err
	}
	// Scope/values always come from the original request. Install the caller's
	// earlier deadline directly instead of translating its expiry to Cancel.
	// The independent deadline retains DeadlineExceeded even when AfterFunc
	// has not been scheduled, or races with the caller's own timer.
	var bound context.Context
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		bound, cancel = context.WithDeadline(t.projection.ctx, deadline)
	} else {
		bound, cancel = context.WithCancel(t.projection.ctx)
	}
	stop := context.AfterFunc(ctx, func() {
		if errors.Is(ctx.Err(), context.Canceled) {
			cancel()
		}
	})
	return bound, func() { stop(); cancel() }, nil
}

// Cancellation propagation is asynchronous, so its derived ctx is never the
// only authority. Callers recheck the original ctx after custom code, and this
// final check retains the original reason and poisons a swallowed cancellation.
// Ordinary Data missing-row errors and unrelated operation errors are not
// independently made fatal. Actual SQL failures retain their error identity.
func (t pluginDefaultsTransaction) projectionContextError(caller context.Context, operationErr error) error {
	var contextErr error
	if caller != nil {
		contextErr = caller.Err()
	}
	if contextErr == nil && t.projection != nil && t.projection.ctx != nil {
		contextErr = t.projection.ctx.Err()
	}
	if contextErr != nil {
		t.captureProjectionFailure(contextErr)
		if operationErr == nil || errors.Is(operationErr, context.Canceled) || errors.Is(operationErr, context.DeadlineExceeded) {
			return contextErr
		}
	}
	if errors.Is(operationErr, context.Canceled) || errors.Is(operationErr, context.DeadlineExceeded) {
		t.captureProjectionFailure(operationErr)
	}
	return operationErr
}

func (t pluginDefaultsTransaction) captureProjectionFailure(err error) {
	if err == nil || t.projection == nil {
		return
	}
	t.projection.mu.Lock()
	defer t.projection.mu.Unlock()
	if t.projection.failure == nil {
		t.projection.failure = err
	}
}

// Config authenticates the database-current Next image using this very TX.
// It does not trust a caller's change.Next, read the pool, or consult a cache.
// Paused installations remain valid, but lifecycle/identity drift does not.
func (t pluginDefaultsTransaction) Config(ctx context.Context) (out PluginStoredValue, returnErr error) {
	callerCtx := ctx
	defer func() {
		returnErr = t.projectionContextError(callerCtx, returnErr)
		t.captureProjectionFailure(returnErr)
	}()
	ctx, cancel, err := t.projectionContext(ctx)
	if err != nil {
		return out, err
	}
	defer cancel()
	var plugin, version string
	var generation int64
	var installed, enabled bool
	if err := t.tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation,installed,desired_enabled FROM plugin_installations WHERE instance_id=?`, t.instance).Scan(&plugin, &version, &generation, &installed, &enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, pluginFormStale()
		}
		return out, err
	}
	in := t.installation
	if !installed || plugin != in.PluginID || version != in.Version || generation != in.Generation || enabled != in.DesiredEnabled {
		return out, pluginFormStale()
	}
	if _, err := t.store.instanceMetadata(ctx, t.tx, t.instance); err != nil {
		return out, err
	}
	var sealed []byte
	if err := t.tx.QueryRowContext(ctx, `SELECT revision,sealed,updated_at FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, t.instance).Scan(&out.Revision, &sealed, &out.UpdatedAt); err != nil {
		return PluginStoredValue{}, err
	}
	out.Value, err = t.store.unseal("config", t.instance, "main", sealed)
	if err != nil {
		return PluginStoredValue{}, err
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(out.Value, &object) != nil || object == nil {
		return PluginStoredValue{}, bad("INVALID_PLUGIN_CONFIG", "插件配置不是有效对象")
	}
	return pluginFormValueCopy(out), nil
}

func pluginInitializationProofInvalid() error {
	return bad("PLUGIN_RUN_PROOF_INVALID", "插件初始化证明无效")
}

// Missing/corrupt/authentication failures have a safe proof classification.
// Operational errors, including cancellation, deadlines, TxDone and SQL I/O,
// retain their identities so trusted callers do not mistake failure for absence.
func pluginInitializationProofError(err error) error {
	var api *APIError
	if errors.Is(err, sql.ErrNoRows) || errors.As(err, &api) {
		return pluginInitializationProofInvalid()
	}
	return err
}

func (t pluginDefaultsTransaction) VerifyInitialization(ctx context.Context, proof PluginInitializationProof) (returnErr error) {
	callerCtx := ctx
	defer func() {
		returnErr = t.projectionContextError(callerCtx, returnErr)
		t.captureProjectionFailure(returnErr)
	}()
	ctx, cancel, err := t.projectionContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	if proof.RunID == "" || proof.Method == "" || (proof.Action != "init" && proof.Action != "reload") || proof.OriginRevision < 1 || proof.InitializedRevision < proof.OriginRevision {
		return pluginInitializationProofInvalid()
	}
	for _, digest := range []string{proof.InitializedConfigSHA, proof.OriginInputSHA, proof.OriginConfigSHA, proof.OriginAckSHA, proof.OriginRawSHA} {
		if !pluginStorageDigest.MatchString(digest) {
			return pluginInitializationProofInvalid()
		}
	}
	// Revalidate the bound installation and metadata without requiring today's
	// config revision/hash to equal the immutable initialized revision/hash.
	if _, err := t.Config(ctx); err != nil {
		return err
	}
	in := t.installation
	run, err := t.store.scanPluginRun(t.tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, proof.RunID))
	if err != nil {
		return pluginInitializationProofError(err)
	}
	if run.InstanceID != in.InstanceID || run.Version != in.Version || run.Generation != in.Generation || run.ConfigRevision != proof.InitializedRevision || run.Action != proof.Action || (run.State != "running" && run.State != "succeeded") {
		return pluginInitializationProofInvalid()
	}
	receipt, _, err := t.store.readDeferred(ctx, t.tx, run)
	if err != nil {
		return pluginInitializationProofError(err)
	}
	if receipt.Phase != "confirmed" || receipt.Plugin != in.PluginID || receipt.Version != in.Version || receipt.Generation != in.Generation || receipt.Action != proof.Action || receipt.Method != proof.Method || receipt.Revision != proof.OriginRevision || receipt.InputSHA != proof.OriginInputSHA || receipt.ConfigSHA != proof.OriginConfigSHA || deferredInputSHA(receipt.Ack) != proof.OriginAckSHA || deferredInputSHA(receipt.Raw) != proof.OriginRawSHA || !json.Valid(receipt.Result) {
		return pluginInitializationProofInvalid()
	}
	var successors int
	if err = t.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_run_config_transitions WHERE run_id=? AND from_revision>=?`, run.ID, run.ConfigRevision).Scan(&successors); err != nil {
		return err
	}
	if successors != 0 {
		return pluginInitializationProofInvalid()
	}
	sha, err := t.store.verifyRunConfigChainTx(ctx, t.tx, in.PluginID, run, proof.OriginRevision, proof.OriginInputSHA, proof.OriginConfigSHA)
	if err != nil {
		return pluginInitializationProofError(err)
	}
	if sha != proof.InitializedConfigSHA {
		return pluginInitializationProofInvalid()
	}
	return nil
}

var _ PluginDefaultsTransaction = pluginDefaultsTransaction{}
var _ PluginDefaultsTransaction = (*pluginDefaultsTransaction)(nil)
