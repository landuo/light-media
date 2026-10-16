package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	seed "light-media/internal/plugins/cleaninvalidseed"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

// The instance gate orders real source actions and administrative publication.
// It is never waited on with lifecycle, process or file-write locks retained.
// Source plans remain in the journal; plugin_runs is the only execution queue.
type pluginCleanInvalidSeedRuntime struct {
	mu      sync.Mutex
	owners  map[string]*pluginCleanInvalidSeedOwner
	closing bool
	now     func() time.Time // trusted native clock seam; never SDK/config input
}
type pluginCleanInvalidSeedOwner struct {
	a          *App
	instance   string
	generation int64
	gate       chan struct{}
	j          *pluginCleanInvalidSeedJournal
	journal    *pluginCleanInvalidSeedActionJournal
	core       *pluginCleanInvalidSeedCore
	h          *seedhost.Owner
}
type pluginCleanInvalidSeedCall struct {
	owner     *pluginCleanInvalidSeedOwner
	run       PluginRun // immutable original admission and input
	inv       seedhost.Invocation
	config    json.RawMessage
	effective json.RawMessage
	nonce     string
	mu        sync.Mutex
	lease     *pluginCleanInvalidSeedLease
}
type pluginCleanInvalidSeedCallKey struct{}
type pluginCleanInvalidSeedStopKey struct{}

func cleanSeedBindingForRun(r PluginRun) seedhost.Binding {
	return seedhost.Binding{InstanceID: r.InstanceID, Generation: r.Generation, Revision: r.ConfigRevision}
}
func cleanSeedInvocation(r PluginRun) (seedhost.Invocation, error) {
	inv := seedhost.Invocation{Binding: cleanSeedBindingForRun(r), RunID: r.ID, Action: r.Action, InputSHA: tokenHash(string(r.Input))}
	if !pluginStorageID.MatchString(r.ID) || !pluginStorageID.MatchString(r.InstanceID) || r.Version != "2.1" || r.Generation < 1 || r.ConfigRevision < 1 || len(r.Input) == 0 {
		return inv, seedhost.ErrUnsafe
	}
	var envelope struct {
		Action string          `json:"action"`
		Config json.RawMessage `json:"config"`
		Input  json.RawMessage `json:"input"`
	}
	if cleanupJournalStrict(r.Input, &envelope) != nil || envelope.Action != r.Action {
		return inv, seedhost.ErrUnsafe
	}
	switch r.Action {
	case "init", "reload", "run":
		if len(envelope.Input) != 0 && string(envelope.Input) != "null" && string(envelope.Input) != "{}" {
			return inv, seedhost.ErrUnsafe
		}
	case "event", "service", "once", "command":
		var bound struct {
			ID string `json:"event_id"`
		}
		if cleanupJournalStrict(envelope.Input, &bound) != nil || bound.ID == "" || len(bound.ID) > 128 {
			return inv, seedhost.ErrUnsafe
		}
		inv.EventID = bound.ID
	default:
		return inv, seedhost.ErrUnsafe
	}
	return inv, nil
}
func cleanSeedInstalled(i PluginInstallation) error {
	if i.PluginID != "CleanInvalidSeed" || i.Version != "2.1" || !i.Installed || i.Generation < 1 {
		return seedhost.ErrUnsafe
	}
	return nil
}
func (o *pluginCleanInvalidSeedOwner) lock(ctx context.Context) error {
	select {
	case o.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *pluginCleanInvalidSeedOwner) unlock() { <-o.gate }
func (a *App) cleanSeedOwner(ctx context.Context, i PluginInstallation) (*pluginCleanInvalidSeedOwner, error) {
	if err := cleanSeedInstalled(i); err != nil {
		return nil, err
	}
	r := &a.pluginCleanInvalidSeed
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return nil, seedhost.ErrUnsafe
	}
	if r.owners == nil {
		r.owners = map[string]*pluginCleanInvalidSeedOwner{}
	}
	if o := r.owners[i.InstanceID]; o != nil {
		if o.generation != i.Generation {
			return nil, seedhost.ErrUnknown
		}
		return o, nil
	}
	j, err := newPluginCleanInvalidSeedJournal(ctx, a.pluginStore, i.InstanceID, i.Generation)
	if err != nil {
		return nil, err
	}
	o := &pluginCleanInvalidSeedOwner{a: a, instance: i.InstanceID, generation: i.Generation, gate: make(chan struct{}, 1), j: j}
	o.core = &pluginCleanInvalidSeedCore{o: o}
	o.journal = &pluginCleanInvalidSeedActionJournal{o: o}
	r.owners[i.InstanceID] = o
	return o, nil
}
func (o *pluginCleanInvalidSeedOwner) openHost(b seedhost.Binding) error {
	if o.h != nil {
		return nil
	}
	var err error
	// Initial empty authority is deliberate: no root or downloader is usable
	// before native BeforeRun has acquired and validated the real action lease.
	o.h, err = seedhost.Open(seedhost.Options{Binding: b, Journal: o.journal, Core: o.core, BeforeRun: o.beforeRun, Timezone: "Asia/Shanghai"})
	return err
}

