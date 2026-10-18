package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins"
	ab "light-media/internal/plugins/autobackup"
	"light-media/internal/plugins/schedule"
)

// Candidate native owner. The SDK can only submit a bound terminal handshake;
// private state and effects live in the actual App SQLite database.
const autoBackupSchema = `CREATE TABLE IF NOT EXISTS plugin_auto_backup_state(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));`
const autoBackupBlockedByUnknown = "AUTO_BACKUP_BLOCKED_BY_UNKNOWN"

func autoBackupBlockedResult() json.RawMessage {
	return json.RawMessage(`{"version":1,"decision":"blocked_by_unknown","effects_started":false}`)
}

type pluginAutoBackupRuntime struct {
	mu                   sync.Mutex
	owners               map[string]*pluginAutoBackupOwner
	now                  func() time.Time
	afterEffect          func(string) error
	beforeRetentionLeaf  func(string)
	beforeOwnerAdmission func(context.Context, PluginRun)
	client               *http.Client
}
type pluginAutoBackupOwner struct {
	a             *App
	instance      string
	generation    int64
	timezone      string
	timezoneError bool
	gate          chan struct{}
	machine       *ab.Plugin
	active        *autoBackupCall
}
type autoBackupEffect struct {
	Kind, Phase  string
	Args, Result json.RawMessage
}
type autoBackupAction struct {
	RunID, Action, InputSHA, Method, Phase, ErrorCode string
	Revision                                          int64
	Result                                            json.RawMessage
	Effects                                           []autoBackupEffect
	Cut                                               *autoBackupSnapshotCut
	Resolution                                        *autoBackupResolution
}
type autoBackupState struct {
	Version                     int
	Instance                    string
	Generation, AppliedRevision int64
	Config                      json.RawMessage
	Runs                        map[string]autoBackupAction
}
type autoBackupCall struct {
	o         *pluginAutoBackupOwner
	run       PluginRun
	method    string
	uncertain bool
	pins      map[string]autoBackupPin
	config    ab.Object
	scheduler *autoBackupScheduler
	cleared   ab.Object
}
type autoBackupPin struct {
	Dev, Ino uint64
	Mode     uint32
	Size     int64
	Mod      int64
}

