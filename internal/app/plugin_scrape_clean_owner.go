package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins/scrapefileclean"
	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

// There is one continuous native owner per installed generation. Gates order
// administrative transitions, not work: plugin_runs remains the only SDK queue.
// Never hold runtime.mu, pluginMu, lifecycle or a process token while waiting
// for a host command. The host Guard does not acquire runtime.mu or a gate.
type pluginScrapeCleanRuntime struct {
	mu      sync.Mutex
	owners  map[string]*pluginScrapeCleanOwner
	closing bool
	watch   scrapehost.WatchFactory // trusted native factory; nil uses platform observer.
}
type pluginScrapeCleanOwner struct {
	originalMachine *scrapefileclean.Machine
	a               *App
	instance        string
	generation      int64
	j               *pluginScrapeCleanJournal
	e               *pluginScrapeCleanEffects
	h               *scrapehost.Host
	roots           []scrapehost.RootStamp
	gate            chan struct{}
	ready           chan struct{}
	err             error
}
type pluginScrapeCleanPrepared struct {
	a      *App
	o      *pluginScrapeCleanOwner
	ticket scrapehost.TransitionTicket
	from   scrapehost.Binding
	once   sync.Once
}
type pluginScrapeCleanOwnerCall struct {
	owner *pluginScrapeCleanOwner
	run   *PluginRun
	roots []scrapehost.RootStamp
}
type pluginScrapeCleanOwnerCallKey struct{}

func scrapeCleanOwnerError(code, message string) error { return bad(code, message) }
func (o *pluginScrapeCleanOwner) lock(ctx context.Context) error {
	select {
	case o.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *pluginScrapeCleanOwner) unlock() { <-o.gate }
func scrapeCleanBinding(i PluginInstallation, c PluginStoredValue) scrapehost.Binding {
	return scrapehost.Binding{InstanceID: i.InstanceID, Generation: i.Generation, Revision: c.Revision}
}
func scrapeCleanOwnerContext(ctx context.Context, o *pluginScrapeCleanOwner, run *PluginRun) context.Context {
	var copied *PluginRun
	if run != nil {
		value := *run
		value.Input = append(json.RawMessage(nil), run.Input...)
		copied = &value
	}
	return context.WithValue(ctx, pluginScrapeCleanOwnerCallKey{}, pluginScrapeCleanOwnerCall{owner: o, run: copied})
}
func (a *App) scrapeCleanSnapshot(ctx context.Context, instance string) (PluginInstallation, PluginStoredValue, error) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	i, err := a.pluginStore.Installation(ctx, instance)
	if err != nil {
		return i, PluginStoredValue{}, err
	}
	c, err := a.pluginStore.Config(ctx, instance)
	if errors.Is(err, sql.ErrNoRows) && !i.DesiredEnabled {
		return i, PluginStoredValue{Value: json.RawMessage(`{}`)}, nil
	}
	return i, c, err
}
func scrapeCleanInstallation(i PluginInstallation) error {
	if i.PluginID != "ScrapeFileClean" || i.Version != "1.0.7" || !i.Installed {
		return scrapehost.ErrUnsafe
	}
	return nil
}
func scrapeCleanIdle(s scrapehost.Durable) bool {
	if s.Active != nil || s.Blocked != "" || len(s.Machine.State) > 0 || len(s.Machine.Queue) > 0 || len(s.Machine.Watches) > 0 || s.Machine.TimerDue != nil || len(s.Pins) > 0 || len(s.Frozen) > 0 || len(s.Events) > 0 {
		return false
	}
	for _, e := range s.Effects {
		if e.Kind != "lifecycle_applied" {
			return false
		}
	}
	return true
}