func cleanSeedConfigMap(raw json.RawMessage) (map[string]any, error) {
	var c map[string]any
	if cleanupJournalStrict(raw, &c) != nil || c == nil {
		return nil, seedhost.ErrUnsafe
	}
	return c, nil
}
func cleanSeedEffectiveConfig(warm, persisted, proposed json.RawMessage, action string) (json.RawMessage, error) {
	base := seed.New(nil).Config
	if action != "reload" {
		old := warm
		if len(old) == 0 && action != "init" {
			old = persisted
		}
		if len(old) != 0 {
			m, err := cleanSeedConfigMap(old)
			if err != nil {
				return nil, err
			}
			base = m
		}
	}
	if action == "init" || action == "reload" {
		m, err := cleanSeedConfigMap(proposed)
		if err != nil {
			return nil, err
		}
		if len(m) != 0 {
			base = m
		}
	}
	return cleanupJournalJSON(base)
}

// Only the real deferred execution may call configure/execute. The worker
// supplies a fixed action or opaque event ID; config, clients and paths come
// from SQLite/Settings under the native action lease.
func (a *App) pluginCleanInvalidSeedHost(ctx context.Context, installation PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method == seed.FormCapability {
		if string(raw) != "{}" || run != nil || installation.PluginID != "CleanInvalidSeed" || installation.Version != "2.1" {
			return nil, seedhost.ErrUnsafe
		}
		expected, _ := json.Marshal(seed.Manifest())
		preview := installation.InstanceID == installation.PluginID && installation.Generation == 0 && !installation.Installed && !installation.DesiredEnabled && installation.CreatedAt == "" && installation.UpdatedAt == "" && installation.ManifestSchemaVersion == 1 && bytes.Equal(installation.Manifest, expected)
		current, readErr := a.pluginStore.Installation(ctx, installation.InstanceID)
		if preview {
			if !errors.Is(readErr, sql.ErrNoRows) && (readErr != nil || current.PluginID != "CleanInvalidSeed" || current.Installed) {
				return nil, seedhost.ErrUnsafe
			}
		} else if readErr != nil || cleanSeedInstalled(current) != nil || current.Generation != installation.Generation {
			return nil, seedhost.ErrUnsafe
		}
		names := []string{}
		for _, d := range a.Settings().Downloaders {
			names = append(names, d.Name)
		}
		return seed.Form(names), nil
	}
	if err := cleanSeedInstalled(installation); err != nil {
		return nil, err
	}
	if run == nil || (method != seed.ConfigureCapability && method != seed.ExecuteCapability) {
		return nil, seedhost.ErrUnsafe
	}
	if err := a.requireDeferredExecution(ctx, run, method); err != nil {
		return nil, err
	}
	inv, err := cleanSeedInvocation(*run)
	if err != nil {
		return nil, err
	}
	if method == seed.ConfigureCapability {
		if string(raw) != "{}" || (run.Action != "init" && run.Action != "reload") {
			return nil, seedhost.ErrUnsafe
		}
	} else if run.Action == "run" {
		var action struct {
			Action string `json:"action"`
		}
		if cleanupJournalStrict(raw, &action) != nil || action.Action != "clean" {
			return nil, seedhost.ErrUnsafe
		}
	} else {
		var event struct {
			ID string `json:"event_id"`
		}
		if cleanupJournalStrict(raw, &event) != nil || event.ID != inv.EventID || event.ID == "" {
			return nil, seedhost.ErrUnsafe
		}
	}
	o, err := a.cleanSeedOwner(ctx, installation)
	if err != nil {
		return nil, err
	}
	if err = o.lock(ctx); err != nil {
		return nil, err
	}
	defer o.unlock()
	if err = o.openHost(inv.Binding); err != nil {
		return nil, err
	}
	warm, err := o.h.RuntimeConfig(ctx)
	if err != nil {
		return nil, err
	}
	s, err := o.j.Read(ctx)
	if err != nil {
		return nil, err
	}
	c, err := a.pluginStore.Config(ctx, installation.InstanceID)
	if err != nil {
		return nil, err
	}
	effective, err := cleanSeedEffectiveConfig(warm, s.Config, c.Value, run.Action)
	if err != nil {
		return nil, err
	}
	copied := *run
	copied.Input = append(json.RawMessage(nil), run.Input...)
	call := &pluginCleanInvalidSeedCall{owner: o, run: copied, inv: inv, config: append(json.RawMessage(nil), c.Value...), effective: effective, nonce: id("clean_")}
	ctx = context.WithValue(ctx, pluginCleanInvalidSeedCallKey{}, call)
	if method == seed.ConfigureCapability {
		err = o.h.Configure(ctx, inv, call.config)
	} else {
		err = o.h.Execute(ctx, inv)
	}
	if err != nil {
		return nil, err
	}
	result, knownCode, found, proofErr := a.pluginStore.cleanInvalidSeedDeferredOutcome(context.WithoutCancel(ctx), copied, method)
	if proofErr != nil {
		return nil, proofErr
	}
	if !found {
		return nil, seedhost.ErrUnknown
	}
	if knownCode != "" {
		return nil, bad(knownCode, "清理做种插件执行失败，已保留确认过的执行记录")
	}
	return result, nil
}