func (p *PluginStore) isAutoBackupRun(ctx context.Context, tx *sql.Tx, r PluginRun) bool {
	if r.Version != "2.1.4" {
		return false
	}
	var plugin string
	return tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, r.InstanceID).Scan(&plugin) == nil && plugin == "AutoBackup"
}
func autoBackupConfig(raw json.RawMessage) (ab.Object, error) {
	var v ab.Object
	if cleanupJournalStrict(raw, &v) != nil || v == nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "自动备份配置必须为对象")
	}
	return v, nil
}
func autoBackupKey(g int64) string { return "auto-backup:v1:" + strconv.FormatInt(g, 10) }
func (p *PluginStore) autoBackupRead(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, generation int64) (autoBackupState, []byte, error) {
	if db, ok := q.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return autoBackupState{}, nil, err
		}
		defer tx.Rollback()
		s, blob, err := p.autoBackupRead(ctx, tx, instance, generation)
		if err != nil {
			return s, nil, err
		}
		if err = tx.Commit(); err != nil {
			return s, nil, err
		}
		return s, blob, nil
	}
	s := autoBackupState{Version: 1, Instance: instance, Generation: generation, Runs: map[string]autoBackupAction{}}
	var sealed []byte
	err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_auto_backup_state WHERE instance_id=? AND generation=?`, instance, generation).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil, autoBackupEvidenceError()
	}
	if err != nil {
		return s, nil, err
	}
	raw, err := p.unseal("run_result", instance, autoBackupKey(generation), sealed)
	if err != nil {
		return s, nil, autoBackupEvidenceError()
	}
	if cleanupJournalStrict(raw, &s) != nil || s.Version != 1 || s.Instance != instance || s.Generation != generation || s.Runs == nil {
		return s, nil, bad("AUTO_BACKUP_JOURNAL_INVALID", "备份执行记录无法校验")
	}
	if err = p.autoBackupValidateRootChain(ctx, q, s, raw); err != nil {
		return s, nil, err
	}
	for key, r := range s.Runs {
		if r.ErrorCode == autoBackupBlockedByUnknown && (r.Phase != "failed" || len(r.Effects) != 0 || !bytes.Equal(r.Result, autoBackupBlockedResult())) {
			return s, nil, bad("AUTO_BACKUP_JOURNAL_INVALID", "备份零效果阻止记录无法校验")
		}
		if r.Phase == "abandoned_unknown" && !validAutoBackupResolution(r, s.Instance, s.Generation) {
			return s, nil, bad("AUTO_BACKUP_JOURNAL_INVALID", "备份放弃记录无效")
		}
		if r.Phase == "snapshot_cut" && !validAutoBackupCut(r, s.Instance, s.Generation) {
			return s, nil, bad("AUTO_BACKUP_JOURNAL_INVALID", "备份快照断点无效")
		}
		if key != r.RunID || !pluginStorageID.MatchString(key) || r.Revision < 1 || !pluginStorageDigest.MatchString(r.InputSHA) {
			return s, nil, bad("AUTO_BACKUP_JOURNAL_INVALID", "备份执行身份无效")
		}
		switch r.Phase {
		case "started", "confirmed", "failed", "snapshot_cut", "abandoned_unknown":
		default:
			return s, nil, bad("AUTO_BACKUP_JOURNAL_INVALID", "备份执行阶段无效")
		}
		for _, e := range r.Effects {
			if (e.Phase != "started" && e.Phase != "confirmed") || e.Kind == "" || !json.Valid(e.Args) {
				return s, nil, bad("AUTO_BACKUP_JOURNAL_INVALID", "备份副作用阶段无效")
			}
		}
	}
	if err = p.autoBackupValidateDeferred(ctx, q, s); err != nil {
		return s, nil, err
	}
	return s, sealed, nil
}
func (p *PluginStore) autoBackupSave(ctx context.Context, tx *sql.Tx, s autoBackupState, previous []byte) error {
	raw, err := json.Marshal(s)
	if err != nil || len(raw) > 16<<20 {
		return bad("AUTO_BACKUP_JOURNAL_LIMIT", "备份执行记录超出容量")
	}
	if previous == nil {
		return autoBackupEvidenceError()
	}
	root, rootBlob, err := p.autoBackupRootRead(ctx, tx, s.Instance, s.Generation)
	if err != nil {
		return err
	}
	old, err := p.unseal("run_result", s.Instance, autoBackupKey(s.Generation), previous)
	if err != nil || root.StateSHA != tokenHash(string(old)) {
		return autoBackupEvidenceError()
	}
	sealed, err := p.seal("run_result", s.Instance, autoBackupKey(s.Generation), raw)
	if err != nil {
		return err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE plugin_auto_backup_state SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?`, sealed, s.Instance, s.Generation, previous)
	if err != nil {
		return err
	}
	if n, err := changed.RowsAffected(); err != nil || n != 1 {
		return bad("AUTO_BACKUP_JOURNAL_CONFLICT", "备份执行记录已变化")
	}
	root.Sequence++
	root.StateSHA = tokenHash(string(raw))
	sealed, err = p.seal("run_result", s.Instance, autoBackupRootKey(s.Generation), mustCleanSeedJSON(root))
	if err != nil {
		return err
	}
	changed, err = tx.ExecContext(ctx, `UPDATE plugin_auto_backup_roots SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?`, sealed, s.Instance, s.Generation, rootBlob)
	if err != nil {
		return err
	}
	if n, err := changed.RowsAffected(); err != nil || n != 1 {
		return autoBackupEvidenceError()
	}
	return nil
}

