package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"light-media/internal/plugins"
	cf "light-media/internal/plugins/cloudflarespeedtest"
	"light-media/internal/plugins/schedule"
)

// Candidate native owner. The SDK can only submit a bound terminal handshake;
// private state and effects live in the actual App SQLite database.
const cloudflareSchema = `CREATE TABLE IF NOT EXISTS plugin_cloudflare_state(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));`
const cloudflareBlockedByUnknown = "CLOUDFLARE_BLOCKED_BY_UNKNOWN"

func cloudflareBlockedResult() json.RawMessage {
	return json.RawMessage(`{"version":1,"decision":"blocked_by_unknown","effects_started":false}`)
}

type pluginCloudflareRuntime struct {
	mu                   sync.Mutex
	owners               map[string]*pluginCloudflareOwner
	now                  func() time.Time
	afterEffect          func(string) error
	beforeRetentionLeaf  func(string)
	beforeOwnerAdmission func(context.Context, PluginRun)
	client               *http.Client
	proxyClient          *http.Client // Owned protocol-test seam; never worker/config input.
	nativeFactory        func(*cloudflareCall) (cf.Runtime, error)
}
type pluginCloudflareOwner struct {
	a             *App
	instance      string
	generation    int64
	timezone      string
	timezoneError bool
	gate          chan struct{}
	machine       *cf.Machine
	active        *cloudflareCall
}
type cloudflareEffect struct {
	Kind, Phase  string
	Args, Result json.RawMessage
}
type cloudflareAction struct {
	RunID, Action, InputSHA, Method, Phase, ErrorCode string
	Revision                                          int64
	Result                                            json.RawMessage
	Effects                                           []cloudflareEffect
	Resolution                                        *cloudflareResolution
	Entered                                           bool
}
type cloudflareState struct {
	Version                     int
	Instance                    string
	Generation, AppliedRevision int64
	Config                      json.RawMessage
	Artifacts                   []cloudflareAssetProof
	Runs                        map[string]cloudflareAction
}
type cloudflareCall struct {
	o              *pluginCloudflareOwner
	run            PluginRun
	method         string
	uncertain      bool
	config         cf.Object
	revision       int64
	defs           []pluginScheduleDefinition
	staged         cf.Object
	warming        bool
	native         cf.Runtime
	customInstance PluginInstallation
	customRevision int64
	asset          *cloudflareAssetProof
}
type cloudflarePin struct {
	Dev, Ino uint64
	Mode     uint32
	Size     int64
	Mod      int64
}

