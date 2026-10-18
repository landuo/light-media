package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	native "light-media/internal/plugins/iyuuautoseed/native"
	"light-media/internal/plugins/schedule"
	transfer "light-media/internal/plugins/torrenttransfer"
)

func isTorrentTransfer(id string) bool { _, ok := transfer.Lookup(id); return ok }

type pluginTransferRuntime struct {
	mu              sync.Mutex
	owners          map[string]*pluginTransferOwner
	pollSleep       func(context.Context, time.Duration) error
	afterCheckpoint func() error
}
type pluginTransferOwner struct {
	gate       chan struct{}
	generation int64
	journal    *transfer.Journal
}

func (o *pluginTransferOwner) lock(ctx context.Context) error {
	select {
	case o.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *pluginTransferOwner) unlock() { <-o.gate }
func (a *App) transferOwner(ctx context.Context, in PluginInstallation) (*pluginTransferOwner, error) {
	if !isTorrentTransfer(in.PluginID) || in.Version != "1.10.3" || !in.Installed || in.Generation < 1 {
		return nil, bad("TORRENT_TRANSFER_OWNER_DENIED", "转移做种安装身份无效")
	}
	p := &a.pluginTransfer
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owners == nil {
		p.owners = map[string]*pluginTransferOwner{}
	}
	if o := p.owners[in.InstanceID]; o != nil {
		if o.generation == in.Generation {
			return o, nil
		}
		if len(o.gate) > 0 {
			return nil, transfer.ErrReplayDiverged
		}
		delete(p.owners, in.InstanceID)
	}
	j, e := transfer.AttachAnchoredJournal(ctx, a.Store.DB, a.pluginStore.transferJournalKey(in.InstanceID, in.Generation), transferRegistryAuthority{a.pluginStore, in.InstanceID, in.Generation})
	if e != nil {
		return nil, e
	}
	o := &pluginTransferOwner{gate: make(chan struct{}, 1), generation: in.Generation, journal: j}
	p.owners[in.InstanceID] = o
	return o, nil
}
func transferDeferredSpec(run PluginRun) (string, json.RawMessage, error) {
	method := ""
	var payload json.RawMessage
	_, e := transfer.Handle(context.Background(), "invoke", run.Input, func(_ context.Context, m string, b json.RawMessage) (json.RawMessage, error) {
		if method != "" {
			return nil, errors.New("multiple transfer terminal capabilities")
		}
		method = m
		payload = append(json.RawMessage{}, b...)
		return json.RawMessage(`{}`), nil
	})
	return method, payload, e
}
func transferSourceMap(raw []byte) (map[string]any, error) {
	var fields map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(&fields); e != nil || fields == nil {
		return nil, errors.New("invalid native transfer config")
	}
	return fields, nil
}
func (a *App) pluginTransferHost(ctx context.Context, in PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !isTorrentTransfer(in.PluginID) || in.Version != "1.10.3" {
		return nil, bad("TORRENT_TRANSFER_OWNER_DENIED", "转移做种安装身份无效")
	}
	if method == transfer.FormCapability {
		if run != nil || string(raw) != "{}" {
			return nil, bad("TORRENT_TRANSFER_OWNER_DENIED", "转移做种表单权限无效")
		}
		names := []string{}
		for _, d := range a.Settings().Downloaders {
			names = append(names, d.Name)
		}
		form, defaults, e := transfer.Form(names)
		if e != nil {
			return nil, e
		}
		return json.Marshal(map[string]any{"conf": form, "model": defaults})
	}
	if run == nil || a.requireDeferredExecution(ctx, run, method) != nil {
		return nil, bad("TORRENT_TRANSFER_DEFERRED_REQUIRED", "转移做种操作缺少可信执行授权")
	}
	expected, payload, e := transferDeferredSpec(*run)
	if e != nil || method != expected || !bytes.Equal(raw, payload) {
		return nil, bad("TORRENT_TRANSFER_RUN_DENIED", "转移做种操作与执行快照不符")
	}
	ctx = context.WithValue(ctx, transferAdmissionKey{}, transferAdmissionFromRun(*run, method))
	owner, e := a.transferOwner(ctx, in)
	if e != nil {
		return nil, e
	}
	held, ok := ctx.Value(transferDeferredOwnerKey{}).(transferDeferredOwner)
	call, _ := ctx.Value(pluginDeferredExecutionKey{}).(*pluginDeferredCall)
	if !ok || held.owner != owner || held.call == nil || held.call != call || call.a != a || call.run.ID != run.ID {
		return nil, bad("TORRENT_TRANSFER_DEFERRED_REQUIRED", "转移做种缺少当前实例串行执行授权")
	}
	a.pluginLifecycleMu.Lock()
	a.pluginMu.Lock()
	e = a.validatePluginHostRun(ctx, in, run, method)
	stored := PluginStoredValue{}
	if e == nil {
		stored, e = a.pluginStore.Config(ctx, in.InstanceID)
	}
	a.pluginMu.Unlock()
	if e != nil {
		a.pluginLifecycleMu.Unlock()
		return nil, e
	}
	a.downloadMu.Lock()
	settings := a.Settings()
	a.pluginLifecycleMu.Unlock()
	defer a.downloadMu.Unlock()
	if e = a.pluginStore.transferAcceptRun(ctx, *run); e != nil {
		return nil, e
	}
	if method == transfer.ConfigureCapability {
		return a.configureTransfer(ctx, owner, in, run, stored.Value, settings)
	}
	return a.executeTransfer(ctx, owner, in, run, raw, settings)
}
func (a *App) transferBindings(settings Settings) []transfer.NativeBinding {
	out := []transfer.NativeBinding{}
	for _, d := range settings.Downloaders {
		kind := d.Kind
		if kind == "qb" {
			kind = "qbittorrent"
		}
		if kind == "tr" {
			kind = "transmission"
		}
		out = append(out, transfer.NativeBinding{Service: transfer.Service{Ref: d.ID, Name: d.Name, Type: kind, Inactive: !downloaderEnabled(d)}, Config: native.Config{ID: d.ID, Kind: kind, BaseURL: d.BaseURL, Username: d.Username, Password: d.Password}, Options: native.IYUUOptions{APIKey: d.APIKey, AutoCategory: d.AutoCategory, Sequential: d.Sequential, FirstLast: d.FirstLast, Sleep: a.pluginTransfer.pollSleep}})
	}
	return out
}
func (a *App) transferJournalRun(ctx context.Context, owner *pluginTransferOwner, in PluginInstallation, run *PluginRun, initial transfer.RuntimeCheckpoint, method string) (*transfer.JournalRun, transfer.RuntimeCheckpoint, error) {
	identity := transfer.RunIdentity{Instance: run.InstanceID, Generation: uint64(run.Generation), Revision: uint64(run.ConfigRevision), Fingerprint: tokenHash(string(run.Input)), Run: run.ID}
	guard := func(ctx context.Context) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		return a.validatePluginHostRun(ctx, in, run, method)
	}
	if raw, exists, e := owner.journal.ReadRunInitial(ctx, identity); e != nil {
		return nil, initial, e
	} else if exists {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if e = d.Decode(&initial); e != nil {
			return nil, initial, e
		}
	}
	jr, e := owner.journal.Begin(ctx, identity, initial, guard)
	return jr, initial, e
}
func (a *App) transferHostFor(ctx context.Context, jr *transfer.JournalRun, in PluginInstallation, run *PluginRun, cfg transfer.Config, settings Settings, proofs []transfer.AuthorityGrant) (*transfer.NativeHost, error) {
	return transfer.NewNativeHost(transfer.NativeHostOptions{Context: ctx, Run: jr, Config: cfg, Bindings: a.transferBindings(settings), TempDir: filepath.Join(a.Dir, "plugin-state", "torrent-transfer", run.InstanceID, "generation-"+itoaSourceSite(run.Generation), run.ID), PendingProofs: proofs, NoticeAccepted: func(ctx context.Context, key string) error { return a.routeTransferNotice(ctx, jr.Identity(), key) }, Logger: a.transferSourceLogger(in, *run, settings)})
}
func (a *App) transferLocalCommits(jr *transfer.JournalRun, run *PluginRun, configuring bool) {
	jr.LocalCommit = func(ctx context.Context, tx *sql.Tx, id transfer.RunIdentity, op, key string, value any) (func(), error) {
		switch op {
		case "source-notice":
			return nil, a.acceptTransferNoticeTx(ctx, tx, id, key, value)
		case "source-system-message":
			m, ok := value.(map[string]any)
			if !ok {
				return nil, transfer.ErrReplayDiverged
			}
			title, _ := m["title"].(string)
			msg, _ := m["text"].(string)
			n := Notification{ID: "notice_" + a.pluginStore.noticeID("transfer-system", id, key), Title: cleanWithSettings(title, a.Settings()), Message: cleanWithSettings(msg, a.Settings()), CreatedAt: jr.Started.UTC().Format(time.RFC3339Nano)}
			return nil, putCleanupNoticeTx(ctx, tx, n)
		case "source-history":
			item := strings.TrimPrefix(key, "history/")
			var revision int64
			e := tx.QueryRowContext(ctx, "SELECT revision FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?", run.InstanceID, item).Scan(&revision)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			raw, e := json.Marshal(value)
			if e != nil {
				return nil, e
			}
			_, e = a.pluginStore.saveValueTx(ctx, tx, run.InstanceID, "data", item, revision, raw)
			return nil, e
		case "runtime-checkpoint":
			if configuring {
				return nil, nil
			}
			state, ok := value.(transfer.RuntimeCheckpoint)
			if !ok || state.Revision != uint64(run.ConfigRevision) {
				return nil, transfer.ErrReplayDiverged
			}
			result, e := transferSourceResult(*run, state)
			if e != nil {
				return nil, e
			}
			return nil, a.pluginStore.transferCompleteRunTx(ctx, tx, *run, result, run.ConfigRevision)
		}
		return nil, nil
	}
}
func (a *App) configureTransfer(ctx context.Context, owner *pluginTransferOwner, in PluginInstallation, run *PluginRun, raw []byte, settings Settings) (json.RawMessage, error) {
	input, e := transferSourceMap(raw)
	if e != nil {
		return nil, e
	}
	previous, found, e := owner.journal.ReadCheckpoint(ctx, in.InstanceID, uint64(in.Generation))
	if e != nil {
		return nil, e
	}
	initial := transfer.RuntimeCheckpoint{Core: transfer.State{Config: input, Recheck: map[string][]string{}}}
	if found {
		initial.Core.Recheck = previous.Core.Recheck
		initial.Core.RecheckRunning = previous.Core.RecheckRunning
		initial.AuthorityProofs = previous.AuthorityProofs
	}
	jr, initial, e := a.transferJournalRun(ctx, owner, in, run, initial, transfer.ConfigureCapability)
	if e != nil {
		return nil, e
	}
	a.transferLocalCommits(jr, run, true)
	cfg, e := transfer.RestoreConfig(initial.Core.Config)
	if e != nil {
		return nil, e
	}
	host, e := a.transferHostFor(ctx, jr, in, run, cfg, settings, nil)
	if e != nil {
		return nil, e
	}
	defer host.Close()
	cfg = transfer.NewConfig()
	init, e := cfg.Init(ctx, input, host)
	if e != nil {
		return a.transferConfigurationFailure(ctx, owner, in, run, jr, initial, cfg, e)
	}
	zone, e := transferScheduleTimezone()
	if e != nil {
		return a.transferConfigurationFailure(ctx, owner, in, run, jr, initial, cfg, e)
	}
	services, e := cfg.Services(zone)
	if e != nil {
		return a.transferConfigurationFailure(ctx, owner, in, run, jr, initial, cfg, e)
	}
	jobs := append(append([]transfer.Job{}, init.Jobs...), services...)
	at := jr.Started
	defs := []pluginScheduleDefinition{}
	for _, job := range jobs {
		handler := job.ID
		if job.Kind == "cron" {
			handler = "transfer"
		}
		name := job.Name
		if name == "" {
			name = job.ID
		}
		trigger := schedule.Spec{Kind: job.Kind, Timezone: zone}
		switch job.Kind {
		case "date":
			trigger.RunAt = schedule.FormatTime(at.Add(job.Delay))
		case "interval":
			trigger.IntervalMS = job.Interval.Milliseconds()
		case "cron":
			trigger.Cron = job.Cron
		}
		defs = append(defs, pluginScheduleDefinition{ID: in.PluginID + "." + job.ID, Name: name, Handler: handler, Trigger: trigger})
	}
	return a.publishTransferConfiguration(ctx, owner, in, run, jr, initial, cfg, defs)
}
func (a *App) publishTransferConfiguration(ctx context.Context, owner *pluginTransferOwner, in PluginInstallation, run *PluginRun, jr *transfer.JournalRun, initial transfer.RuntimeCheckpoint, cfg transfer.Config, defs []pluginScheduleDefinition) (json.RawMessage, error) {
	snapshot, e := cfg.Snapshot()
	if e != nil {
		return nil, e
	}
	patch := map[string]json.RawMessage{}
	for k, v := range snapshot {
		patch[k], e = json.Marshal(v)
		if e != nil {
			return nil, e
		}
	}
	initial.Core.Config = snapshot
	effect := func(ctx context.Context, tx *sql.Tx, current PluginRun) error {
		initial.Revision = uint64(current.ConfigRevision + 1)
		initial.Run = current.ID
		initial.Identity = transfer.RunIdentity{Instance: current.InstanceID, Generation: uint64(current.Generation), Revision: uint64(current.ConfigRevision), Fingerprint: tokenHash(string(current.Input)), Run: current.ID}
		result, e := transferSourceResult(current, initial)
		if e != nil {
			return e
		}
		if e = a.pluginStore.transferCompleteRunTx(ctx, tx, current, result, int64(initial.Revision)); e != nil {
			return e
		}
		return owner.journal.WriteCheckpointTx(ctx, tx, current.InstanceID, uint64(current.Generation), initial)
	}
	if e = a.pluginStore.configurePluginSchedulesWithEffect(ctx, run, "torrent_transfer", defs, jr.Started, patch, effect); e != nil {
		return nil, e
	}
	return transferSourceResult(*run, initial)
}
func (a *App) executeTransfer(ctx context.Context, owner *pluginTransferOwner, in PluginInstallation, run *PluginRun, raw json.RawMessage, settings Settings) (json.RawMessage, error) {
	cp, found, e := owner.journal.ReadCheckpoint(ctx, in.InstanceID, uint64(in.Generation))
	if e != nil {
		return nil, e
	}
	if !found || cp.Revision != uint64(run.ConfigRevision) {
		return nil, bad("TORRENT_TRANSFER_INITIALIZATION_REQUIRED", "转移做种当前配置尚未初始化")
	}
	handler := "transfer"
	if run.Action == "service" || run.Action == "once" {
		var input struct {
			ID string `json:"occurrence_id"`
		}
		if !pluginStrictJSON(raw, &input) || input.ID == "" {
			return nil, bad("TORRENT_TRANSFER_OCCURRENCE_DENIED", "转移任务没有绑定持久计划")
		}
		def, e := a.pluginStore.pluginScheduledInvocation(ctx, *run, input.ID)
		if e != nil {
			return nil, e
		}
		if !strings.HasPrefix(def.ID, in.PluginID+".") || (def.Handler != "transfer" && def.Handler != "check_recheck") {
			return nil, bad("TORRENT_TRANSFER_OCCURRENCE_DENIED", "转移任务计划不匹配")
		}
		handler = def.Handler
	} else if run.Action != "run" {
		return nil, bad("TORRENT_TRANSFER_ACTION_DENIED", "转移任务来源无效")
	}
	jr, cp, e := a.transferJournalRun(ctx, owner, in, run, cp, transfer.ExecuteCapability)
	if e != nil {
		return nil, e
	}
	a.transferLocalCommits(jr, run, false)
	cfg, e := transfer.RestoreConfig(cp.Core.Config)
	if e != nil {
		return nil, e
	}
	host, e := a.transferHostFor(ctx, jr, in, run, cfg, settings, cp.AuthorityProofs)
	if e != nil {
		return nil, e
	}
	defer host.Close()
	core, e := transfer.RestoreCore(cp.Core, host)
	if e != nil {
		return nil, e
	}
	if handler == "transfer" {
		cp.Counts, e = core.Transfer(ctx)
	} else {
		e = core.CheckRecheck(ctx)
	}
	if e != nil {
		cause := e
		finish, cancel, known := a.transferKnownFailureContext(ctx, owner, *run, cause)
		if !known {
			return nil, cause
		}
		defer cancel()
		cp.Core, e = core.Snapshot()
		if e != nil {
			return nil, e
		}
		cp.AuthorityProofs, e = host.PendingProofs(cp.Core.Recheck)
		if e != nil {
			return nil, e
		}
		cp.SourceError = transferSourceFailed
		cp.Identity = jr.Identity()
		cp.Revision = uint64(run.ConfigRevision)
		if e = jr.Checkpoint(finish, cp); e != nil {
			return nil, e
		}
		result, e := transferSourceResult(*run, cp)
		if e != nil {
			return nil, e
		}
		return result, bad(transferSourceFailed, "转移做种源流程失败，已保留确定结果")
	}
	cp.SourceError = ""
	cp.Core, e = core.Snapshot()
	if e != nil {
		return nil, e
	}
	cp.AuthorityProofs, e = host.PendingProofs(cp.Core.Recheck)
	if e != nil {
		return nil, e
	}
	cp.Identity = jr.Identity()
	cp.Revision = uint64(run.ConfigRevision)
	if e = jr.Checkpoint(ctx, cp); e != nil {
		return nil, e
	}
	if a.pluginTransfer.afterCheckpoint != nil {
		if e = a.pluginTransfer.afterCheckpoint(); e != nil {
			return nil, e
		}
	}
	return transferSourceResult(*run, cp)
}
