package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"light-media/internal/plugins/scrapefileclean"
	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

func scrapeCleanMutates(action string) bool {
	return action == "configure" || action == "event" || action == "apply" || action == "timer" || action == "transition_abort" || strings.HasPrefix(action, "stop:")
}

// Every owner command waits for this lock itself. An HTTP/deferred/uninstall
// caller must never wait for the owner while retaining lifecycle or process.
// Context values carry run identity only; they never suppress lock acquisition.
func (o *pluginScrapeCleanOwner) guard(ctx context.Context, b scrapehost.Binding, action string) (func(), error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	call, owned := ctx.Value(pluginScrapeCleanOwnerCallKey{}).(pluginScrapeCleanOwnerCall)
	if owned && call.owner != o {
		return nil, scrapehost.ErrUnsafe
	}
	if !owned && action != "event" && action != "timer" {
		return nil, scrapehost.ErrUnsafe
	}
	if (action == "configure" || action == "apply") && (!owned || call.run == nil) {
		return nil, scrapehost.ErrUnsafe
	}
	if owned && call.run != nil {
		method := scrapefileclean.ConfigureCapability
		if call.run.Action == "event" {
			method = scrapefileclean.ApplyCapability
		}
		if err := o.a.requireDeferredExecution(ctx, call.run, method); err != nil {
			return nil, err
		}
	}
	o.a.pluginLifecycleMu.Lock()
	// Snapshot plugin state briefly. Settings publication already takes
	// pluginMu -> download -> import; never reverse that order after waiting.
	i, c, err := o.a.scrapeCleanSnapshot(ctx, o.instance)
	if err != nil {
		o.a.pluginLifecycleMu.Unlock()
		return nil, err
	}
	if i.PluginID != "ScrapeFileClean" || i.Version != "1.0.7" || b.InstanceID != o.instance || b.Generation != o.generation {
		o.a.pluginLifecycleMu.Unlock()
		return nil, scrapehost.ErrUnsafe
	}
	o.a.downloadMu.Lock()
	o.a.importMu.Lock()
	release := func() { o.a.importMu.Unlock(); o.a.downloadMu.Unlock(); o.a.pluginLifecycleMu.Unlock() }
	if err = ctx.Err(); err != nil {
		release()
		return nil, err
	}
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		release()
		return nil, err
	}
	validationCtx := ctx
	if owned && call.run != nil {
		method := scrapefileclean.ConfigureCapability
		if call.run.Action == "event" {
			method = scrapefileclean.ApplyCapability
		}
		validationCtx = context.WithValue(ctx, scrapeAdmissionKey{}, scrapeAccepted(*call.run, method))
	}
	anchor, _, proofErr := o.j.root(validationCtx, tx)
	if proofErr == nil {
		proofErr = o.a.pluginStore.scrapeCleanValidateAcceptedTx(validationCtx, tx, o.j, anchor)
	}
	if proofErr != nil {
		tx.Rollback()
		release()
		return nil, proofErr
	}
	s, _, _, err := o.j.read(ctx, tx)
	if err != nil {
		tx.Rollback()
		release()
		return nil, err
	}
	roots := s.Roots
	if len(roots) == 0 {
		roots = append([]scrapehost.RootStamp(nil), o.roots...)
	}
	expansion := action == "transition_roots"
	retire := strings.HasPrefix(action, "transition_retire:") || action == "transition_receipt" && s.Transition != nil && (s.Transition.Kind == "reset" || s.Transition.Kind == "uninstall")
	if retire {
		t := s.Transition
		if t == nil || t.From != b || t.To.Generation != b.Generation+1 || t.Phase != "published_drained" && t.Phase != "applied" {
			err = scrapehost.ErrUnsafe
		} else {
			err = o.verifyCoreTx(ctx, tx, t.To, t.Kind)
		}
	} else if expansion {
		t := s.Transition
		if !owned || call.run == nil || t == nil || t.From != b || t.To != scrapeCleanBinding(i, c) || t.RunID != call.run.ID || t.Kind != call.run.Action || (t.Phase != "published" && t.Phase != "published_drained") {
			err = scrapehost.ErrUnsafe
		} else {
			err = o.verifyCoreTx(ctx, tx, t.To, "init")
			if err == nil {
				err = o.verifyRunTx(ctx, tx, t.To, call.run.ID, call.run.Action, "running")
			}
			if err == nil {
				var effective json.RawMessage
				base := s.Config
				if call.run.Action == "reload" {
					base = json.RawMessage(`{}`)
				}
				effective, err = scrapeCleanEffectiveConfig(base, c.Value)
				if err == nil {
					var expected []scrapehost.RootStamp
					expected, err = o.a.scrapeCleanRoots(effective, s.Roots)
					if err == nil && !scrapeCleanSameRoots(expected, call.roots) {
						err = scrapehost.ErrUnsafe
					}
					roots = call.roots
				}
			}
		}
	} else {
		if !i.Installed || !i.DesiredEnabled || i.Generation != b.Generation || c.Revision != b.Revision {
			err = scrapehost.ErrUnsafe
		}
		if err == nil {
			err = o.verifyCoreTx(ctx, tx, b, "init")
		}
		if err == nil && call.run != nil && (action == "configure" || action == "apply") {
			var count int
			err = tx.QueryRowContext(ctx, `SELECT count(*) FROM plugin_runs WHERE id=? AND instance_id=? AND generation=? AND config_revision=? AND version='1.0.7' AND state='running' AND action=?`, call.run.ID, b.InstanceID, b.Generation, b.Revision, call.run.Action).Scan(&count)
			if err == nil && count != 1 {
				err = scrapehost.ErrUnsafe
			}
		}
		if err == nil && s.Transition != nil && s.Transition.Phase != "applied" {
			t := s.Transition
			switch {
			case action == "configure":
				if call.run == nil || t.RunID != call.run.ID || t.Kind != call.run.Action || t.To != b || t.Phase != "published" && t.Phase != "published_drained" {
					err = scrapehost.ErrUnsafe
				}
			case action == "transition_abort":
				if t.From != b || t.Phase != "prepared" && t.Phase != "drained" {
					err = scrapehost.ErrUnsafe
				}
			case strings.HasPrefix(action, "stop:"):
				if t.From != b {
					err = scrapehost.ErrUnsafe
				}
			case strings.HasPrefix(action, "transition_begin:"):
				err = scrapehost.ErrTransition
			default:
				err = scrapehost.ErrTransition
			}
		}
	}
	_ = tx.Rollback()
	if err == nil {
		err = o.e.coreRoots(roots)
	}
	if err == nil {
		err = scrapeEffectRoots(roots)
	}
	if err != nil {
		release()
		return nil, err
	}
	if !scrapeCleanMutates(action) {
		return release, nil
	}
	runID := ""
	if call.run != nil && (action == "configure" || action == "apply") {
		runID = call.run.ID
	}
	revoke, err := o.e.authorize(ctx, b, action, runID, roots)
	if err != nil {
		release()
		return nil, err
	}
	return func() { revoke(); release() }, nil
}