func (p *PluginStore) isCloudflareRun(ctx context.Context, tx *sql.Tx, r PluginRun) bool {
	if r.Version != "1.5.1" {
		return false
	}
	var plugin string
	return tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, r.InstanceID).Scan(&plugin) == nil && plugin == "CloudflareSpeedTest"
}
func cloudflareConfig(raw json.RawMessage) (cf.Object, error) {
	var v cf.Object
	if cleanupJournalStrict(raw, &v) != nil || v == nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "Cloudflare优选配置必须为对象")
	}
	return v, nil
}
func cloudflareKey(g int64) string { return "cloudflare:v1:" + strconv.FormatInt(g, 10) }
func (p *PluginStore) cloudflareRead(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, generation int64) (cloudflareState, []byte, error) {
	if db, ok := q.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return cloudflareState{}, nil, err
		}
		defer tx.Rollback()
		s, blob, err := p.cloudflareRead(ctx, tx, instance, generation)
		if err != nil {
			return s, nil, err
		}
		if err = tx.Commit(); err != nil {
			return s, nil, err
		}
		return s, blob, nil
	}
	s := cloudflareState{Version: 1, Instance: instance, Generation: generation, Runs: map[string]cloudflareAction{}}
	var sealed []byte
	err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_cloudflare_state WHERE instance_id=? AND generation=?`, instance, generation).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil, cloudflareEvidenceError()
	}
	if err != nil {
		return s, nil, err
	}
	raw, err := p.unseal("run_result", instance, cloudflareKey(generation), sealed)
	if err != nil {
		return s, nil, cloudflareEvidenceError()
	}
	if cleanupJournalStrict(raw, &s) != nil || s.Version != 1 || s.Instance != instance || s.Generation != generation || s.Runs == nil {
		return s, nil, bad("CLOUDFLARE_JOURNAL_INVALID", "优选执行记录无法校验")
	}
	if err = p.cloudflareValidateRootChain(ctx, q, s, raw); err != nil {
		return s, nil, err
	}
	for key, r := range s.Runs {
		if r.ErrorCode == cloudflareBlockedByUnknown && (r.Phase != "failed" || len(r.Effects) != 0 || !bytes.Equal(r.Result, cloudflareBlockedResult())) {
			return s, nil, bad("CLOUDFLARE_JOURNAL_INVALID", "优选零效果阻止记录无法校验")
		}
		if r.Phase == "abandoned_unknown" && !validCloudflareResolution(r, s.Instance, s.Generation) {
			return s, nil, bad("CLOUDFLARE_JOURNAL_INVALID", "优选放弃记录无效")
		}
		if key != r.RunID || !pluginStorageID.MatchString(key) || r.Revision < 1 || !pluginStorageDigest.MatchString(r.InputSHA) {
			return s, nil, bad("CLOUDFLARE_JOURNAL_INVALID", "优选执行身份无效")
		}
		switch r.Phase {
		case "started", "confirmed", "failed", "abandoned_unknown":
		default:
			return s, nil, bad("CLOUDFLARE_JOURNAL_INVALID", "优选执行阶段无效")
		}
		for _, e := range r.Effects {
			if (e.Phase != "started" && e.Phase != "confirmed") || e.Kind == "" || !json.Valid(e.Args) {
				return s, nil, bad("CLOUDFLARE_JOURNAL_INVALID", "优选副作用阶段无效")
			}
		}
	}
	if err = p.cloudflareValidateDeferred(ctx, q, s); err != nil {
		return s, nil, err
	}
	return s, sealed, nil
}
func (p *PluginStore) cloudflareSave(ctx context.Context, tx *sql.Tx, s cloudflareState, previous []byte) error {
	raw, err := json.Marshal(s)
	if err != nil || len(raw) > 16<<20 {
		return bad("CLOUDFLARE_JOURNAL_LIMIT", "优选执行记录超出容量")
	}
	if previous == nil {
		return cloudflareEvidenceError()
	}
	root, rootBlob, err := p.cloudflareRootRead(ctx, tx, s.Instance, s.Generation)
	if err != nil {
		return err
	}
	old, err := p.unseal("run_result", s.Instance, cloudflareKey(s.Generation), previous)
	if err != nil || root.StateSHA != tokenHash(string(old)) {
		return cloudflareEvidenceError()
	}
	sealed, err := p.seal("run_result", s.Instance, cloudflareKey(s.Generation), raw)
	if err != nil {
		return err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE plugin_cloudflare_state SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?`, sealed, s.Instance, s.Generation, previous)
	if err != nil {
		return err
	}
	if n, err := changed.RowsAffected(); err != nil || n != 1 {
		return bad("CLOUDFLARE_JOURNAL_CONFLICT", "优选执行记录已变化")
	}
	root.Sequence++
	root.StateSHA = tokenHash(string(raw))
	sealed, err = p.seal("run_result", s.Instance, cloudflareRootKey(s.Generation), mustCleanSeedJSON(root))
	if err != nil {
		return err
	}
	changed, err = tx.ExecContext(ctx, `UPDATE plugin_cloudflare_roots SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?`, sealed, s.Instance, s.Generation, rootBlob)
	if err != nil {
		return err
	}
	if n, err := changed.RowsAffected(); err != nil || n != 1 {
		return cloudflareEvidenceError()
	}
	return nil
}