func (o *pluginAutoBackupOwner) update(ctx context.Context, fn func(*autoBackupState) error) error {
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, b, err := o.a.pluginStore.autoBackupRead(ctx, tx, o.instance, o.generation)
	if err == nil {
		err = fn(&s)
	}
	if err == nil {
		err = o.a.pluginStore.autoBackupSave(ctx, tx, s, b)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func autoBackupNoUnknown(s autoBackupState) error {
	for _, r := range s.Runs {
		if r.Phase == "abandoned_unknown" && validAutoBackupResolution(r, s.Instance, s.Generation) {
			continue
		}
		if r.Phase == "snapshot_cut" && validAutoBackupCut(r, s.Instance, s.Generation) {
			continue
		}
		if r.Phase == "started" {
			return bad("AUTO_BACKUP_UNCERTAIN", "备份存在待核对副作用，未重复执行")
		}
		for _, e := range r.Effects {
			if e.Phase != "confirmed" {
				return bad("AUTO_BACKUP_UNCERTAIN", "备份存在待核对副作用，未重复执行")
			}
		}
	}
	return nil
}
func (a *App) autoBackupOwner(ctx context.Context, i PluginInstallation) (*pluginAutoBackupOwner, error) {
	if i.PluginID != "AutoBackup" || i.Version != "2.1.4" || !i.Installed || i.Generation < 1 {
		return nil, bad("AUTO_BACKUP_SCOPE_DENIED", "自动备份安装身份不匹配")
	}
	r := &a.pluginAutoBackup
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owners == nil {
		r.owners = map[string]*pluginAutoBackupOwner{}
	}
	key := i.InstanceID + ":" + strconv.FormatInt(i.Generation, 10)
	if o := r.owners[key]; o != nil {
		return o, nil
	}
	if _, err := a.Store.DB.ExecContext(ctx, autoBackupSchema); err != nil {
		return nil, err
	}
	zone, err := autoBackupHostTimezone()
	invalidZone := err != nil
	if invalidZone {
		zone = "Etc/UTC"
	}
	o := &pluginAutoBackupOwner{timezone: zone, timezoneError: invalidZone, a: a, instance: i.InstanceID, generation: i.Generation, gate: make(chan struct{}, 1)}
	r.owners[key] = o
	return o, nil
}
func (o *pluginAutoBackupOwner) lock(ctx context.Context) error {
	select {
	case o.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *pluginAutoBackupOwner) unlock() { <-o.gate }
func autoBackupDeferredSpec(run PluginRun) (string, json.RawMessage, error) {
	var inv plugins.Invocation
	if cleanupJournalStrict(run.Input, &inv) != nil || inv.Action != run.Action {
		return "", nil, bad("INVALID_PLUGIN_RUN", "备份执行输入无效")
	}
	switch run.Action {
	case "init", "reload":
		return ab.ConfigureCapability, json.RawMessage(`{}`), nil
	case "run", "backup":
		if len(inv.Input) != 0 && string(inv.Input) != "{}" && string(inv.Input) != "null" {
			break
		}
		return ab.ExecuteCapability, json.RawMessage(`{}`), nil
	case "service", "once":
		var token struct {
			ID string `json:"event_id"`
		}
		if cleanupJournalStrict(inv.Input, &token) != nil || !pluginStorageID.MatchString(token.ID) {
			break
		}
		return ab.ExecuteCapability, inv.Input, nil
	case "api":
		var request plugins.HTTPRequest
		if cleanupJournalStrict(inv.Input, &request) != nil || request.RequestID == "" || len(request.RequestID) > 128 {
			break
		}
		raw, _ := json.Marshal(map[string]string{"request_id": request.RequestID})
		return ab.APICapability, raw, nil
	}
	return "", nil, bad("AUTO_BACKUP_SCOPE_DENIED", "备份执行没有绑定宿主来源")
}
func (a *App) shouldInitializeAutoBackup(ctx context.Context, i PluginInstallation, c PluginStoredValue) (bool, error) {
	if _, err := a.autoBackupOwner(ctx, i); err != nil {
		return false, err
	}
	s, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, i.InstanceID, i.Generation)
	if err != nil {
		return false, err
	}
	if err = autoBackupNoUnknown(s); err != nil {
		return false, err
	}
	return s.AppliedRevision != c.Revision, nil
}
func (a *App) pluginAutoBackupHost(ctx context.Context, i PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method == ab.FormCapability {
		if run != nil || string(raw) != "{}" || i.PluginID != "AutoBackup" || i.Version != "2.1.4" {
			return nil, bad("AUTO_BACKUP_SCOPE_DENIED", "备份表单身份无效")
		}
		manifest, _ := json.Marshal(ab.Manifest())
		current, e := a.pluginStore.Installation(ctx, i.InstanceID)
		preview := i.InstanceID == i.PluginID && i.Generation == 0 && !i.Installed && bytes.Equal(i.Manifest, manifest)
		if (!preview && (e != nil || current.PluginID != "AutoBackup" || !current.Installed || current.Generation != i.Generation)) || (preview && !errors.Is(e, sql.ErrNoRows) && (e != nil || current.Installed)) {
			return nil, bad("AUTO_BACKUP_SCOPE_DENIED", "备份表单安装已变化")
		}
		formRaw, e := ab.Form(filepath.Join(a.Dir, "plugin-private", "AutoBackup", "backups"))
		if e != nil {
			return nil, e
		}
		var form struct {
			Schema   json.RawMessage `json:"schema"`
			Defaults json.RawMessage `json:"defaults"`
		}
		if json.Unmarshal(formRaw, &form) != nil {
			return nil, bad("AUTO_BACKUP_FORM_INVALID", "备份表单无效")
		}
		model := ab.New(ab.Dependencies{}).Config()
		var defaults ab.Object
		if json.Unmarshal(form.Defaults, &defaults) != nil {
			return nil, bad("AUTO_BACKUP_FORM_INVALID", "备份表单默认配置无效")
		}
		for key, value := range defaults {
			model[key] = value
		}
		rawModel, e := json.Marshal(model)
		if e != nil {
			return nil, e
		}
		return json.Marshal(map[string]json.RawMessage{"conf": form.Schema, "model": rawModel})
	}
	if run == nil {
		return nil, bad("AUTO_BACKUP_SCOPE_DENIED", "备份缺少执行身份")
	}
	want, payload, err := autoBackupDeferredSpec(*run)
	if err != nil || want != method || !deferredPayloadEqual(raw, payload) {
		return nil, bad("AUTO_BACKUP_SCOPE_DENIED", "备份操作身份不一致")
	}
	if err = a.requireDeferredExecution(ctx, run, method); err != nil {
		return nil, err
	}
	o, err := a.autoBackupOwner(ctx, i)
	if err != nil {
		return nil, err
	}
	if err = o.lock(ctx); err != nil {
		return nil, err
	}
	defer o.unlock()
	// Lifecycle mutations acquire this gate first, so a new owner cannot run on
	// a superseded config. This short recheck holds no lock across filesystem/I/O.
	if err = a.validatePluginHostRun(ctx, i, run, method); err != nil {
		return nil, err
	}
	if hook := a.pluginAutoBackup.beforeOwnerAdmission; hook != nil {
		hook(ctx, *run)
	}
	ctx, err = a.autoBackupAuthorizedAdmission(ctx, *run, method)
	if err != nil {
		return nil, err
	}
	c, err := a.pluginStore.Config(ctx, i.InstanceID)
	if err != nil {
		return nil, err
	}
	config, err := autoBackupConfig(c.Value)
	if err != nil {
		return nil, err
	}
	call := &autoBackupCall{o: o, run: *run, method: method, pins: map[string]autoBackupPin{}, config: config}
	call.run.Input = append(json.RawMessage(nil), run.Input...)
	o.active = call
	defer func() { o.active = nil }()
	var blocked error
	err = o.update(ctx, func(s *autoBackupState) error {
		blocked = autoBackupNoUnknown(*s)
		if _, ok := s.Runs[run.ID]; ok {
			return bad("AUTO_BACKUP_REPLAY_DENIED", "备份执行已经进入宿主")
		}
		action := autoBackupAction{RunID: run.ID, Action: run.Action, Revision: run.ConfigRevision, InputSHA: deferredInputSHA(run.Input), Method: method, Phase: "started"}
		if blocked != nil {
			// This is before any file, network, configuration or scheduler effect.
			// Preserve the denied admission as a known terminal result while the
			// original unknown operation and its effects remain untouched.
			action.Phase, action.ErrorCode, action.Result = "failed", autoBackupBlockedByUnknown, autoBackupBlockedResult()
		}
		s.Runs[run.ID] = action
		return nil
	})
	if err != nil {
		return nil, err
	}
	if blocked != nil {
		return nil, bad(autoBackupBlockedByUnknown, "已有备份结果待核对，此次执行未开始文件或远程操作")
	}
	if run.Action == "api" {
		if err = a.validatePluginHTTPRun(ctx, *run); err != nil {
			return nil, err
		}
	}
	if run.Action == "once" || run.Action == "service" {
		if err = a.pluginStore.validateAutoBackupOccurrence(ctx, *run); err != nil {
			return nil, err
		}
	}
	result, sourceErr := call.execute(ctx)
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if call.uncertain {
		a.pluginExecutionLog(i, call.run, "ERROR", "backup_effect_uncertain", "自动备份结果未能确认；请在插件执行记录查看具体阶段，核对后可确认放弃旧执行", "AUTO_BACKUP_UNCERTAIN")
		return nil, bad("AUTO_BACKUP_UNCERTAIN", "备份副作用结果尚未确认")
	}
	if sourceErr != nil {
		a.pluginExecutionLog(i, call.run, "ERROR", "backup_source_failed", fmt.Sprintf("自动备份失败（%v）；已保留确认过的操作，请在执行记录与日志中检查", ab.ErrorType(sourceErr)), "AUTO_BACKUP_SOURCE_FAILED")
	}
	err = o.update(finish, func(s *autoBackupState) error {
		r := s.Runs[run.ID]
		for _, e := range r.Effects {
			if e.Phase != "confirmed" {
				return bad("AUTO_BACKUP_UNCERTAIN", "备份副作用收据缺失")
			}
		}
		r.Phase = "confirmed"
		if sourceErr != nil {

			r.Phase = "failed"
			r.ErrorCode = "AUTO_BACKUP_SOURCE_FAILED"
			result, _ = json.Marshal(map[string]any{"source_failed": true, "error_type": ab.ErrorType(sourceErr)})
		}
		r.Result = result
		s.Runs[run.ID] = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, sourceErr
}
func (c *autoBackupCall) effect(ctx context.Context, kind string, args any, apply func() (any, error)) (any, error) {
	if c.run.Action == "api" {
		if err := c.o.a.validatePluginHTTPRun(ctx, c.run); err != nil {
			return nil, err
		}
	}
	var n int
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	if err = c.o.update(ctx, func(s *autoBackupState) error {
		r := s.Runs[c.run.ID]
		if r.Phase != "started" {
			return bad("AUTO_BACKUP_SCOPE_DENIED", "备份执行已结束")
		}
		n = len(r.Effects)
		r.Effects = append(r.Effects, autoBackupEffect{Kind: kind, Phase: "started", Args: raw})
		s.Runs[c.run.ID] = r
		return nil
	}); err != nil {
		c.uncertain = true
		return nil, err
	}
	value, err := apply()
	if err != nil {
		c.uncertain = true
		return nil, err
	}
	if c.o.a.pluginAutoBackup.afterEffect != nil {
		if e := c.o.a.pluginAutoBackup.afterEffect(kind); e != nil {
			c.uncertain = true
			return nil, e
		}
	}
	result, err := json.Marshal(value)
	if err != nil {
		c.uncertain = true
		return nil, err
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	err = c.o.update(finish, func(s *autoBackupState) error {
		r := s.Runs[c.run.ID]
		if n >= len(r.Effects) || r.Effects[n].Phase != "started" || r.Effects[n].Kind != kind || !bytes.Equal(r.Effects[n].Args, raw) {
			return bad("AUTO_BACKUP_JOURNAL_CONFLICT", "备份副作用身份已变化")
		}
		r.Effects[n].Phase = "confirmed"
		r.Effects[n].Result = result
		s.Runs[c.run.ID] = r
		return nil
	})
	if err != nil {
		c.uncertain = true
		return nil, err
	}
	return value, nil
}
func (p *PluginStore) autoBackupOutcome(ctx context.Context, run PluginRun, method string) (json.RawMessage, string, bool, error) {
	var exists int
	if err := p.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='plugin_auto_backup_state' AND type='table'`).Scan(&exists); err != nil || exists == 0 {
		return nil, "", false, err
	}
	s, _, err := p.autoBackupRead(ctx, p.store.DB, run.InstanceID, run.Generation)
	if err != nil {
		return nil, "", false, err
	}
	r, ok := s.Runs[run.ID]
	if !ok {
		return nil, "", false, nil
	}
	if r.RunID != run.ID || r.Action != run.Action || r.Revision != run.ConfigRevision || r.InputSHA != deferredInputSHA(run.Input) || r.Method != method {
		return nil, "", false, bad("AUTO_BACKUP_JOURNAL_INVALID", "备份历史输入不匹配")
	}
	if (r.Phase != "confirmed" && r.Phase != "failed") || !json.Valid(r.Result) {
		return nil, "", false, nil
	}
	for _, e := range r.Effects {
		if e.Phase != "confirmed" {
			return nil, "", false, nil
		}
		if e.Kind == "notice" {
			var proof struct {
				RouteID    string `json:"route_id"`
				InitialSHA string `json:"initial_sha"`
			}
			if cleanupJournalStrict(e.Result, &proof) != nil {
				return nil, "", false, bad("AUTO_BACKUP_JOURNAL_INVALID", "备份通知收据无效")
			}
			route, _, err := p.readNoticeRoute(ctx, p.store.DB, proof.RouteID)
			if err != nil {
				return nil, "", false, err
			}
			if !route.Complete {
				return nil, "", false, nil
			}
		}
	}
	return r.Result, r.ErrorCode, true, nil
}
func (c *autoBackupCall) execute(ctx context.Context) (json.RawMessage, error) {
	if c.o.timezoneError {
		return nil, &ab.SourceError{Type: "UnknownTimeZoneError", Operation: "host TZ"}
	}
	defs := []pluginScheduleDefinition{}
	o := c.o
	c.scheduler = &autoBackupScheduler{defs: &defs, timezone: c.o.timezone}
	deps := ab.Dependencies{Now: func() time.Time { return o.active.now() }, Timezone: c.o.timezone, DataPath: func() string { return o.active.defaultPath() },
		Log: func(ctx context.Context, level, msg string) {
			o.a.pluginExecutionLog(PluginInstallation{PluginID: "AutoBackup", InstanceID: o.active.run.InstanceID}, o.active.run, strings.ToUpper(level), "backup_source", cleanWithSettings(msg, o.a.Settings()), "")
		},
		BackupFile: func(ctx context.Context, d string) (*string, error) { return o.active.archive(ctx, d) }, ListBackups: func(ctx context.Context, d string) ([]ab.Entry, error) {
			list, err := (ab.Filesystem{Roots: o.active.writeRoots()}).ListBackups(ctx, d)
			if err == nil {
				for _, entry := range list {
					pin, e := autoBackupFilePin(entry.Path)
					if e != nil {
						return nil, e
					}
					o.active.pins[entry.Path] = pin
				}
			}
			return list, err
		},
		RemoveFile: func(ctx context.Context, p string) error { return o.active.remove(ctx, p, false) }, RemoveTree: func(ctx context.Context, p string) error { return o.active.remove(ctx, p, true) },
		NewDAV: func(ctx context.Context, options ab.Object) (ab.DAV, error) {
			native, err := ab.NewHTTPDAV(options, o.a.pluginAutoBackup.client, o.active.writeRoots())
			if err != nil {
				return nil, err
			}
			return &autoBackupDAV{o: o, native: native}, nil
		},
		NewScheduler: func(context.Context, string) (ab.Scheduler, error) { return o.active.scheduler, nil }, SaveConfig: func(_ context.Context, v ab.Object) error { o.active.cleared = v; return nil }, PostMessage: func(ctx context.Context, v ab.Object) error { return o.active.notice(ctx, v) }}
	machine := o.machine
	if machine == nil {
		machine = ab.New(deps)
		state, _, e := o.a.pluginStore.autoBackupRead(ctx, o.a.Store.DB, o.instance, o.generation)
		if e != nil {
			return nil, e
		}
		if len(state.Config) != 0 && string(state.Config) != "null" {
			warm, e := autoBackupConfig(state.Config)
			if e != nil {
				return nil, e
			}
			warm["onlyonce"] = false
			if e = machine.Init(ctx, warm); e != nil {
				return nil, e
			}
		}
		o.machine = machine
	}
	if c.run.Action == "init" || c.run.Action == "reload" {
		if err := machine.Init(ctx, c.config); err != nil {
			return nil, err
		}
	}
	if c.run.Action == "init" || c.run.Action == "reload" {
		trigger, err := machine.ServiceTrigger()
		if err != nil {
			return nil, err
		}
		if trigger != nil {
			defs = append(defs, pluginScheduleDefinition{ID: "AutoBackup", Name: "自动备份定时服务", Handler: "auto_backup", Trigger: *trigger, MisfireGraceMS: 1000})
		}
		runtimeConfig, err := json.Marshal(machine.Config())
		if err != nil {
			return nil, err
		}
		updated := c.run
		err = c.o.a.pluginStore.configurePluginSchedulesWithEffect(ctx, &updated, "auto_backup", defs, c.now(), nil, func(ctx context.Context, tx *sql.Tx, _ PluginRun) error {
			s, b, err := c.o.a.pluginStore.autoBackupRead(ctx, tx, c.o.instance, c.o.generation)
			if err != nil {
				return err
			}
			revision := c.run.ConfigRevision
			if c.cleared != nil {
				raw, e := json.Marshal(c.cleared)
				if e != nil {
					return e
				}
				value, e := c.o.a.pluginStore.saveValueTx(ctx, tx, c.o.instance, "config", "main", revision, raw)
				if e != nil {
					return e
				}
				revision = value.Revision
				if _, e = tx.ExecContext(ctx, `UPDATE plugin_schedules SET config_revision=? WHERE instance_id=? AND generation=? AND group_key='auto_backup'`, revision, c.o.instance, c.o.generation); e != nil {
					return e
				}
			}
			s.AppliedRevision = revision
			s.Config = runtimeConfig
			action := s.Runs[c.run.ID]
			action.Phase = "confirmed"
			action.Result = json.RawMessage(`{"configured":true}`)
			s.Runs[c.run.ID] = action
			return c.o.a.pluginStore.autoBackupSave(ctx, tx, s, b)
		})
		if err != nil {
			return nil, err
		}
		if o.a.pluginAutoBackup.afterEffect != nil {
			if err := o.a.pluginAutoBackup.afterEffect("configure_commit"); err != nil {
				c.uncertain = true
				return nil, err
			}
		}
		return json.RawMessage(`{"configured":true}`), nil
	}
	if c.run.Action == "api" {
		var req plugins.HTTPRequest
		var inv plugins.Invocation
		_ = json.Unmarshal(c.run.Input, &inv)
		_ = json.Unmarshal(inv.Input, &req)
		var value any
		var err error
		if !req.CredentialMatches["apikey"] {
			value, err = machine.APIBackup(ctx, "wrong-owned-boolean", "expected-owned-boolean")
		} else {
			value, err = machine.APIBackup(ctx, "approved", "approved")
		}
		if err != nil {
			return nil, err
		}
		body, _ := json.Marshal(value)
		return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body})
	}
	result, err := machine.BackupResponse(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
func (c *autoBackupCall) now() time.Time {
	if c.o.a.pluginAutoBackup.now != nil {
		return c.o.a.pluginAutoBackup.now()
	}
	return time.Now()
}
func (c *autoBackupCall) defaultPath() string {
	return filepath.Join(c.o.a.Dir, "plugin-private", "AutoBackup", "backups")
}
func (c *autoBackupCall) writeRoots() []string {
	return append([]string{c.defaultPath()}, c.o.a.Settings().AllowedRoots...)
}

type autoBackupScheduler struct {
	defs     *[]pluginScheduleDefinition
	running  bool
	timezone string
}

func (s *autoBackupScheduler) AddDate(_ context.Context, t time.Time, _ string) error {
	*s.defs = append(*s.defs, pluginScheduleDefinition{ID: "AutoBackup.once", Name: "自动备份", Handler: "auto_backup", Trigger: schedule.Spec{Kind: "date", RunAt: schedule.FormatTime(t), Timezone: s.timezone}, MisfireGraceMS: 1000})
	return nil
}
func (s *autoBackupScheduler) Jobs(context.Context) (int, error) { return len(*s.defs), nil }
func (s *autoBackupScheduler) Print(context.Context) error       { return nil }
func (s *autoBackupScheduler) Start(context.Context) error       { s.running = true; return nil }
func (s *autoBackupScheduler) Clear(context.Context) error       { *s.defs = nil; return nil }
func (s *autoBackupScheduler) Running() bool                     { return s.running }
func (s *autoBackupScheduler) Shutdown(context.Context) error    { s.running = false; return nil }

func (a *App) autoBackupLifecycleRequest(ctx context.Context, path, method string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !(parts[1] == "config" && method == "PUT" || (parts[1] == "reload" || parts[1] == "reset" || parts[1] == "uninstall") && method == "POST") {
		return false
	}
	i, err := a.pluginStore.Installation(ctx, parts[0])
	return err == nil && i.PluginID == "AutoBackup" && i.Version == "2.1.4"
}
func (a *App) pluginAutoBackupLifecycleHTTP(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	parts := strings.Split(path, "/")
	ctx := r.Context()
	i, err := a.pluginStore.Installation(ctx, parts[0])
	if err != nil {
		return nil, err
	}
	o, err := a.autoBackupOwner(ctx, i)
	if err != nil {
		return nil, err
	}
	if err = o.lock(ctx); err != nil {
		return nil, err
	}
	defer o.unlock()
	s, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, o.instance, o.generation)
	if err != nil {
		return nil, err
	}
	if err = autoBackupNoUnknown(s); err != nil {
		return nil, err
	}
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	switch parts[1] {
	case "config":
		var body struct {
			Revision int64           `json:"revision"`
			Config   json.RawMessage `json:"config"`
		}
		if err = decodePluginBody(w, r, &body); err != nil {
			return nil, err
		}
		value, e := autoBackupConfig(body.Config)
		if e != nil {
			return nil, e
		}
		v, run, e := a.pluginStore.SaveConfigAndQueue(ctx, i.InstanceID, body.Revision, value)
		if e == nil {
			a.wakePlugins()
		}
		return map[string]any{"revision": v.Revision, "run": run}, e
	case "reload":
		run, e := a.pluginStore.EnqueueRun(ctx, i.InstanceID, "reload", nil)
		if e == nil {
			a.wakePlugins()
		}
		return map[string]any{"run": run}, e
	case "reset":
		e := a.pluginStore.Reset(ctx, i.InstanceID)
		return map[string]bool{"reset": e == nil}, e
	case "uninstall":
		e := a.pluginStore.Uninstall(ctx, i.InstanceID)
		return map[string]bool{"uninstalled": e == nil, "data_retained": true}, e
	}
	return nil, fmt.Errorf("invalid lifecycle operation")
}

// Bulk PluginUnInstall shares the same gate ordering as direct HTTP lifecycle.
// No pending transition can outlive a crash: before SQL this is only an in-memory
// reservation, and actual backup actions are already durable and quiescent.
type pluginAutoBackupPrepared struct {
	o            *pluginAutoBackupOwner
	fromRevision int64
}

func (a *App) prepareAutoBackupTransition(ctx context.Context, i PluginInstallation) (*pluginAutoBackupPrepared, error) {
	o, e := a.autoBackupOwner(ctx, i)
	if e != nil {
		return nil, e
	}
	if e = o.lock(ctx); e != nil {
		return nil, e
	}
	fail := func(err error) (*pluginAutoBackupPrepared, error) { o.unlock(); return nil, err }
	s, _, e := a.pluginStore.autoBackupRead(ctx, a.Store.DB, i.InstanceID, i.Generation)
	if e != nil {
		return fail(e)
	}
	if e = autoBackupNoUnknown(s); e != nil {
		return fail(e)
	}
	current, e := a.pluginStore.Installation(ctx, i.InstanceID)
	if e != nil || !current.Installed || current.Generation != i.Generation {
		return fail(bad("PLUGIN_SNAPSHOT_STALE", "自动备份安装已变化"))
	}
	config, e := a.pluginStore.Config(ctx, i.InstanceID)
	if e != nil {
		return fail(e)
	}
	return &pluginAutoBackupPrepared{o: o, fromRevision: config.Revision}, nil
}
func (p *pluginAutoBackupPrepared) publishTx(ctx context.Context, tx *sql.Tx) error {
	s, _, e := p.o.a.pluginStore.autoBackupRead(ctx, tx, p.o.instance, p.o.generation)
	if e != nil {
		return e
	}
	return autoBackupNoUnknown(s)
}

func autoBackupWorkerInput(run PluginRun) (json.RawMessage, error) {
	if run.Action != "api" {
		return run.Input, nil
	}
	var inv plugins.Invocation
	var request plugins.HTTPRequest
	if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &request) != nil || request.RequestID == "" {
		return nil, bad("AUTO_BACKUP_SCOPE_DENIED", "备份API输入无效")
	}
	inv.Input, _ = json.Marshal(map[string]string{"request_id": request.RequestID})
	return json.Marshal(inv)
}