// cleanInvalidSeedDeferredOutcome is historical proof only. It does not create
// an owner, inspect current configuration, touch files or call a downloader.
func (p *PluginStore) cleanInvalidSeedDeferredOutcome(ctx context.Context, run PluginRun, method string) (json.RawMessage, string, bool, error) {
	inv, err := cleanSeedInvocation(run)
	if err != nil {
		return nil, "", false, err
	}
	if method == seed.ConfigureCapability && run.Action != "init" && run.Action != "reload" || method == seed.ExecuteCapability && (run.Action == "init" || run.Action == "reload") || method != seed.ConfigureCapability && method != seed.ExecuteCapability {
		return nil, "", false, seedhost.ErrUnsafe
	}
	var exists int
	if err = p.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='plugin_clean_seed_meta'`).Scan(&exists); err != nil || exists == 0 {
		return nil, "", false, err
	}
	j := &pluginCleanInvalidSeedJournal{p: p, instance: run.InstanceID, generation: run.Generation}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", false, err
	}
	defer tx.Rollback()
	s, _, err := j.read(ctx, tx)
	if err != nil {
		return nil, "", false, err
	}
	r, ok := s.Runs[run.ID]
	if !ok || r.Invocation != inv || (r.Phase != "confirmed" && r.Phase != "failed") || len(r.Result) == 0 {
		return nil, "", false, nil
	}
	for _, effect := range s.Effects {
		if effect.RunID != run.ID {
			continue
		}
		if effect.Binding != inv.Binding || effect.Phase != "confirmed" {
			return nil, "", false, nil
		}
		if effect.Kind == "file_delete" {
			if effect.Pin == nil {
				return nil, "", false, nil
			}
			for _, pin := range effect.Manifest {
				matched := false
				for _, step := range s.Effects {
					if step.RunID == run.ID && step.Kind == "file_step" && step.Pin != nil && *step.Pin == pin && strings.HasPrefix(step.ID, effect.ID+":step:") && step.Phase == "confirmed" {
						matched = true
						break
					}
				}
				if !matched {
					return nil, "", false, nil
				}
			}
			continue
		}
		if effect.Kind != "native_write" && effect.Kind != "file_step" && effect.Kind != "config_write" && effect.Kind != "notice" {
			return nil, "", false, seedhost.ErrUnsafe
		}
		if err = cleanSeedReadEffectReceipt(ctx, tx, j, inv, effect); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, "", false, nil
			}
			return nil, "", false, err
		}
	}
	code := ""
	if r.Phase == "failed" {
		if r.ErrorCode == "SOURCE_FAILED" {
			code = "CLEAN_SEED_SOURCE_FAILED"
		} else if r.ErrorCode == "HOST_ACTION_DENIED" {
			code = "CLEAN_SEED_HOST_DENIED"
		} else {
			return nil, "", false, nil
		}
	}
	return append(json.RawMessage(nil), r.Result...), code, true, nil
}

// Startup must preserve persisted once deadlines and cron anchors. A normal
// close stops scheduling without replaying source cleanup. This hook is called
// under the existing initializer's pluginMu and does not take that lock again.
func (a *App) shouldInitializeCleanInvalidSeed(ctx context.Context, i PluginInstallation, c PluginStoredValue) (bool, error) {
	if err := cleanSeedInstalled(i); err != nil {
		return false, err
	}
	var exists int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='plugin_clean_seed_meta'`).Scan(&exists); err != nil || exists == 0 {
		return exists == 0, err
	}
	j := &pluginCleanInvalidSeedJournal{p: a.pluginStore, instance: i.InstanceID, generation: i.Generation}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	s, m, err := j.read(ctx, tx)
	if err != nil {
		return false, err
	}
	for _, r := range s.Runs {
		if r.Phase == "started" || r.Phase == "unknown" {
			return false, seedhost.ErrUnknown
		}
	}
	if m.Transition != nil && m.Transition.Phase != "applied" {
		return false, seedhost.ErrUnknown
	}
	return m.Closed || m.AppliedRun == "" || m.AppliedBinding != (seedhost.Binding{InstanceID: i.InstanceID, Generation: i.Generation, Revision: c.Revision}), nil
}

func (a *App) stopCleanInvalidSeedPlugins() {
	r := &a.pluginCleanInvalidSeed
	r.mu.Lock()
	r.closing = true
	owners := make([]*pluginCleanInvalidSeedOwner, 0, len(r.owners))
	for _, o := range r.owners {
		owners = append(owners, o)
	}
	r.mu.Unlock()
	sort.Slice(owners, func(i, j int) bool { return owners[i].instance < owners[j].instance })
	for _, o := range owners {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := o.lock(ctx); err == nil {
			if o.h != nil {
				if err = o.h.Stop(context.WithValue(ctx, pluginCleanInvalidSeedStopKey{}, o)); err != nil {
					a.log("ERROR", "plugins", "clean_seed_stop_pending", "清理做种插件尚未停止，已保留状态", "", "")
				}
			}
			o.unlock()
		}
		cancel()
	}
}