func (o *pluginCloudflareOwner) update(ctx context.Context, fn func(*cloudflareState) error) error {
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, b, err := o.a.pluginStore.cloudflareRead(ctx, tx, o.instance, o.generation)
	if err == nil {
		err = fn(&s)
	}
	if err == nil {
		err = o.a.pluginStore.cloudflareSave(ctx, tx, s, b)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func cloudflareNoUnknown(s cloudflareState) error {
	for _, r := range s.Runs {
		if r.Phase == "abandoned_unknown" && validCloudflareResolution(r, s.Instance, s.Generation) {
			continue
		}
		if r.Phase == "started" {
			return bad("CLOUDFLARE_UNCERTAIN", "优选存在待核对副作用，未重复执行")
		}
		for _, e := range r.Effects {
			if e.Phase != "confirmed" {
				return bad("CLOUDFLARE_UNCERTAIN", "优选存在待核对副作用，未重复执行")
			}
		}
	}
	return nil
}

func (a *App) cloudflareOwner(ctx context.Context, i PluginInstallation) (*pluginCloudflareOwner, error) {
	if i.PluginID != cf.PluginID || i.Version != cf.Version || !i.Installed || i.Generation < 1 {
		return nil, bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare安装身份不匹配")
	}
	r := &a.pluginCloudflare
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owners == nil {
		r.owners = map[string]*pluginCloudflareOwner{}
	}
	key := i.InstanceID + ":" + strconv.FormatInt(i.Generation, 10)
	if o := r.owners[key]; o != nil {
		return o, nil
	}
	zone, e := autoBackupHostTimezone()
	invalid := e != nil
	if invalid {
		zone = "Etc/UTC"
	}
	o := &pluginCloudflareOwner{a: a, instance: i.InstanceID, generation: i.Generation, timezone: zone, timezoneError: invalid, gate: make(chan struct{}, 1)}
	r.owners[key] = o
	return o, nil
}
func (o *pluginCloudflareOwner) lock(ctx context.Context) error {
	select {
	case o.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *pluginCloudflareOwner) unlock() { <-o.gate }
func cloudflareDeferredSpec(run PluginRun) (string, json.RawMessage, error) {
	var inv plugins.Invocation
	if cleanupJournalStrict(run.Input, &inv) != nil || inv.Action != run.Action {
		return "", nil, bad("INVALID_PLUGIN_RUN", "Cloudflare执行输入无效")
	}
	switch run.Action {
	case "init", "reload":
		return cf.ConfigureCapability, json.RawMessage(`{}`), nil
	case "run", cf.Action:
		if len(inv.Input) == 0 || string(inv.Input) == "null" || string(inv.Input) == "{}" {
			return cf.RunCapability, json.RawMessage(`{}`), nil
		}
	case "once", "service", "event", "command":
		var token struct {
			ID string `json:"event_id"`
		}
		if cleanupJournalStrict(inv.Input, &token) == nil && pluginStorageID.MatchString(token.ID) {
			return cf.RunCapability, inv.Input, nil
		}
	case "api":
		var request plugins.HTTPRequest
		if cleanupJournalStrict(inv.Input, &request) == nil && request.RequestID != "" && len(request.RequestID) <= 128 {
			raw, _ := json.Marshal(map[string]string{"request_id": request.RequestID})
			return cf.RunCapability, raw, nil
		}
	}
	return "", nil, bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare执行没有绑定宿主来源")
}
func cloudflareWorkerInput(run PluginRun) (json.RawMessage, error) {
	if run.Action != "api" {
		return run.Input, nil
	}
	var inv plugins.Invocation
	var request plugins.HTTPRequest
	if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &request) != nil || request.RequestID == "" {
		return nil, bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare API输入无效")
	}
	inv.Input = mustCleanSeedJSON(map[string]string{"request_id": request.RequestID})
	return json.Marshal(inv)
}
func (a *App) shouldInitializeCloudflare(ctx context.Context, i PluginInstallation, config PluginStoredValue) (bool, error) {
	s, _, e := a.pluginStore.cloudflareRead(ctx, a.Store.DB, i.InstanceID, i.Generation)
	if e != nil {
		return false, e
	}
	if e = cloudflareNoUnknown(s); e != nil {
		return false, e
	}
	return s.AppliedRevision != config.Revision, nil
}

func (a *App) pluginCloudflareHost(ctx context.Context, i PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if run == nil {
		return nil, bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare缺少持久执行")
	}
	want, payload, e := cloudflareDeferredSpec(*run)
	if e != nil || want != method || !deferredPayloadEqual(raw, payload) {
		return nil, bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare执行身份不一致")
	}
	if e = a.requireDeferredExecution(ctx, run, method); e != nil {
		return nil, e
	}
	o, e := a.cloudflareOwner(ctx, i)
	if e != nil {
		return nil, e
	}
	d, _ := ctx.Value(pluginDeferredExecutionKey{}).(*pluginDeferredCall)
	if !cloudflareHeldOwner(ctx, a, d, o) {
		return nil, bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare执行缺少当前私有宿主预留")
	}
	if e = a.validatePluginHostRun(ctx, i, run, method); e != nil {
		return nil, e
	}
	if hook := a.pluginCloudflare.beforeOwnerAdmission; hook != nil {
		hook(ctx, *run)
	}
	ctx, e = a.cloudflareAuthorizedAdmission(ctx, *run, method)
	if e != nil {
		return nil, e
	}
	config, e := a.pluginStore.Config(ctx, i.InstanceID)
	if e != nil {
		return nil, e
	}
	value, e := cloudflareConfig(config.Value)
	if e != nil {
		return nil, e
	}
	c := &cloudflareCall{o: o, run: *run, method: method, config: value, revision: config.Revision}
	c.run.Input = append(json.RawMessage(nil), run.Input...)
	o.active = c
	defer func() {
		if closer, ok := c.native.(interface{ Close() }); ok {
			closer.Close()
		}
		o.active = nil
	}()
	var blocked error
	e = o.update(ctx, func(s *cloudflareState) error {
		action, exists := s.Runs[run.ID]
		if !exists {
			return cloudflareEvidenceError()
		}
		if cloudflareBlockedMatches(action, c) {
			blocked = bad(cloudflareBlockedByUnknown, "已有优选执行待核对")
			return nil
		}
		if !cloudflareStartedMatches(action, c) {
			return bad("CLOUDFLARE_REPLAY_DENIED", "Cloudflare执行已经进入宿主")
		}
		action.Entered = true
		s.Runs[run.ID] = action
		return nil
	})
	if e != nil {
		return nil, e
	}
	if blocked != nil {
		return nil, bad(cloudflareBlockedByUnknown, "已有优选执行待核对，此次没有开始测速或配置写入")
	}
	// Admission failures after creating this private action have a known zero
	// effect outcome. Preserve that terminal fact instead of leaving a false
	// unknown timeline that can never be admitted or recovered.
	var result json.RawMessage
	var sourceErr error
	if run.Action == "api" {
		sourceErr = a.validatePluginHTTPRun(ctx, *run)
	}
	if sourceErr == nil && (run.Action == "once" || run.Action == "service") {
		sourceErr = a.pluginStore.validateCloudflareOccurrence(ctx, *run)
	}
	if sourceErr == nil && (run.Action == "event" || run.Action == "command") {
		sourceErr = c.validateCommand(ctx)
	}
	if sourceErr == nil {
		result, sourceErr = c.execute(ctx)
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if c.uncertain {
		a.pluginExecutionLog(i, *run, "ERROR", "cloudflare_effect_uncertain", "Cloudflare优选结果待核对；未重试测速或配置写入", "CLOUDFLARE_UNCERTAIN")
		return nil, bad("CLOUDFLARE_UNCERTAIN", "Cloudflare副作用结果未确认")
	}
	e = o.update(finish, func(s *cloudflareState) error {
		r := s.Runs[run.ID]
		for _, effect := range r.Effects {
			if effect.Phase != "confirmed" {
				return bad("CLOUDFLARE_UNCERTAIN", "Cloudflare副作用收据缺失")
			}
		}
		r.Phase = "confirmed"
		r.Result = result
		if sourceErr != nil {
			class := cf.ErrorClass(sourceErr)
			if class == "" {
				class = cloudflareErrorCode(sourceErr)
			}
			if class == "" {
				class = "HostError"
			}
			r.Phase = "failed"
			r.ErrorCode = "CLOUDFLARE_SOURCE_FAILED"
			r.Result = mustCleanSeedJSON(map[string]any{"source_failed": true, "error_type": class})
		}
		if o.machine != nil {
			s.Config = mustCleanSeedJSON(o.machine.Config())
		}
		s.Runs[run.ID] = r
		return nil
	})
	if e != nil {
		return nil, e
	}
	return result, sourceErr
}

func (c *cloudflareCall) effect(ctx context.Context, kind string, args any, apply func() (any, error)) (any, error) {
	if e := c.Guard(ctx); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(args)
	if e != nil {
		return nil, e
	}
	var ordinal int
	e = c.o.update(ctx, func(s *cloudflareState) error {
		r := s.Runs[c.run.ID]
		if r.Phase != "started" {
			return bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare执行已结束")
		}
		ordinal = len(r.Effects)
		r.Effects = append(r.Effects, cloudflareEffect{Kind: kind, Phase: "started", Args: raw})
		s.Runs[c.run.ID] = r
		return nil
	})
	if e != nil {
		c.uncertain = true
		return nil, e
	}
	v, applyErr := apply()
	var completedExtractionFailure *cf.ExtractionError
	if applyErr != nil && (kind != "archive_extract" || !errors.As(applyErr, &completedExtractionFailure)) {
		c.uncertain = true
		return nil, applyErr
	}
	if applyErr != nil {
		v = map[string]string{"completed_failure": "ExtractionError"}
	}
	if hook := c.o.a.pluginCloudflare.afterEffect; hook != nil {
		if e = hook(kind); e != nil {
			c.uncertain = true
			return nil, e
		}
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	result, e := json.Marshal(v)
	if e != nil {
		c.uncertain = true
		return nil, e
	}
	e = c.o.update(finish, func(s *cloudflareState) error {
		r := s.Runs[c.run.ID]
		if ordinal >= len(r.Effects) || r.Effects[ordinal].Kind != kind || r.Effects[ordinal].Phase != "started" || !bytes.Equal(r.Effects[ordinal].Args, raw) {
			return bad("CLOUDFLARE_JOURNAL_CONFLICT", "Cloudflare副作用身份变化")
		}
		r.Effects[ordinal].Phase = "confirmed"
		r.Effects[ordinal].Result = result
		if kind == "archive_extract" && c.asset != nil {
			s.Artifacts = append(s.Artifacts, *c.asset)
		}
		s.Runs[c.run.ID] = r
		return nil
	})
	if e != nil {
		c.uncertain = true
	}
	if e != nil {
		return nil, e
	}
	return v, applyErr
}

// Local effects and their authenticated receipt commit in one App transaction.
// No DB-pool call or global lock wait is allowed inside apply.
func (c *cloudflareCall) local(ctx context.Context, kind string, args any, apply func(*sql.Tx) (any, error)) (any, error) {
	if e := c.Guard(ctx); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(args)
	if e != nil {
		return nil, e
	}
	tx, e := c.o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = c.guardTx(ctx, tx); e != nil {
		return nil, e
	}
	s, b, e := c.o.a.pluginStore.cloudflareRead(ctx, tx, c.o.instance, c.o.generation)
	if e != nil {
		return nil, e
	}
	r := s.Runs[c.run.ID]
	if r.Phase != "started" {
		return nil, bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare执行已结束")
	}
	v, e := apply(tx)
	if e != nil {
		return nil, e
	}
	result, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	r.Effects = append(r.Effects, cloudflareEffect{Kind: kind, Phase: "confirmed", Args: raw, Result: result})
	s.Runs[c.run.ID] = r
	if kind == "own_config" {
		s.AppliedRevision = c.revision
		s.Config = raw
	}
	if e = c.o.a.pluginStore.cloudflareSave(ctx, tx, s, b); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		c.uncertain = true
		return nil, e
	}
	if hook := c.o.a.pluginCloudflare.afterEffect; hook != nil {
		if e = hook(kind); e != nil {
			c.uncertain = true
			return nil, e
		}
	}
	return v, nil
}

func (p *PluginStore) cloudflareOutcome(ctx context.Context, run PluginRun, method string) (json.RawMessage, string, bool, error) {
	s, _, e := p.cloudflareRead(ctx, p.store.DB, run.InstanceID, run.Generation)
	if e != nil {
		return nil, "", false, e
	}
	r, ok := s.Runs[run.ID]
	if !ok {
		return nil, "", false, nil
	}
	if r.Action != run.Action || r.Revision != run.ConfigRevision || r.InputSHA != deferredInputSHA(run.Input) || r.Method != method {
		return nil, "", false, cloudflareEvidenceError()
	}
	if r.Phase != "confirmed" && r.Phase != "failed" {
		return nil, "", false, nil
	}
	for _, effect := range r.Effects {
		if effect.Phase != "confirmed" {
			return nil, "", false, nil
		}
		if effect.Kind == "notice" {
			var proof struct {
				RouteID    string `json:"route_id"`
				InitialSHA string `json:"initial_sha"`
			}
			if cleanupJournalStrict(effect.Result, &proof) != nil {
				return nil, "", false, cloudflareEvidenceError()
			}
			route, _, e := p.readNoticeRoute(ctx, p.store.DB, proof.RouteID)
			if e != nil {
				return nil, "", false, e
			}
			if !route.Complete {
				return nil, "", false, nil
			}
		}
	}
	return r.Result, r.ErrorCode, true, nil
}

func (c *cloudflareCall) execute(ctx context.Context) (json.RawMessage, error) {
	if c.o.timezoneError {
		return nil, &cf.SourceError{Class: "UnknownTimeZoneError", Operation: "host TZ"}
	}
	originalInit := originalInitializationMatches(ctx, c.run.InstanceID, "CloudflareSpeedTest", c.run.Version, c.run.Generation, c.run.Action)
	m := c.o.machine
	if originalInit {
		// Start creates a new object; direct init retains that object's members.
		m, _ = originalNativeStateBeforeInit(ctx).(*cf.Machine)
	}
	if m == nil {
		var e error
		m, e = cf.New(c.o, c.o)
		if e != nil {
			return nil, e
		}
		s, _, e := c.o.a.pluginStore.cloudflareRead(ctx, c.o.a.Store.DB, c.o.instance, c.o.generation)
		if e != nil {
			return nil, e
		}
		if !originalInit && len(s.Config) > 0 && !bytes.Equal(bytes.TrimSpace(s.Config), []byte("null")) {
			warm, e := cloudflareConfig(s.Config)
			if e != nil {
				return nil, e
			}
			warm["onlyonce"] = false
			c.warming = true
			e = m.Init(ctx, warm)
			c.warming = false
			if e != nil {
				return nil, e
			}
		}
		c.o.machine = m
	}
	if originalInit {
		c.o.machine = m
		captureOriginalNativeState(ctx, m)
		defer func() { captureOriginalAppliedConfiguration(ctx, m.Config()) }()
	}
	if c.run.Action == "init" || c.run.Action == "reload" {
		if e := m.Init(ctx, c.config); e != nil {
			return nil, e
		}
		if !originalInit {
			services, e := m.Services(func(s string) error { _, e := schedule.ParseCrontab(s, c.o.timezone); return e })
			if e != nil {
				return nil, e
			}
			for _, service := range services {
				c.defs = append(c.defs, pluginScheduleDefinition{ID: service.ID, Name: service.Name, Handler: "cloudflare", Trigger: schedule.Spec{Kind: "cron", Cron: service.Cron, Timezone: c.o.timezone}, MisfireGraceMS: 1000})
			}
		}

		updated := c.run
		e := c.o.a.pluginStore.configurePluginSchedulesWithEffect(ctx, &updated, "cloudflare", c.defs, c.Now(), nil, func(ctx context.Context, tx *sql.Tx, _ PluginRun) error {
			s, b, e := c.o.a.pluginStore.cloudflareRead(ctx, tx, c.o.instance, c.o.generation)
			if e != nil {
				return e
			}
			revision := c.run.ConfigRevision
			if c.staged != nil {
				v, e := c.o.a.pluginStore.saveValueTx(ctx, tx, c.o.instance, "config", "main", revision, mustCleanSeedJSON(c.staged))
				if e != nil {
					return e
				}
				revision = v.Revision
				if _, e = tx.ExecContext(ctx, `UPDATE plugin_schedules SET config_revision=? WHERE instance_id=? AND generation=? AND group_key='cloudflare'`, revision, c.o.instance, c.o.generation); e != nil {
					return e
				}
			}
			s.AppliedRevision = revision
			s.Config = mustCleanSeedJSON(m.Config())
			r := s.Runs[c.run.ID]
			r.Phase = "confirmed"
			r.Result = json.RawMessage(`{"configured":true}`)
			s.Runs[c.run.ID] = r
			return c.o.a.pluginStore.cloudflareSave(ctx, tx, s, b)
		})
		if e != nil {
			return nil, e
		}
		return json.RawMessage(`{"configured":true}`), nil
	}
	if c.run.Action == "api" {
		var inv plugins.Invocation
		var request plugins.HTTPRequest
		_ = json.Unmarshal(c.run.Input, &inv)
		_ = json.Unmarshal(inv.Input, &request)
		value, e := m.API(ctx, request.CredentialMatches["apikey"])
		if e != nil {
			return nil, e
		}
		body := mustCleanSeedJSON(map[string]any{"success": value.Success, "message": value.Message, "data": map[string]any{}})
		return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body})
	}
	var event *cf.Event
	if c.run.Action == "event" || c.run.Action == "command" {
		e, err := c.commandEvent(ctx)
		if err != nil {
			return nil, err
		}
		event = &cf.Event{Data: e}
	}
	out, e := m.Main(ctx, event)
	if e != nil {
		return nil, e
	}
	return json.Marshal(out)
}
