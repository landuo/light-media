package durable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	source "light-media/internal/plugins/autosignin"
)

type Command struct {
	Action         string
	Config         any
	Event          *source.EventEnvelope
	DeletedID      any
	Domain         string
	APIKeyAccepted bool
}
type BoundSite struct {
	Site     source.Object
	Hooks    source.SiteVisitHooks
	Revision string
}
type Dependencies struct {
	Source    SourcePort
	Inventory func(context.Context) (source.Inventory, error)
	Lookup    func(context.Context, string) (source.Object, error)
	// Factory returns only trusted host-bound capabilities. No SDK/worker may
	// supply this object, a site URL, credentials, origin grant or TLS policy.
	Factory   func(context.Context, source.Object) (BoundSite, error)
	Now       func() time.Time
	RandomInt func(int, int) (int, error)
	Sleep     func(context.Context, time.Duration) error
	Timezone  string
}
type Controller struct {
	Journal      Journal
	Binding      Binding
	DigestKey    [32]byte
	Dependencies Dependencies
}

func decode(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return nil
	}
	return source.Decode(raw, target)
}
func NewRuntime(timezone string) (json.RawMessage, error) {
	if timezone == "" {
		timezone = "UTC"
	}
	engine, err := source.NewAtZone(source.Inventory{}, time.Now, timezone)
	if err != nil {
		return nil, err
	}
	return marshal(engine.Runtime()), nil
}
func (c *Controller) Execute(ctx context.Context, runID string, command Command) (source.Object, error) {
	switch command.Action {
	case "cold_init", "init", "reload", "service", "event", "site_deleted", "api":
	default:
		return nil, ErrConflict
	}
	if c.DigestKey == [32]byte{} {
		return nil, ErrStale
	}
	if c.Journal == nil || runID == "" || c.Binding.Epoch == "" {
		return nil, ErrStale
	}
	release, err := c.Journal.AcquireExecution(ctx, c.Binding)
	if err != nil {
		return nil, err
	}
	defer release()
	now := c.Dependencies.Now
	if now == nil {
		now = time.Now
	}
	zone := c.Dependencies.Timezone
	if zone == "" {
		zone = "UTC"
	}
	head, err := readRunStartHead(ctx, c.Journal, c.Binding, runID)
	if err != nil {
		return nil, err
	}
	if head.RuntimeEpoch != c.Binding.Epoch && command.Action != "cold_init" && !(command.Action == "init" && head.Fresh) && !head.Exists {
		return nil, ErrStale
	}
	var inventory source.Inventory
	// Start still validates the immutable action/plan. A replay must not depend
	// on a current inventory that its frozen baseline cannot use.
	base := head.Baseline
	if !head.Exists {
		if c.Dependencies.Inventory == nil {
			return nil, ErrStale
		}
		inventory, err = c.Dependencies.Inventory(ctx)
		if err != nil {
			return nil, err
		}
		inventory = publicInventory(inventory)
		base.Inventory, base.Timezone = marshal(inventory), zone
	}
	plan := marshal(command)
	run, err := c.Journal.Start(ctx, c.Binding, runID, command.Action, plan, base)
	if err != nil {
		return nil, err
	}
	if run.Phase == "failed_source" {
		raw, failure := ReplaySourceFailure(ctx, c.Journal, c.Binding, run, c.Dependencies.Source)
		var out source.Object
		if len(raw) > 0 {
			if decodeErr := decode(raw, &out); decodeErr != nil {
				return nil, decodeErr
			}
		}
		return out, failure
	}
	if run.Phase == "completed" {
		if err := verifyCompletedSourceResults(ctx, c.Binding, run, c.Dependencies.Source); err != nil {
			return nil, err
		}
		var out source.Object
		err = decode(run.Result, &out)
		return out, err
	}
	zone = run.Baseline.Timezone
	if zone == "" {
		return nil, ErrStore
	}
	if err = decode(run.Baseline.Inventory, &inventory); err != nil {
		return nil, err
	}
	var runtime source.Object
	var rawHistory map[string]json.RawMessage
	if decode(run.Baseline.Runtime, &runtime) != nil || decode(run.Baseline.History, &rawHistory) != nil {
		return nil, ErrStore
	}
	history := source.Object{}
	for key, raw := range rawHistory {
		var value any
		if decode(raw, &value) != nil {
			return nil, ErrStore
		}
		history[key] = value
	}
	execution := &Execution{Store: c.Journal, Binding: c.Binding, RunID: runID, Epoch: run.Epoch, DigestKey: c.DigestKey}
	main := execution.Lane("main", "")
	clock := execution.Lane("main-clock", "")
	engine, err := source.NewAtZone(inventory, clock.Clock(ctx, now), zone)
	if err != nil {
		return nil, err
	}
	if err = engine.Restore(runtime, history); err != nil {
		return nil, err
	}
	engine.Inventory = inventory
	effect := func(e source.Effect) error {
		mutation := Mutation{Kind: e.Kind, Key: e.Key, Value: marshal(e.Value)}
		switch e.Kind {
		case "config", "history.put", "history.delete", "scheduler_job", "scheduler_remove":
		case "load_modules", "scheduler_new", "scheduler_print", "scheduler_start", "scheduler_stop":
			mutation.Kind = ""
		default:
			return ErrConflict
		}
		if e.Kind == "scheduler_job" {
			mutation.Key = "once:" + itoa(int(c.Binding.Generation)) + ":" + itoa(int(c.Binding.Revision))
		}
		return main.Effect(ctx, e.Kind, e.Key, e.Value, mutation)
	}
	engine.Effects = effect
	hooks := source.RunHooks{VisitScoped: func(ctx context.Context, site source.Object, operation string, loaded bool, index int) (source.Result, error) {
		if c.Dependencies.Factory == nil {
			return source.Result{}, source.ErrAdapterNotReady
		}
		bound, err := c.Dependencies.Factory(ctx, site)
		privateSite, h := bound.Site, bound.Hooks
		if site["id"] == nil || !bytes.Equal(marshal(privateSite["id"]), marshal(site["id"])) {
			return source.Result{}, ErrStale
		}
		if err != nil {
			return source.Result{}, err
		}
		guard := main.digest("site-operation", struct {
			ID        any
			Operation string
		}{site["id"], operation})
		lane := execution.Lane(operation+":"+itoa(index), guard)
		if bound.Revision == "" {
			return source.Result{}, lane.stop(ErrStale)
		}
		bindingID := lane.digest("credential-and-transport-binding", struct {
			Site     source.Object
			Revision string
		}{privateSite, bound.Revision})
		if err := lane.Effect(ctx, "site.binding", "", bindingID, Mutation{}); err != nil {
			return source.Result{}, err
		}
		h.HTTP = HTTP{lane, h.HTTP}
		if h.MTeam != nil {
			h.MTeam = MTeam{lane, h.MTeam}
		}
		if h.Browser != nil {
			h.Browser = Browser{lane, h.Browser}
		}
		if h.OCR != nil {
			h.OCR = lane.OCR(ctx, h.OCR)
		}
		h.Now = lane.Clock(ctx, now)
		if h.Choose == nil {
			h.Choose = defaultChoose
		}
		h.Choose = lane.Choose(ctx, h.Choose)
		sleep := c.Dependencies.Sleep
		if sleep == nil {
			sleep = func(ctx context.Context, d time.Duration) error {
				timer := time.NewTimer(d)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-timer.C:
					return nil
				}
			}
		}
		h.Sleep = lane.Sleep(ctx, now, sleep)
		if h.Tjupt != nil {
			deps := *h.Tjupt
			deps.Cache = Cache{Lane: lane, Key: "tjupt"}
			if deps.Public != nil {
				deps.Public = Public{lane, deps.Public}
			}
			h.Tjupt = &deps
		}
		h.Stats = func(ctx context.Context, domain string, success bool, seconds int) error {
			if lane.Fault() != nil {
				return lane.Fault()
			}
			return lane.StatisticClock(ctx, domain, success, seconds, now)
		}
		result, err := (source.SiteVisitor{Adapters: source.InstalledAdapterRegistry(), Hooks: h}).Visit(ctx, privateSite, operation, loaded)
		if lane.Fault() != nil {
			return source.Result{}, lane.Fault()
		}
		return source.Result{SiteName: result.SiteName, Message: result.Message, Success: result.Success, LoggedIn: result.LoggedIn}, err
	}, Emit: func(ctx context.Context, kind string, payload source.Object) error {
		return main.Source(ctx, "source.emit", source.Object{"event": kind, "data": payload}, c.Dependencies.Source)
	}, Notify: func(ctx context.Context, payload source.Object) error {
		return main.Source(ctx, "source.notify", payload, c.Dependencies.Source)
	}}
	keys := func() []string {
		keys := []string{}
		for _, key := range run.Baseline.Keys {
			if _, exists := engine.Data[key]; exists {
				keys = append(keys, key)
			}
		}
		for _, event := range engine.Trace {
			if event["op"] == "put" {
				key, _ := event["key"].(string)
				found := false
				for _, old := range keys {
					if old == key {
						found = true
					}
				}
				if !found {
					keys = append(keys, key)
				}
			}
		}
		return keys
	}
	var apiResult source.Object
	switch command.Action {
	case "api":
		lookup := func(ctx context.Context, domain string) (source.Object, error) {
			type observed struct {
				Site  source.Object
				Error string
			}
			raw, err := main.Value(ctx, "api.lookup", domain, func() (json.RawMessage, error) {
				if c.Dependencies.Lookup == nil {
					return marshal(observed{Error: "site lookup not ready"}), nil
				}
				site, err := c.Dependencies.Lookup(ctx, domain)
				out := observed{Site: publicSite(site)}
				if err != nil {
					out.Error = err.Error()
				}
				return marshal(out), nil
			})
			if err != nil {
				return nil, err
			}
			var out observed
			if decode(raw, &out) != nil {
				return nil, main.stop(ErrStore)
			}
			if out.Error != "" {
				return nil, errors.New(out.Error)
			}
			return out.Site, nil
		}
		apiResult, err = source.SigninByDomain(ctx, command.Domain, source.DomainAPIHooks{KeyAccepted: command.APIKeyAccepted, Lookup: lookup, Visit: func(ctx context.Context, ref source.Object) (source.Result, error) {
			return hooks.VisitScoped(ctx, ref, "签到", engine.SchemaLoaded, 0)
		}})
	case "init":
		err = engine.Init(command.Config)
	case "reload", "cold_init":
		if command.Action == "reload" {
			err = engine.Stop()
		}
		if err == nil {
			oldData := engine.Data
			engine, err = source.NewAtZone(inventory, clock.Clock(ctx, now), zone)
			if err == nil {
				engine.Data = oldData
				engine.Effects = effect
				config := command.Config
				if command.Action == "cold_init" {
					var value any
					decode(run.Baseline.Config, &value)
					config = value
				}
				err = engine.Init(config)
			}
		}
	case "service":
		err = engine.SignIn(ctx, nil, source.EntryHooks{Run: hooks, HistoryKeys: keys})
	case "event":
		if source.Truth(engine.Enabled) {
			err = engine.SignIn(ctx, command.Event, source.EntryHooks{Run: hooks, HistoryKeys: keys})
		}
	case "site_deleted":
		if source.Truth(engine.Enabled) {
			err = engine.SiteDeleted(command.DeletedID)
		}
	default:
		err = ErrConflict
	}
	if err == nil && (command.Action == "init" || command.Action == "reload" || command.Action == "cold_init") {
		choose := c.Dependencies.RandomInt
		if choose == nil {
			choose = defaultChoose
		}
		services, serviceErr := engine.Services(execution.Lane("service-entropy", "").Choose(ctx, choose), zone)
		err = serviceErr
		if err == nil {
			err = main.Effect(ctx, "service.definitions", "", services, Mutation{Kind: "services", Value: marshal(services)})
		}
	}
	if main.Fault() != nil {
		err = main.Fault()
	}
	if clock.Fault() != nil {
		err = clock.Fault()
	}
	phase := "completed"
	if err != nil {
		phase = "interrupted"
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	result := engine.Snapshot()
	if apiResult != nil {
		result = apiResult
	}
	var finishErr error
	execution.mu.Lock()
	failure, failureIdentity := execution.sourceFailure, execution.sourceFailureIdentity
	execution.mu.Unlock()
	if failure != nil && err == failure {
		finishErr = c.Journal.FinishSourceFailure(finishCtx, c.Binding, runID, marshal(engine.Runtime()), marshal(result), failureIdentity)
		if finishErr != nil {
			return result, finishErr
		}
	} else {
		finishErr = c.Journal.Finish(finishCtx, c.Binding, runID, marshal(engine.Runtime()), marshal(result), phase)
	}
	if err == nil {
		err = finishErr
	}
	return result, err
}
func publicInventory(i source.Inventory) source.Inventory {
	clean := func(rows []source.Object) []source.Object {
		out := []source.Object{}
		for _, row := range rows {
			out = append(out, publicSite(row))
		}
		return out
	}
	return source.Inventory{Configured: clean(i.Configured), Indexers: clean(i.Indexers), Custom: clean(i.Custom), CustomEnabled: i.CustomEnabled}
}

func publicSite(row source.Object) source.Object {
	if row == nil {
		return nil
	}
	copy := source.Object{}
	for _, key := range []string{"id", "name", "public"} {
		if v, ok := row[key]; ok {
			copy[key] = v
		}
	}
	return copy
}
