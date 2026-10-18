package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"

	cleanuphost "light-media/internal/plugins/removelink/host"
	transfer "light-media/internal/plugins/torrenttransfer"
)

const pluginTransferRetirementSchema = `CREATE TABLE IF NOT EXISTS plugin_transfer_retirements(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,token TEXT NOT NULL,sealed BLOB NOT NULL,
 PRIMARY KEY(instance_id,generation));`

type pluginTransferPrepared struct {
	a         *App
	owner     *pluginTransferOwner
	from      cleanuphost.Binding
	operation string
	token     string
	configSHA string
	drained   atomic.Bool
	released  atomic.Bool
	once      sync.Once
}

type pluginTransferRetirement struct {
	Version   int                 `json:"version"`
	Token     string              `json:"token"`
	Operation string              `json:"operation"`
	From      cleanuphost.Binding `json:"from"`
	To        cleanuphost.Binding `json:"to"`
	Run       string              `json:"run"`
	ConfigSHA string              `json:"config_sha"`
}

type pluginTransferTransitionSnapshot struct {
	binding   cleanuphost.Binding
	plugin    string
	version   string
	installed bool
	enabled   bool
	configSHA string
}

func (a *App) transferTransitionSnapshot(ctx context.Context, q pluginRemoveLinkJournalReader, instance string) (pluginTransferTransitionSnapshot, error) {
	s := pluginTransferTransitionSnapshot{binding: cleanuphost.Binding{InstanceID: instance}}
	var sealed []byte
	err := q.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,COALESCE(v.revision,0),v.sealed FROM plugin_installations i LEFT JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, instance).
		Scan(&s.plugin, &s.version, &s.binding.Generation, &s.installed, &s.enabled, &s.binding.Revision, &sealed)
	if err != nil {
		return s, err
	}
	if s.binding.Revision == 0 {
		if sealed != nil {
			return s, transfer.ErrReplayDiverged
		}
		return s, nil
	}
	raw, err := a.pluginStore.unseal("config", instance, "main", sealed)
	if err != nil {
		return s, err
	}
	s.configSHA = tokenHash(string(raw))
	return s, nil
}

func (p *pluginTransferPrepared) active() bool {
	return p != nil && p.a != nil && p.owner != nil && p.from.InstanceID != "" && p.from.Generation > 0 && p.owner.generation == p.from.Generation && !p.released.Load() && (p.operation == "reset" || p.operation == "uninstall")
}

// This is the bulk-transition entry point. Call it before taking any global
// lifecycle/process/DB lock. The HTTP wrapper already owns this gate and must
// not call it again. Holding the gate blocks new Transfer work while the original
// uninstall transaction deactivates schedules and cancels pending admissions.
func (a *App) prepareTransferTransition(ctx context.Context, in PluginInstallation, operation string) (*pluginTransferPrepared, error) {
	if (operation != "reset" && operation != "uninstall") || !isTorrentTransfer(in.PluginID) || in.Version != "1.10.3" || !in.Installed || in.Generation < 1 {
		return nil, transfer.ErrReplayDiverged
	}
	owner, err := a.transferOwner(ctx, in)
	if err != nil {
		return nil, err
	}
	if err = owner.lock(ctx); err != nil {
		return nil, err
	}
	p := &pluginTransferPrepared{a: a, owner: owner, operation: operation, token: id("ttt_")}
	err = func() error {
		a.pluginLifecycleMu.Lock()
		defer a.pluginLifecycleMu.Unlock()
		a.pluginMu.Lock()
		defer a.pluginMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, pluginTransferRetirementSchema); err != nil {
			return err
		}
		s, err := a.transferTransitionSnapshot(ctx, tx, in.InstanceID)
		if err != nil {
			return err
		}
		if s.plugin != in.PluginID || s.version != in.Version || !s.installed || s.binding.Generation != in.Generation || s.binding.Generation != owner.generation {
			return transfer.ErrReplayDiverged
		}
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT token FROM plugin_transfer_retirements WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&existing)
		if err == nil {
			return transfer.ErrReplayDiverged
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		p.from, p.configSHA = s.binding, s.configSHA
		return tx.Commit()
	}()
	if err != nil {
		p.release()
		return nil, err
	}
	return p, nil
}

func (p *pluginTransferPrepared) release() {
	if p == nil || p.owner == nil {
		return
	}
	p.once.Do(func() {
		p.released.Store(true)
		p.owner.unlock()
	})
}

func (p *pluginTransferPrepared) drain(ctx context.Context) error {
	if !p.active() {
		return transfer.ErrReplayDiverged
	}
	unknown, err := p.owner.journal.HasUnconfirmed(ctx, p.from.InstanceID, uint64(p.from.Generation))
	if err != nil {
		return err
	}
	if unknown {
		return transfer.ErrUnconfirmed
	}
	// No global or SQL lock is held during HasUnconfirmed or an owner wait.
	p.a.pluginLifecycleMu.Lock()
	defer p.a.pluginLifecycleMu.Unlock()
	p.a.pluginMu.Lock()
	defer p.a.pluginMu.Unlock()
	s, err := p.a.transferTransitionSnapshot(ctx, p.a.Store.DB, p.from.InstanceID)
	if err != nil {
		return err
	}
	if !isTorrentTransfer(s.plugin) || s.version != "1.10.3" || !s.installed || s.binding != p.from || s.configSHA != p.configSHA {
		return transfer.ErrReplayDiverged
	}
	p.drained.Store(true)
	return nil
}