// This is a read-only hook for the administrator's retry of the same deferred
// run. No new RunID/config/transition is created and no restored event is run.
func (a *App) validateScrapeFileCleanDeferredRetry(ctx context.Context, run PluginRun) error {
	i, err := a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil {
		return err
	}
	c, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		return err
	}
	if err = scrapeCleanInstallation(i); err != nil || !i.DesiredEnabled || i.Generation != run.Generation || i.Version != run.Version || c.Revision != run.ConfigRevision {
		return scrapehost.ErrUnsafe
	}
	d, _, err := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
	if err != nil {
		return err
	}
	if d.Phase != "prepared" {
		return scrapehost.ErrUncertain
	}
	j := &pluginScrapeCleanJournal{p: a.pluginStore, instance: run.InstanceID, generation: run.Generation}
	var exists int
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='plugin_scrape_journal_meta'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {

		return scrapehost.ErrUnsafe
	}
	s, err := j.Read(ctx)
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
	for _, effect := range s.Effects {
		if effect.State != "confirmed" {
			return scrapehost.ErrUncertain
		}
		if effect.Kind == "lifecycle_applied" && effect.ID == "lifecycle:"+run.ID {
			return scrapehost.ErrUncertain
		}
	}
	b := scrapeCleanBinding(i, c)
	if s.Transition != nil && s.Transition.Phase != "applied" {
		t := s.Transition
		if t.RunID != run.ID || t.To != b || t.Kind != run.Action || t.Phase != "published" && t.Phase != "published_drained" {
			return scrapehost.ErrUnsafe
		}
	} else if s.Transition != nil || run.Action != "init" || s.Binding != (scrapehost.Binding{}) && s.Binding != b {
		return scrapehost.ErrUnsafe
	}
	return nil
}