// Roots are capabilities, not every allowed parent. Preserve sealed capabilities
// in order across configuration changes, then append actual disjoint watched
// directories. All retained capabilities are re-authorized by current settings.
func (a *App) scrapeCleanRoots(raw json.RawMessage, prior []scrapehost.RootStamp) ([]scrapehost.RootStamp, error) {
	settings := a.Settings()
	out := append([]scrapehost.RootStamp(nil), prior...)
	for _, r := range prior {
		valid := false
		if r.Root.Role == "media" {
			for _, m := range settings.Roots {
				if m.ID == r.Root.ID && m.Path == r.Root.Path {
					valid = true
					break
				}
			}
		} else if r.Root.Role == "source" {
			for _, p := range settings.AllowedRoots {
				if within(p, r.Root.Path) {
					valid = true
					break
				}
			}
		}
		if !valid {
			return nil, scrapehost.ErrUnsafe
		}
	}
	if len(out) > 0 {
		if err := scrapeEffectRoots(out); err != nil {
			return nil, err
		}
	}
	needs, err := scrapeCleanNeedsFiles(raw)
	if err != nil {
		return nil, err
	}
	if !needs {
		return out, nil
	}
	cfg := scrapefileclean.NewConfig()
	if err = cfg.Apply(raw); err != nil {
		return nil, err
	}
	appendRoot := func(root scrapehost.Root) error {
		for _, r := range out {
			if within(r.Root.Path, root.Path) {
				if root.Role == "media" && root != r.Root {
					return scrapehost.ErrUnsafe
				}
				return nil
			}
		}
		for _, r := range out {
			if within(root.Path, r.Root.Path) {
				return bad("SCRAPE_CLEAN_ROOT_EXPANSION_UNSUPPORTED", "监控目录不能跨越现有源目录与媒体库授权边界，请选择独立的实际子目录")
			}
		}
		stamp, err := scrapeEffectStamp(root.Path)
		if err != nil {
			return err
		}
		out = append(out, scrapehost.RootStamp{Root: root, File: stamp})
		return nil
	}
	// Media roots retain their real core IDs. Adding them first makes a watch
	// within a library reuse its media authority rather than overlap a source.
	for _, m := range settings.Roots {
		if err = appendRoot(scrapehost.Root{ID: m.ID, Path: m.Path, Role: "media"}); err != nil {
			return nil, err
		}
	}
	var paths []string
	for _, path := range strings.Split(cfg.Monitor, "\n") {
		path = strings.TrimSpace(path)
		if path != "" {
			paths = append(paths, path)
		}
	}

	sort.Strings(paths)
	for _, path := range paths {
		if !importPath(path) || path == "/" || filepath.Clean(path) != path {
			return nil, scrapehost.ErrUnsafe
		}
		dir, openErr := importDir(path, false)
		if openErr != nil {
			return nil, openErr
		}
		dir.Close()

		covered := false
		for _, r := range out {
			if within(r.Root.Path, path) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		allowed := false
		for _, p := range settings.AllowedRoots {
			if within(p, path) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, scrapehost.ErrUnsafe
		}
		if err = appendRoot(scrapehost.Root{ID: "source_" + tokenHash(path), Path: path, Role: "source"}); err != nil {
			return nil, err
		}
	}
	if len(out) > 0 {
		if err = scrapeEffectRoots(out); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func scrapeCleanEffectiveConfig(_ json.RawMessage, raw json.RawMessage) (json.RawMessage, error) {
	cfg := scrapefileclean.NewConfig()
	if err := cfg.Apply(raw); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), raw...), nil
}
func scrapeCleanNeedsFiles(raw json.RawMessage) (bool, error) {
	cfg := scrapefileclean.NewConfig()
	if err := cfg.Apply(raw); err != nil {
		return false, err
	}
	if !scrapefileclean.Truth(cfg.Enabled) {
		return false, nil
	}
	for _, path := range strings.Split(cfg.Monitor, "\n") {
		if strings.TrimSpace(path) != "" {
			return true, nil
		}
	}

	return false, nil
}
func (a *App) scrapeCleanOwner(ctx context.Context, i PluginInstallation) (*pluginScrapeCleanOwner, error) {
	if err := scrapeCleanInstallation(i); err != nil {
		return nil, err
	}
	r := &a.pluginScrapeClean
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return nil, scrapehost.ErrClosed
	}
	if r.owners == nil {
		r.owners = map[string]*pluginScrapeCleanOwner{}
	}
	if old := r.owners[i.InstanceID]; old != nil {
		r.mu.Unlock()
		select {
		case <-old.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if old.generation != i.Generation {
			return nil, scrapehost.ErrUncertain
		}
		return old, old.err
	}
	o := &pluginScrapeCleanOwner{a: a, instance: i.InstanceID, generation: i.Generation, gate: make(chan struct{}, 1), ready: make(chan struct{})}
	r.owners[i.InstanceID] = o
	r.mu.Unlock()
	o.err = o.open(ctx)
	close(o.ready)
	if o.err != nil {
		r.mu.Lock()
		if r.owners[i.InstanceID] == o {
			delete(r.owners, i.InstanceID)
		}
		r.mu.Unlock()
	}
	return o, o.err
}
func (o *pluginScrapeCleanOwner) open(ctx context.Context) error {
	var err error
	o.j, err = newPluginScrapeCleanJournal(ctx, o.a.pluginStore, o.instance, o.generation)
	if err != nil {
		return err
	}
	o.e, err = newPluginScrapeCleanEffects(ctx, o.a, o.j)
	if err != nil {
		return err
	}
	s, err := o.j.Read(ctx)
	if err != nil {
		return err
	}
	if s.Active != nil || s.Blocked != "" {
		return scrapehost.ErrUncertain
	}
	for _, event := range s.Events {
		if event.State != "confirmed" && event.State != "pending" && event.State != "cancelled" {
			return scrapehost.ErrUncertain
		}
	}
	// A legacy confirmed file deletion without a core index receipt cannot be
	// attached to a later same-path owner merely because the process restarted.
	for _, fact := range s.Effects {
		if scrapeEffectFileFact(fact) && fact.Role == "media" {
			x := fact
			x.ID = "file-index:" + fact.ID
			if _, err = o.e.receipt(ctx, o.a.Store.DB, x); err != nil {
				return scrapehost.ErrUncertain
			}
		}
	}
	i, c, err := o.a.scrapeCleanSnapshot(ctx, o.instance)
	if err != nil {
		return err
	}
	if err = scrapeCleanInstallation(i); err != nil {
		return err
	}
	if i.Generation != o.generation {
		return scrapehost.ErrUnsafe
	}
	// Restart always opens the sealed capabilities, including old retained
	// source roots; new configuration authority is applied only by ExpandRoots.
	if len(s.Roots) > 0 {
		o.roots = append([]scrapehost.RootStamp(nil), s.Roots...)
		if err = o.e.coreRoots(o.roots); err != nil {
			return err
		}
		if err = scrapeEffectRoots(o.roots); err != nil {
			return err
		}
	} else {
		if !scrapeCleanIdle(s) {
			return scrapehost.ErrUncertain
		}
		return nil
	}
	return o.openHost(i, c)
}
func (o *pluginScrapeCleanOwner) openHost(i PluginInstallation, c PluginStoredValue) error {
	if len(o.roots) == 0 {
		return nil
	}
	opts := scrapehost.Options{Binding: scrapeCleanBinding(i, c), Roots: make([]scrapehost.Root, 0, len(o.roots)), Journal: o.j, Effects: o.e, Guard: o.guard, AfterAction: o.e.afterAction, Watch: o.a.pluginScrapeClean.watch, Log: o.logSink(o.a.Settings()), AsyncTimeout: 2 * time.Minute}
	for _, r := range o.roots {
		opts.Roots = append(opts.Roots, r.Root)
	}
	var err error
	o.h, err = scrapehost.Open(opts)
	if err == nil && o.originalMachine != nil {
		err = o.h.AdoptOriginalIdleMachine(context.Background(), o.originalMachine)
	}
	return err
}

// prepare methods must run before an HTTP/uninstall caller takes lifecycle or
// a process slot. Multiple targets reserve gates in stable order, pause all,
// then permit the caller's single SQL publication transaction.
func (a *App) prepareScrapeFileCleanTransition(ctx context.Context, i PluginInstallation, kind string) (*pluginScrapeCleanPrepared, error) {
	items, err := a.prepareScrapeFileCleanTransitions(ctx, []PluginInstallation{i}, kind)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return items[0], nil
}
func (a *App) prepareScrapeFileCleanTransitions(ctx context.Context, installations []PluginInstallation, kind string) ([]*pluginScrapeCleanPrepared, error) {
	if kind != "init" && kind != "reload" && kind != "reset" && kind != "uninstall" {
		return nil, scrapehost.ErrUnsafe
	}
	selected := map[string]PluginInstallation{}
	for _, i := range installations {
		if i.PluginID == "ScrapeFileClean" && i.Installed {
			if _, exists := selected[i.InstanceID]; exists {
				return nil, scrapehost.ErrUnsafe
			}
			selected[i.InstanceID] = i
		}
	}
	ids := make([]string, 0, len(selected))
	for key := range selected {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	prepared := make([]*pluginScrapeCleanPrepared, 0, len(ids))
	fail := func(cause error) ([]*pluginScrapeCleanPrepared, error) {
		for n := len(prepared) - 1; n >= 0; n-- {
			p := prepared[n]
			restore, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := p.resolve(restore, false); err != nil {
				cause = errors.Join(cause, err)
			}
			cancel()
			p.release()
		}
		return nil, cause
	}
	for _, key := range ids {
		i := selected[key]
		o, err := a.scrapeCleanOwner(ctx, i)
		if err != nil {
			return fail(err)
		}
		if err = o.lock(ctx); err != nil {
			return fail(err)
		}
		current, c, err := a.scrapeCleanSnapshot(ctx, key)
		if err != nil {
			o.unlock()
			return fail(err)
		}
		if err = scrapeCleanInstallation(current); err != nil || current.Generation != i.Generation || current.Version != i.Version {
			o.unlock()
			return fail(scrapehost.ErrUnsafe)
		}
		p := &pluginScrapeCleanPrepared{a: a, o: o, from: scrapeCleanBinding(current, c)}
		if o.h != nil {
			p.ticket, err = o.h.BeginTransition(scrapeCleanOwnerContext(ctx, o, nil), p.from, id("scrape_transition_"), kind)
		} else {
			p.ticket = scrapehost.TransitionTicket{ID: id("scrape_transition_"), Kind: kind, From: p.from}
			err = o.prepareIdle(ctx, p.ticket, c.Value)
		}
		if err != nil {
			o.unlock()
			return fail(err)
		}
		prepared = append(prepared, p)
	}
	return prepared, nil
}

// inertUpdate still takes the full core lock order and proves the exact old
// generation/revision in the same transaction as the journal mutation.
func (o *pluginScrapeCleanOwner) inertUpdate(ctx context.Context, b scrapehost.Binding, fn func(*scrapehost.Durable) error) error {
	o.a.pluginLifecycleMu.Lock()
	defer o.a.pluginLifecycleMu.Unlock()
	o.a.downloadMu.Lock()
	defer o.a.downloadMu.Unlock()
	o.a.importMu.Lock()
	defer o.a.importMu.Unlock()
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = o.verifyCoreTx(ctx, tx, b, "prepare"); err != nil {
		return err
	}
	if err = o.j.updateTx(ctx, tx, fn); err != nil {
		return err
	}
	return tx.Commit()
}
func (o *pluginScrapeCleanOwner) prepareIdle(ctx context.Context, t scrapehost.TransitionTicket, raw json.RawMessage) error {
	return o.inertUpdate(ctx, t.From, func(s *scrapehost.Durable) error {
		if !scrapeCleanIdle(*s) || s.Transition != nil && s.Transition.Phase != "applied" {
			return scrapehost.ErrUncertain
		}
		if s.Binding == (scrapehost.Binding{}) {
			s.Binding = t.From
			s.Config = append(json.RawMessage(nil), raw...)
		}
		if s.Binding != t.From {
			return scrapehost.ErrUnsafe
		}
		s.Transition = &scrapehost.Transition{ID: t.ID, Kind: t.Kind, From: t.From, Phase: "prepared"}
		return nil
	})
}
func (p *pluginScrapeCleanPrepared) release() {
	if p != nil {
		p.once.Do(p.o.unlock)
	}
}
func (p *pluginScrapeCleanPrepared) drain(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if p.ticket.Kind != "reset" && p.ticket.Kind != "uninstall" && p.ticket.Kind != "reload" {
		return scrapehost.ErrUnsafe
	}
	if p.o.h != nil {
		return p.o.h.DrainTransition(scrapeCleanOwnerContext(ctx, p.o, nil), p.ticket)
	}
	return p.o.inertUpdate(ctx, p.from, func(s *scrapehost.Durable) error {
		if !scrapeCleanIdle(*s) || s.Transition == nil || s.Transition.ID != p.ticket.ID || s.Transition.Phase != "prepared" {
			return scrapehost.ErrUnsafe
		}
		s.Transition.Phase = "drained"
		return nil
	})
}
func (p *pluginScrapeCleanPrepared) publishTx(ctx context.Context, tx *sql.Tx, to scrapehost.Binding, runID string) error {
	if p == nil {
		return nil
	}
	if err := p.o.verifyCoreTx(ctx, tx, to, p.ticket.Kind); err != nil {
		return err
	}
	if p.ticket.Kind == "init" || p.ticket.Kind == "reload" {
		if err := p.o.verifyRunTx(ctx, tx, to, runID, p.ticket.Kind, "pending", "running"); err != nil {
			return err
		}
	}
	return p.o.j.updateTx(ctx, tx, func(s *scrapehost.Durable) error { return scrapehost.PublishTransition(s, p.ticket, to, runID) })
}
func (o *pluginScrapeCleanOwner) verifyCoreTx(ctx context.Context, tx *sql.Tx, b scrapehost.Binding, kind string) error {
	var plugin, version string
	var generation, revision int64
	var installed, desired bool
	err := tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,COALESCE(v.revision,0) FROM plugin_installations i LEFT JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, o.instance).Scan(&plugin, &version, &generation, &installed, &desired, &revision)
	if err != nil {
		return err
	}
	if plugin != "ScrapeFileClean" || version != "1.0.7" || b.InstanceID != o.instance || b.Generation != generation {
		return scrapehost.ErrUnsafe
	}
	if kind == "uninstall" {
		if b.Revision != 0 {
			return scrapehost.ErrUnsafe
		}
	} else if b.Revision != revision {
		return scrapehost.ErrUnsafe
	}
	if kind == "uninstall" {
		if installed {
			return scrapehost.ErrUnsafe
		}
	} else if !installed {
		return scrapehost.ErrUnsafe
	}
	if kind == "reset" {
		if desired || revision != 0 {
			return scrapehost.ErrUnsafe
		}
	} else if kind != "uninstall" && kind != "prepare" && !desired {
		return scrapehost.ErrUnsafe
	}
	return nil
}
func (o *pluginScrapeCleanOwner) verifyRunTx(ctx context.Context, tx *sql.Tx, b scrapehost.Binding, id, action string, states ...string) error {
	var got PluginRun
	err := tx.QueryRowContext(ctx, `SELECT id,instance_id,generation,config_revision,version,action,state FROM plugin_runs WHERE id=?`, id).Scan(&got.ID, &got.InstanceID, &got.Generation, &got.ConfigRevision, &got.Version, &got.Action, &got.State)
	if err != nil {
		return err
	}
	valid := false
	for _, state := range states {
		valid = valid || got.State == state
	}
	if !valid || got.InstanceID != b.InstanceID || got.Generation != b.Generation || got.ConfigRevision != b.Revision || got.Version != "1.0.7" || got.Action != action {
		return scrapehost.ErrUnsafe
	}
	return nil
}
func (p *pluginScrapeCleanPrepared) abort(ctx context.Context) error {
	if p == nil {
		return nil
	}
	s, err := p.o.j.Read(ctx)
	if err != nil {
		return err
	}
	if s.Transition == nil || s.Transition.ID != p.ticket.ID || s.Transition.Phase != "prepared" {
		return scrapehost.ErrUncertain
	}
	return p.resolve(ctx, false)
}
func (p *pluginScrapeCleanPrepared) resolve(ctx context.Context, committed bool) error {
	if p == nil {
		return nil
	}
	defer p.release()
	// Inspect the same durable marker after an unknown SQL commit. The caller
	// has released lifecycle before entering here; no host command runs under it.
	p.a.pluginLifecycleMu.Lock()
	tx, err := p.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		p.a.pluginLifecycleMu.Unlock()
		return err
	}
	s, _, _, err := p.o.j.read(ctx, tx)
	if err == nil {
		if s.Transition == nil || s.Transition.ID != p.ticket.ID || s.Transition.From != p.from {
			err = scrapehost.ErrUncertain
		}
	}
	if err != nil {
		tx.Rollback()
		p.a.pluginLifecycleMu.Unlock()
		return err
	}
	t := *s.Transition
	published := t.Phase == "published" || t.Phase == "published_drained" || t.Phase == "applied"
	if published {
		err = p.o.verifyCoreTx(ctx, tx, t.To, t.Kind)
	} else {
		err = p.o.verifyCoreTx(ctx, tx, p.from, "prepare")
	}
	_ = tx.Rollback()
	p.a.pluginLifecycleMu.Unlock()
	if err != nil {
		return scrapehost.ErrUncertain
	}
	if published && !committed {
		return bad("PLUGIN_TRANSITION_UNCERTAIN", "配置提交结果需要核对；清理观察器保持暂停，未恢复旧配置")
	}
	if !published {
		if committed {
			return scrapehost.ErrUncertain
		}
		if p.o.h != nil {
			return p.o.h.AbortTransition(scrapeCleanOwnerContext(ctx, p.o, nil), p.ticket)
		}
		return p.o.inertUpdate(ctx, p.from, func(d *scrapehost.Durable) error {
			if !scrapeCleanIdle(*d) || d.Transition == nil || d.Transition.ID != p.ticket.ID || d.Transition.Phase != "prepared" && d.Transition.Phase != "drained" {
				return scrapehost.ErrUncertain
			}
			d.Transition = nil
			return nil
		})
	}
	if t.Kind == "init" || t.Kind == "reload" {
		return nil
	}
	if p.o.h != nil {
		if err = p.o.h.ApplyTransition(scrapeCleanOwnerContext(ctx, p.o, nil), p.ticket, t.RunID, nil); err != nil {
			return err
		}
	} else if err = p.o.finishIdle(ctx, t); err != nil {
		return err
	}
	p.a.pluginScrapeClean.mu.Lock()
	if p.a.pluginScrapeClean.owners[p.o.instance] == p.o {
		delete(p.a.pluginScrapeClean.owners, p.o.instance)
	}
	p.a.pluginScrapeClean.mu.Unlock()
	return nil
}

