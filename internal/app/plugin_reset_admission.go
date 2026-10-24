package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Reset admission is a host capability, not a worker argument or an HTTP flag.
// It joins before entering any existing lifecycle/owner handler. Store.Reset
// reuses the capability and therefore never joins under the handler's locks.
type pluginResetAdmissionKey struct{}

type pluginResetOutcome uint8

const (
	pluginResetNotStarted pluginResetOutcome = iota
	pluginResetTransactionOpen
	pluginResetCommitted
	pluginResetAborted
	pluginResetUnknown
)

type pluginResetAdmission struct {
	store        *PluginStore
	installation PluginInstallation
	transition   *pluginRunTransition
	mediaDrained bool
	mu           sync.Mutex
	tx           *sql.Tx
	outcome      pluginResetOutcome
	ownerPending bool
	finished     bool
}

func (p *PluginStore) resetAdmission(ctx context.Context, instance string) (*pluginResetAdmission, error) {
	if err := p.checkPluginFormWrite(ctx, instance, pluginFormWriteLifecycle); err != nil {
		return nil, err
	}
	proof, _ := ctx.Value(pluginResetAdmissionKey{}).(*pluginResetAdmission)
	if proof == nil || proof.store != p || proof.installation.InstanceID != instance || proof.transition == nil || proof.transition.supervisor != p.runSupervisor {
		return nil, errPluginRunTransitionState
	}
	proof.mu.Lock()
	finished := proof.finished
	proof.mu.Unlock()
	if finished {
		return nil, errPluginRunTransitionState
	}
	s := p.runSupervisor
	if !s.usable() {
		return nil, errPluginRunSupervisorRequired
	}
	s.mu.Lock()
	valid := proof.transition.phase == pluginRunTransitionOpen && s.fences[instance] == proof.transition && proof.transition.join.completeLocked()
	s.mu.Unlock()
	if !valid {
		return nil, errPluginRunTransitionState
	}
	return proof, nil
}

func (p *PluginStore) beginResetAdmission(ctx context.Context, in PluginInstallation, mediaDrained bool) (context.Context, *pluginResetAdmission, error) {
	if err := p.checkPluginFormWrite(ctx, in.InstanceID, pluginFormWriteLifecycle); err != nil {
		return ctx, nil, err
	}
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if !pluginStorageID.MatchString(in.InstanceID) || in.Generation < 1 || in.PluginID == "" || in.Version == "" {
		return ctx, nil, bad("PLUGIN_SNAPSHOT_STALE", "插件安装状态已变化")
	}
	// A running plugin may not synchronously reset and join its own ticket.
	if lease, _ := ctx.Value(pluginExecutionLeaseContextKey{}).(*pluginExecutionLease); lease != nil {
		return ctx, nil, bad("PLUGIN_RESET_EXECUTION_DENIED", "运行中的插件不能重置自身执行范围")
	}
	transition, err := p.runSupervisor.beginTransition([]string{in.InstanceID})
	if err != nil {
		return ctx, nil, err
	}
	for _, attempt := range transition.join.attempts {
		if _, e := p.finishCanceledUnenteredAttempt(ctx, attempt); e != nil {
			_ = transition.quarantine()
			return ctx, nil, errors.Join(&APIError{"PLUGIN_RESET_UNRESOLVED", "插件执行尚未确认结束，未重置配置", http.StatusConflict}, e)
		}
	}
	joinCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err = transition.Wait(joinCtx); err != nil {
		// Cancellation is not completion. Captured attempts can still be active.
		_ = transition.quarantine()
		return ctx, nil, errors.Join(&APIError{"PLUGIN_RESET_UNRESOLVED", "插件执行尚未确认收尾，重置未执行，执行入口保持关闭", http.StatusConflict}, err)
	}
	proof := &pluginResetAdmission{store: p, installation: pluginFormInstallationCopy(in), transition: transition, mediaDrained: mediaDrained}
	return context.WithValue(ctx, pluginResetAdmissionKey{}, proof), proof, nil
}