// Shutdown happens after worker cancellation/wait and before closing Store.
// It holds no outer lifecycle/process token and discards unstarted pending tasks.
func (a *App) stopScrapeFileCleanPlugins() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := a.closeScrapeFileCleanOwners(ctx); err != nil {
		a.log("ERROR", "plugins", "cleanup_shutdown_pending", "清理插件收尾未完成；已保留活动任务和文件操作凭据供核对", "", "")
	}
}

func scrapeCleanSameRoots(a, b []scrapehost.RootStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for n, r := range a {
		if r.Root != b[n].Root || !scrapeEffectSameEntity(r.File, b[n].File) {
			return false
		}
	}
	return true
}

// Recovery consumes only a sealed completed result. It never opens an owner,
// consults a newer installation as authority, or repeats an external action.
func (p *PluginStore) scrapeCleanDeferredOutcome(ctx context.Context, run PluginRun, method string) (json.RawMessage, bool, error) {
	j := &pluginScrapeCleanJournal{p: p, instance: run.InstanceID, generation: run.Generation}
	var exists int
	if err := p.store.DB.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='plugin_scrape_journal_meta'`).Scan(&exists); err != nil {
		return nil, false, err
	}
	if exists == 0 {
		return nil, false, nil
	}
	s, err := j.Read(ctx)
	if err != nil {
		return nil, false, err
	}
	return p.scrapeCleanOutcomeFromState(ctx, p.store.DB, run, method, s)
}
func (p *PluginStore) scrapeCleanOutcomeFromState(ctx context.Context, q pluginRemoveLinkJournalReader, run PluginRun, method string, s scrapehost.Durable) (json.RawMessage, bool, error) {
	j := &pluginScrapeCleanJournal{p: p, instance: run.InstanceID, generation: run.Generation}
	// Later watcher uncertainty cannot erase an already confirmed earlier
	// lifecycle receipt. Queue admission still rejects that current uncertainty.
	b := scrapehost.Binding{InstanceID: run.InstanceID, Generation: run.Generation, Revision: run.ConfigRevision}
	if method == scrapefileclean.ConfigureCapability {
		if run.Action != "init" && run.Action != "reload" {
			return nil, false, nil
		}
		t := s.Transition
		receipt, ok := s.Effects["lifecycle:"+run.ID]
		var payload struct {
			Kind string
			To   scrapehost.Binding
		}
		if !ok || receipt.ID != "lifecycle:"+run.ID || receipt.Scope == "" || receipt.Kind != "lifecycle_applied" || receipt.Binding != b || receipt.State != "confirmed" || !receipt.Result || scrapeJournalStrict(receipt.Payload, &payload) != nil || payload.Kind != run.Action || payload.To != b {
			return nil, false, nil
		}
		// A normal source shutdown or a later applied configuration replaces
		// the current marker. It cannot erase the immutable old RunID receipt.
		// If it still names this run, every transition field must agree.
		if t != nil && t.RunID == run.ID && (t.Phase != "applied" || t.Kind != run.Action || t.To != b || s.Binding != b || t.ID != receipt.Scope) {
			return nil, false, nil
		}
	} else if method == scrapefileclean.ApplyCapability {
		var invocation struct {
			Action string          `json:"action"`
			Input  json.RawMessage `json:"input"`
		}
		var input struct {
			ID string `json:"event_id"`
		}
		if run.Action != "event" || json.Unmarshal(run.Input, &invocation) != nil || invocation.Action != "event" || scrapeJournalStrict(invocation.Input, &input) != nil || input.ID == "" {
			return nil, false, scrapehost.ErrUnsafe
		}
		event, ok := s.Events[input.ID]
		if !ok || event.Event.ID != input.ID || event.Binding != b || event.State != "confirmed" || event.Error != "" {
			return nil, false, nil
		}
	} else {
		return nil, false, scrapehost.ErrUnsafe
	}
	// Confirmed host callbacks must also have durable Core index receipts for
	// every media deletion. A pre-owner/external journal cannot supply that proof.
	effects := &pluginScrapeCleanEffects{j: j}
	for _, fact := range s.Effects {
		if scrapeEffectFileFact(fact) && fact.Role == "media" {
			x := fact
			x.ID = "file-index:" + fact.ID
			if _, err := effects.receipt(ctx, q, x); err != nil {
				return nil, false, nil
			}
		}
	}
	return json.RawMessage(`null`), true, nil
}