func (p *pluginTransferPrepared) target() cleanuphost.Binding {
	return cleanuphost.Binding{InstanceID: p.from.InstanceID, Generation: p.from.Generation + 1, Revision: 0}
}
func (p *pluginTransferPrepared) validateTarget(s pluginTransferTransitionSnapshot, to cleanuphost.Binding) error {
	if to != p.target() || to.Generation <= p.from.Generation || !isTorrentTransfer(s.plugin) || s.version != "1.10.3" || s.binding.Generation != to.Generation || s.enabled || s.installed != (p.operation == "reset") {
		return transfer.ErrReplayDiverged
	}
	if p.operation == "reset" {
		if s.binding.Revision != 0 || s.configSHA != "" {
			return transfer.ErrReplayDiverged
		}
	} else if s.binding.Revision != p.from.Revision || s.configSHA != p.configSHA {
		// Uninstall retains the target's original config/data in the fixed Root
		// implementation. Revision zero is the retired binding, not a new value.
		return transfer.ErrReplayDiverged
	}
	return nil
}

// publishTx is called after the original Root uninstall/reset changes, in that
// exact transaction. It never obtains a gate, global mutex or DB-pool connection.
// A retirement has no target initialization run, so runID must remain empty.
func (p *pluginTransferPrepared) publishTx(ctx context.Context, tx *sql.Tx, to cleanuphost.Binding, runID string) error {
	if !p.active() || !p.drained.Load() || tx == nil || runID != "" {
		return transfer.ErrReplayDiverged
	}
	s, err := p.a.transferTransitionSnapshot(ctx, tx, p.from.InstanceID)
	if err != nil {
		return err
	}
	if err = p.validateTarget(s, to); err != nil {
		return err
	}
	fact := pluginTransferRetirement{Version: 1, Token: p.token, Operation: p.operation, From: p.from, To: to, Run: runID, ConfigSHA: p.configSHA}
	raw, err := json.Marshal(fact)
	if err != nil {
		return err
	}
	var token string
	var previous []byte
	err = tx.QueryRowContext(ctx, `SELECT token,sealed FROM plugin_transfer_retirements WHERE instance_id=? AND generation=?`, p.from.InstanceID, p.from.Generation).Scan(&token, &previous)
	if err == nil {
		plain, e := p.a.pluginStore.unseal("run_result", p.from.InstanceID, "transfer-retirement:"+p.token, previous)
		if e != nil {
			return e
		}
		if token != p.token || !bytes.Equal(plain, raw) {
			return transfer.ErrReplayDiverged
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	sealed, err := p.a.pluginStore.seal("run_result", p.from.InstanceID, "transfer-retirement:"+p.token, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_transfer_retirements(instance_id,generation,token,sealed) VALUES(?,?,?,?)`, p.from.InstanceID, p.from.Generation, p.token, sealed)
	return err
}

func (p *pluginTransferPrepared) resolve(ctx context.Context, committed bool) error {
	if !p.active() || committed && !p.drained.Load() {
		return transfer.ErrReplayDiverged
	}
	tx, err := p.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, err := p.a.transferTransitionSnapshot(ctx, tx, p.from.InstanceID)
	if err != nil {
		return err
	}
	var token string
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT token,sealed FROM plugin_transfer_retirements WHERE instance_id=? AND generation=?`, p.from.InstanceID, p.from.Generation).Scan(&token, &sealed)
	if !committed {
		if !errors.Is(err, sql.ErrNoRows) || !isTorrentTransfer(s.plugin) || s.version != "1.10.3" || !s.installed || s.binding != p.from || s.configSHA != p.configSHA {
			return bad("TORRENT_TRANSFER_TRANSITION_UNCERTAIN", "Transfer生命周期提交结果需要核对，已保留原宿主")
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if err = p.validateTarget(s, p.target()); err != nil {
		return err
	}
	raw, err := p.a.pluginStore.unseal("run_result", p.from.InstanceID, "transfer-retirement:"+p.token, sealed)
	if err != nil {
		return err
	}
	var fact pluginTransferRetirement
	if cleanupJournalStrict(raw, &fact) != nil || token != p.token || fact.Version != 1 || fact.Token != p.token || fact.Operation != p.operation || fact.From != p.from || fact.To != p.target() || fact.Run != "" || fact.ConfigSHA != p.configSHA {
		return transfer.ErrJournalIntegrity
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// The SQL connection and lifecycle locks are released before the runtime map
	// mutex. A concurrent new owner must never be evicted by an old resolution.
	runtime := &p.a.pluginTransfer
	runtime.mu.Lock()
	if runtime.owners[p.from.InstanceID] == p.owner {
		delete(runtime.owners, p.from.InstanceID)
	}
	runtime.mu.Unlock()
	return nil
}