// prepareResetAdmission is also the trusted direct Store.Reset entry. Its
// caller must not hold an App lifecycle/owner lock unless a proof already came
// from the outer HTTP admission. No SQL transaction is open during this join.
func (p *PluginStore) prepareResetAdmission(ctx context.Context, instance string) (context.Context, *pluginResetAdmission, bool, error) {
	if err := p.checkPluginFormWrite(ctx, instance, pluginFormWriteLifecycle); err != nil {
		return ctx, nil, false, err
	}
	if ctx.Value(pluginResetAdmissionKey{}) != nil {
		proof, err := p.resetAdmission(ctx, instance)
		return ctx, proof, false, err
	}
	in, err := p.Installation(ctx, instance)
	if err != nil {
		return ctx, nil, false, err
	}
	ctx, proof, err := p.beginResetAdmission(ctx, in, false)
	return ctx, proof, true, err
}

// requireResetAdmissionTx binds this one-use capability to the final mutation
// transaction and checks exact installation identity before any Reset deletion.
// An empty live registry is not proof that cold durable attempts are resolved.
func (p *PluginStore) requireResetAdmissionTx(ctx context.Context, tx *sql.Tx, instance string) error {
	if err := p.requireResetAdmissionIdentityTx(ctx, tx, instance); err != nil {
		return err
	}
	return p.requireResetJournalQuiescenceTx(ctx, tx, instance)
}

// Bind the original fence and exact installation snapshot before native read-only
// integrity checks. No lifecycle deletion or schema mutation is authorized yet.
func (p *PluginStore) requireResetAdmissionIdentityTx(ctx context.Context, tx *sql.Tx, instance string) error {
	proof, err := p.resetAdmission(ctx, instance)
	if err != nil || tx == nil {
		return errors.Join(err, errPluginRunTransitionState)
	}
	proof.mu.Lock()
	if proof.tx != nil || proof.outcome != pluginResetNotStarted {
		proof.mu.Unlock()
		return errPluginRunTransitionState
	}
	proof.tx, proof.outcome = tx, pluginResetTransactionOpen
	proof.mu.Unlock()
	var plugin, version string
	var generation int64
	var installed bool
	if err = tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation,installed FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin, &version, &generation, &installed); err != nil {
		return err
	}
	in := proof.installation
	if installed != in.Installed || plugin != in.PluginID || version != in.Version || generation != in.Generation {
		return bad("PLUGIN_SNAPSHOT_STALE", "插件安装状态已变化")
	}
	return nil
}

// rollback/commit outcomes are recorded at the real transaction, never inferred
// from a response, context cancellation, or the current installation row.
func (p *pluginResetAdmission) rollback(tx *sql.Tx) {
	err := tx.Rollback()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tx != tx || p.outcome != pluginResetTransactionOpen {
		return
	}
	if err == nil {
		p.outcome = pluginResetAborted
	} else {
		p.outcome = pluginResetUnknown
	}
}

func (p *pluginResetAdmission) commit(tx *sql.Tx) error {
	p.mu.Lock()
	if p.tx != tx || p.outcome != pluginResetTransactionOpen {
		p.mu.Unlock()
		return errPluginRunTransitionState
	}
	p.outcome = pluginResetUnknown
	p.mu.Unlock()
	err := tx.Commit()
	if err == nil {
		p.mu.Lock()
		p.outcome = pluginResetCommitted
		p.mu.Unlock()
	}
	return err
}

func (p *pluginResetAdmission) setOwnerPending(pending bool) {
	p.mu.Lock()
	p.ownerPending = pending
	p.mu.Unlock()
}

func (p *pluginResetAdmission) finish(resultErr error) error {
	p.mu.Lock()
	if p.finished {
		p.mu.Unlock()
		return errors.Join(resultErr, errPluginRunTransitionState)
	}
	p.finished = true
	outcome, ownerPending := p.outcome, p.ownerPending
	p.mu.Unlock()
	// A successful handler has also resolved its native owner. On errors only
	// an explicit owner resolution, or no owner transition, permits reopening.
	resolved := resultErr == nil && outcome == pluginResetCommitted || resultErr != nil && !ownerPending && (outcome == pluginResetNotStarted || outcome == pluginResetAborted)
	if !resolved {
		_ = p.transition.quarantine()
		return errors.Join(&APIError{"PLUGIN_RESET_UNRESOLVED", "插件重置结果尚未确认，执行入口保持关闭，请核对后恢复", http.StatusConflict}, resultErr, errPluginRunUnresolved)
	}
	if err := p.transition.releaseAfterResolution(); err != nil {
		return errors.Join(resultErr, err)
	}
	return resultErr
}