// Called only by the trusted deferred executor after Runner success and process
// release. Neither an SDK ACK nor worker-provided config authorizes this call.
func (a *App) pluginScrapeCleanHost(ctx context.Context, installation PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if run == nil {
		return nil, scrapehost.ErrUnsafe
	}
	if err := a.requireDeferredExecution(ctx, run, method); err != nil {
		return nil, err
	}
	current, c, err := a.scrapeCleanSnapshot(ctx, installation.InstanceID)
	if err != nil {
		return nil, err
	}
	if err = scrapeCleanInstallation(current); err != nil || !current.DesiredEnabled || current.Generation != run.Generation || current.Version != run.Version || run.InstanceID != current.InstanceID || c.Revision != run.ConfigRevision {
		return nil, scrapehost.ErrUnsafe
	}
	actual, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	if actual.State != "running" || actual.InstanceID != run.InstanceID || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Action != run.Action {
		return nil, scrapehost.ErrUnsafe
	}
	o, err := a.scrapeCleanOwner(ctx, current)
	if err != nil {
		return nil, err
	}
	if err = o.lock(ctx); err != nil {
		return nil, err
	}
	defer o.unlock()
	ctx = scrapeCleanOwnerContext(ctx, o, &actual)
	binding := scrapeCleanBinding(current, c)
	s, err := o.j.Read(ctx)
	if err != nil {
		return nil, err
	}
	var effective json.RawMessage
	var roots []scrapehost.RootStamp
	if method == scrapefileclean.ConfigureCapability {
		var empty map[string]json.RawMessage
		if scrapeJournalStrict(raw, &empty) != nil || empty == nil || len(empty) != 0 || (actual.Action != "init" && actual.Action != "reload") {
			return nil, scrapehost.ErrUnsafe
		}
		base := s.Config
		if actual.Action == "reload" {
			base = json.RawMessage(`{}`)
		}
		effective, err = scrapeCleanEffectiveConfig(base, c.Value)
		if err != nil {
			// If the source's raw config parser itself throws, execute that actual
			// idle Init once to retain its ordered member mutations. No file operation
			// can precede this parse failure, and the no-authority Ops also fail closed.
			if o.h == nil && originalInitializationMatches(ctx, o.instance, current.PluginID, current.Version, o.generation, actual.Action) {
				probe := scrapefileclean.NewConfig()
				if parseErr := probe.Apply(c.Value); parseErr != nil {
					return nil, o.initOriginalIdleMachine(ctx, c.Value)
				}
			}
			return nil, err
		}
		roots, err = a.scrapeCleanRoots(effective, s.Roots)
		if err != nil {
			return nil, err
		}
		if o.h == nil && len(roots) == 0 {
			return json.RawMessage(`null`), o.configureIdle(ctx, actual, c.Value)
		}
		if s.Transition == nil || s.Transition.Phase == "applied" {
			if s.Binding != (scrapehost.Binding{}) {
				return nil, scrapehost.ErrUncertain
			}
			// The first configure also needs a committed lifecycle receipt. Bind its
			// real running invocation before starting any native observer.
			ticket := scrapehost.TransitionTicket{ID: "cold:" + actual.ID, Kind: actual.Action, From: binding}
			if err = o.prepareIdle(ctx, ticket, json.RawMessage(`{}`)); err != nil {
				return nil, err
			}
			a.pluginLifecycleMu.Lock()
			tx, beginErr := a.Store.DB.BeginTx(ctx, nil)
			if beginErr == nil {
				p := pluginScrapeCleanPrepared{a: a, o: o, ticket: ticket, from: binding}
				beginErr = p.publishTx(ctx, tx, binding, actual.ID)
				if beginErr == nil {
					beginErr = tx.Commit()
				} else {
					tx.Rollback()
				}
			}
			a.pluginLifecycleMu.Unlock()
			if beginErr != nil {
				return nil, beginErr
			}
			s, err = o.j.Read(ctx)
			if err != nil {
				return nil, err
			}
		}
		t := s.Transition
		if t == nil || t.RunID != actual.ID || t.To != binding || t.Kind != actual.Action {
			return nil, scrapehost.ErrUnsafe
		}
		ticket := scrapehost.TransitionTicket{ID: t.ID, Kind: t.Kind, From: t.From}
		if o.h == nil {
			// A proven inert generation has no observer/pins to preserve. Its roots
			// become durable under the same actual To/run proof before native Open.
			a.pluginLifecycleMu.Lock()
			a.downloadMu.Lock()
			a.importMu.Lock()
			tx, beginErr := a.Store.DB.BeginTx(ctx, nil)
			if beginErr == nil {
				beginErr = o.verifyCoreTx(ctx, tx, binding, "init")
				if beginErr == nil {
					beginErr = o.verifyRunTx(ctx, tx, binding, actual.ID, actual.Action, "running")
				}
				if beginErr == nil {
					beginErr = o.j.updateTx(ctx, tx, func(d *scrapehost.Durable) error {
						if !scrapeCleanIdle(*d) || d.Transition == nil || d.Transition.ID != t.ID || d.Transition.RunID != actual.ID || d.Transition.To != binding {
							return scrapehost.ErrUncertain
						}
						d.Roots = append([]scrapehost.RootStamp(nil), roots...)
						return nil
					})
				}
				if beginErr == nil {
					beginErr = tx.Commit()
				} else {
					tx.Rollback()
				}
			}
			a.importMu.Unlock()
			a.downloadMu.Unlock()
			a.pluginLifecycleMu.Unlock()
			if beginErr != nil {
				return nil, beginErr
			}
			o.roots = roots
			if err = o.openHost(current, c); err != nil {
				return nil, err
			}
		} else {
			call := ctx.Value(pluginScrapeCleanOwnerCallKey{}).(pluginScrapeCleanOwnerCall)
			call.roots = roots
			expansionCtx := context.WithValue(ctx, pluginScrapeCleanOwnerCallKey{}, call)
			plan := make([]scrapehost.Root, 0, len(roots))
			for _, r := range roots {
				plan = append(plan, r.Root)
			}
			if err = o.h.ExpandRoots(expansionCtx, ticket, actual.ID, plan); err != nil {
				return nil, err
			}
		}
		if originalInitializationMatches(ctx, o.instance, current.PluginID, current.Version, o.generation, actual.Action) {
			machine, captureErr := o.h.OriginalMachine(ctx)
			if captureErr != nil {
				return nil, captureErr
			}
			o.originalMachine = machine
			captureOriginalNativeState(ctx, machine)
			captureOriginalAppliedConfiguration(ctx, c.Value)
		}
		err = o.h.ApplyTransition(ctx, ticket, actual.ID, c.Value)
	} else if method == scrapefileclean.ApplyCapability {
		var input struct {
			ID string `json:"event_id"`
		}
		if actual.Action != "event" || scrapeJournalStrict(raw, &input) != nil || input.ID == "" || o.h == nil {
			return nil, scrapehost.ErrUnsafe
		}
		var envelope struct {
			Input json.RawMessage `json:"input"`
		}
		var bound struct {
			ID string `json:"event_id"`
		}
		if json.Unmarshal(actual.Input, &envelope) != nil || scrapeJournalStrict(envelope.Input, &bound) != nil || bound.ID != input.ID {
			return nil, scrapehost.ErrUnsafe
		}
		err = o.h.Apply(ctx, binding, input.ID)
	} else {
		return nil, scrapehost.ErrUnsupported
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(`null`), nil
}

func (o *pluginScrapeCleanOwner) configureIdle(ctx context.Context, run PluginRun, raw json.RawMessage) error {
	o.a.pluginLifecycleMu.Lock()
	defer o.a.pluginLifecycleMu.Unlock()
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b := scrapehost.Binding{InstanceID: run.InstanceID, Generation: run.Generation, Revision: run.ConfigRevision}
	if err = o.verifyCoreTx(ctx, tx, b, "init"); err != nil {
		return err
	}
	if err = o.verifyRunTx(ctx, tx, b, run.ID, run.Action, "running"); err != nil {
		return err
	}
	err = o.j.updateTx(ctx, tx, func(s *scrapehost.Durable) error {
		if !scrapeCleanIdle(*s) {
			return scrapehost.ErrUncertain
		}
		if originalInitializationMatches(ctx, o.instance, "ScrapeFileClean", run.Version, o.generation, run.Action) {
			if err := o.initOriginalIdleMachine(ctx, raw); err != nil {
				return err
			}
		} else {
			cfg := scrapefileclean.NewConfig()
			if err := cfg.Apply(raw); err != nil {
				return err
			}

		}
		s.Config = append(json.RawMessage(nil), raw...)
		if s.Transition != nil && s.Transition.Phase != "applied" {
			if s.Transition.RunID != run.ID || s.Transition.To != b || s.Transition.Kind != run.Action {
				return scrapehost.ErrUnsafe
			}
			s.Transition.Phase = "applied"
		} else if s.Binding == (scrapehost.Binding{}) {
			s.Transition = &scrapehost.Transition{ID: "cold:" + run.ID, Kind: run.Action, Phase: "applied", RunID: run.ID, From: b, To: b}
		} else {
			return scrapehost.ErrUncertain
		}
		s.Binding = b
		if _, exists := s.Effects["lifecycle:"+run.ID]; exists {
			return scrapehost.ErrUncertain
		}
		scope := "cold:" + run.ID
		if s.Transition != nil {
			scope = s.Transition.ID
		}
		payload, _ := json.Marshal(struct {
			Kind string
			To   scrapehost.Binding
		}{run.Action, b})
		s.Effects["lifecycle:"+run.ID] = scrapehost.Effect{ID: "lifecycle:" + run.ID, Scope: scope, Kind: "lifecycle_applied", Binding: b, State: "confirmed", Result: true, Payload: payload}
		return nil
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (o *pluginScrapeCleanOwner) finishIdle(ctx context.Context, t scrapehost.Transition) error {
	o.a.pluginLifecycleMu.Lock()
	defer o.a.pluginLifecycleMu.Unlock()
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = o.verifyCoreTx(ctx, tx, t.To, t.Kind); err != nil {
		return err
	}
	err = o.j.updateTx(ctx, tx, func(s *scrapehost.Durable) error {
		if !scrapeCleanIdle(*s) || s.Transition == nil || s.Transition.ID != t.ID || s.Transition.Phase != "published_drained" {
			return scrapehost.ErrUncertain
		}
		payload, _ := json.Marshal(struct {
			Kind string
			To   scrapehost.Binding
		}{t.Kind, t.To})
		key := t.RunID
		if key == "" {
			key = t.ID
		}
		s.Effects["lifecycle:"+key] = scrapehost.Effect{ID: "lifecycle:" + key, Scope: t.ID, Kind: "lifecycle_applied", Binding: t.From, State: "confirmed", Result: true, Payload: payload}
		s.Transition.Phase = "applied"
		return nil
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) restoreScrapeFileCleanOwners(ctx context.Context) error {
	items, err := a.pluginStore.Installations(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, i := range items {
		if i.PluginID != "ScrapeFileClean" || i.Version != "1.0.7" || !i.DesiredEnabled {
			continue
		}
		o, err := a.scrapeCleanOwner(ctx, i)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if err = o.lock(ctx); err != nil {
			failures = append(failures, err)
			continue
		}
		err = func() error {
			s, err := o.j.Read(ctx)
			if err != nil {
				return err
			}
			if s.Binding == (scrapehost.Binding{}) || s.Transition != nil && s.Transition.Phase != "applied" {
				return nil
			}
			_, c, err := a.scrapeCleanSnapshot(ctx, i.InstanceID)
			if err != nil || s.Binding != scrapeCleanBinding(i, c) {
				return scrapehost.ErrUncertain
			}
			if o.h != nil {
				return o.h.Recover(scrapeCleanOwnerContext(ctx, o, nil), s.Binding)
			}
			return nil
		}()
		o.unlock()
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (a *App) closeScrapeFileCleanOwners(ctx context.Context) error {
	r := &a.pluginScrapeClean
	r.mu.Lock()
	r.closing = true
	var owners []*pluginScrapeCleanOwner
	for _, o := range r.owners {
		owners = append(owners, o)
	}
	r.mu.Unlock()
	sort.Slice(owners, func(i, j int) bool { return owners[i].instance < owners[j].instance })
	var failures []error
	for _, o := range owners {
		select {
		case <-o.ready:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := o.lock(ctx); err != nil {
			failures = append(failures, err)
			continue
		}
		if o.h == nil {
			o.unlock()
			continue
		}
		err := o.h.Close(scrapeCleanOwnerContext(ctx, o, nil))
		if err != nil {
			failures = append(failures, err)
			fallback, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = o.h.Suspend(fallback)
			cancel()
		}
		o.unlock()
	}
	return errors.Join(failures...)
}

// Called after source recovery has discarded old pending tasks and scanned a
// fresh baseline. Do not create a redundant worker/init invocation on startup.
func (a *App) shouldInitializeScrapeFileClean(ctx context.Context, i PluginInstallation, c PluginStoredValue) (bool, error) {
	var exists int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='plugin_scrape_journal_meta'`).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		return true, nil
	}
	j := &pluginScrapeCleanJournal{p: a.pluginStore, instance: i.InstanceID, generation: i.Generation}
	s, err := j.Read(ctx)
	if err != nil {
		return false, err
	}
	if s.Binding == (scrapehost.Binding{}) {
		return true, nil
	}
	if s.Active != nil || s.Blocked != "" || s.Transition != nil && s.Transition.Phase != "applied" {
		return false, nil
	}
	for _, e := range s.Events {
		if e.State != "confirmed" && e.State != "pending" && e.State != "cancelled" {
			return false, nil
		}
	}
	effective, err := scrapeCleanEffectiveConfig(s.Config, c.Value)
	if err != nil {
		return false, err
	}
	if s.Binding != scrapeCleanBinding(i, c) || string(effective) != string(s.Config) {
		return false, scrapehost.ErrUncertain
	}
	return false, nil
}

// Preflight rejects ordinary configuration mistakes before publishing a new
// revision/run. It grants no lease and performs no action; the actual Guard
// repeats current authorization/identity checks after publication.
func (p *pluginScrapeCleanPrepared) validateConfig(ctx context.Context, raw json.RawMessage, kind string) error {
	if p == nil {
		return scrapehost.ErrUnsafe
	}
	if (kind != "init" && kind != "reload") || kind != p.ticket.Kind {
		return scrapehost.ErrUnsafe
	}
	current, c, err := p.a.scrapeCleanSnapshot(ctx, p.o.instance)
	if err != nil {
		return err
	}
	if err = scrapeCleanInstallation(current); err != nil || scrapeCleanBinding(current, c) != p.from {
		return scrapehost.ErrUnsafe
	}
	s, err := p.o.j.Read(ctx)
	if err != nil {
		return err
	}
	if s.Active != nil || s.Blocked != "" || s.Binding != p.from || s.Transition == nil || s.Transition.ID != p.ticket.ID || s.Transition.From != p.from || s.Transition.Phase != "prepared" {
		return scrapehost.ErrUncertain
	}
	if len(raw) == 0 {
		if kind != "reload" {
			return scrapehost.ErrUnsafe
		}
		raw = c.Value
	}
	base := s.Config
	if kind == "reload" {
		base = json.RawMessage(`{}`)
	}
	effective, err := scrapeCleanEffectiveConfig(base, raw)
	if err != nil {
		return bad("INVALID_PLUGIN_CONFIG", "清理插件配置无效")
	}
	if _, err = p.a.scrapeCleanRoots(effective, s.Roots); err != nil {
		var api *APIError
		if errors.As(err, &api) {
			return err
		}
		return bad("SCRAPE_CLEAN_CONFIG_PATH_INVALID", "清理目录不可访问、越过授权边界或包含不支持的存储，请核对配置")
	}
	return nil
}
