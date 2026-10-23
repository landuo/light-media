package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins/builtin"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	iyuudownload "light-media/internal/plugins/iyuuautoseed/native"
	"light-media/internal/plugins/schedule"
)

// IYUU execution owner shares the application SDK, queue, and lifecycle locks.
type pluginIYUURuntime struct {
	mu              sync.Mutex
	owners          map[string]*pluginIYUUOwner
	loopbackAPI     string
	pollSleep       func(context.Context, time.Duration) error
	afterCheckpoint func() error // native owned fault seam; never worker data
}
type pluginIYUUOwner struct {
	gate       chan struct{}
	generation int64
	journal    *iyuu.Journal
	limiters   map[string]*iyuu.SiteRateLimiter
}

func (a *App) iyuuOwner(ctx context.Context, in PluginInstallation) (*pluginIYUUOwner, error) {
	if in.PluginID != "IYUUAutoSeed" || in.Version != "2.19" || !in.Installed || in.Generation < 1 {
		return nil, bad("IYUU_OWNER_DENIED", "IYUU安装身份无效")
	}
	p := &a.pluginIYUU
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owners == nil {
		p.owners = map[string]*pluginIYUUOwner{}
	}
	if o := p.owners[in.InstanceID]; o != nil {
		if o.generation != in.Generation {
			if len(o.gate) != 0 {
				return nil, iyuu.ErrReplayDiverged
			}
			delete(p.owners, in.InstanceID)
		} else {
			return o, nil
		}
	}
	j, e := iyuu.AttachAnchoredJournal(ctx, a.Store.DB, a.pluginStore.iyuuJournalKey(in.InstanceID, in.Generation), iyuuRegistryAuthority{a.pluginStore, in.InstanceID, in.Generation})
	if e != nil {
		return nil, e
	}
	o := &pluginIYUUOwner{gate: make(chan struct{}, 1), generation: in.Generation, journal: j, limiters: map[string]*iyuu.SiteRateLimiter{}}
	p.owners[in.InstanceID] = o
	return o, nil
}
func (o *pluginIYUUOwner) lock(ctx context.Context) error {
	select {
	case o.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *pluginIYUUOwner) unlock() { <-o.gate }
func iyuuDeferredSpec(run PluginRun) (string, json.RawMessage, error) {
	var method string
	var payload json.RawMessage
	_, e := iyuu.Handle(context.Background(), "invoke", run.Input, func(_ context.Context, m string, b json.RawMessage) (json.RawMessage, error) {
		if method != "" {
			return nil, errors.New("multiple IYUU capabilities")
		}
		method = m
		payload = append(json.RawMessage{}, b...)
		return json.RawMessage(`{}`), nil
	})
	return method, payload, e
}

// Resolve source numeric IDs using the authenticated native map. UI labels and
// mutable slice indexes never become IDs, and no read allocates a mapping.
func (a *App) iyuuFormInventory(ctx context.Context) (iyuu.FormInventory, []any, error) {
	inventory := iyuu.FormInventory{}
	ids := []any{}
	settings := a.Settings()
	sites := append([]SiteConfig{}, settings.Sites...)
	sort.SliceStable(sites, func(i, j int) bool { return sites[i].Priority < sites[j].Priority })
	for _, site := range sites {
		id, e := a.Store.nativeSiteByAppID(ctx, site.ID)
		if e != nil {
			return inventory, nil, e
		}
		origin, e := nativeConfiguredSiteOrigin(site)
		if e != nil || id.State != "active" || id.CanonicalOrigin != origin {
			return inventory, nil, siteIdentityError()
		}
		source := json.Number(itoaSourceSite(id.SourceID))
		inventory.Sites = append(inventory.Sites, iyuu.SiteOption{ID: source, Name: site.Name})
		ids = append(ids, source)
	}
	custom, e := a.iyuuCustomSites(ctx)
	if e != nil {
		return inventory, nil, e
	}
	for _, fields := range custom {
		id := fields["id"].(string)
		name, _ := fields["name"].(string)
		inventory.CustomSites = append(inventory.CustomSites, iyuu.SiteOption{ID: id, Name: name + " [自定义]"})
		ids = append(ids, id)
	}
	for _, downloader := range settings.Downloaders {
		inventory.Downloaders = append(inventory.Downloaders, downloader.Name)
	}
	return inventory, ids, nil
}
func itoaSourceSite(id int64) string { b, _ := json.Marshal(id); return string(b) }
func iyuuSourceMap(raw []byte) (map[string]any, error) {
	var fields map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(&fields); e != nil || fields == nil {
		return nil, errors.New("invalid native IYUU config")
	}
	return fields, nil
}

func (a *App) pluginIYUUHost(ctx context.Context, in PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if in.PluginID != "IYUUAutoSeed" || in.Version != "2.19" {
		return nil, bad("IYUU_OWNER_DENIED", "IYUU安装身份无效")
	}
	if method == iyuu.FormCapability {
		if run != nil || string(raw) != "{}" {
			return nil, bad("IYUU_OWNER_DENIED", "IYUU表单权限无效")
		}
		inventory, _, e := a.iyuuFormInventory(ctx)
		if e != nil {
			return nil, e
		}
		form, defaults, e := iyuu.Form(inventory)
		if e != nil {
			return nil, e
		}
		return json.Marshal(map[string]any{"conf": form, "model": defaults})
	}
	if _, e := a.pluginStore.readSiteReferenceProtocol(ctx, a.Store.DB, in); e != nil {
		return nil, e
	}
	if run == nil || a.requireDeferredExecution(ctx, run, method) != nil {
		return nil, bad("IYUU_DEFERRED_REQUIRED", "IYUU操作缺少当前宿主执行授权")
	}
	expected, payload, e := iyuuDeferredSpec(*run)
	if e != nil || method != expected || !bytes.Equal(raw, payload) {
		return nil, bad("IYUU_RUN_DENIED", "IYUU操作不属于当前执行")
	}
	ctx = context.WithValue(ctx, iyuuAdmissionKey{}, iyuuAdmissionFromRun(*run, method))
	owner, e := a.iyuuOwner(ctx, in)
	if e != nil {
		return nil, e
	}
	held, ok := ctx.Value(iyuuDeferredOwnerKey{}).(iyuuDeferredOwner)
	call, _ := ctx.Value(pluginDeferredExecutionKey{}).(*pluginDeferredCall)
	if !ok || held.owner != owner || held.call == nil || held.call != call || held.call.a != a || held.call.run.ID != run.ID || held.call.installation.Generation != in.Generation {
		return nil, bad("IYUU_DEFERRED_REQUIRED", "IYUU操作缺少当前宿主串行执行授权")
	}
	_, existing, e := owner.journal.ReadRunIdentity(ctx, run.InstanceID, uint64(run.Generation), run.ID)
	if e != nil {
		return nil, e
	}
	if !existing {
		unknown, e := owner.journal.HasUnconfirmed(ctx, run.InstanceID, uint64(run.Generation))
		if e != nil {
			return nil, e
		}
		if unknown {
			return nil, iyuu.ErrUnconfirmed
		}
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
	a.importMu.Lock()
	settings := a.Settings()
	a.pluginLifecycleMu.Unlock()
	defer a.importMu.Unlock()
	defer a.downloadMu.Unlock()
	ctx = a.iyuuSourceLogContext(ctx, in, *run, stored.Value, settings)
	if e = a.pluginStore.iyuuAcceptRun(ctx, *run); e != nil {
		return nil, e
	}
	if method == iyuu.ConfigureCapability {
		return a.configureIYUU(ctx, owner, run, stored.Value)
	}
	return a.executeIYUU(ctx, owner, in, run, raw, settings)
}

func (a *App) configureIYUU(ctx context.Context, owner *pluginIYUUOwner, run *PluginRun, raw []byte) (json.RawMessage, error) {
	_, ids, e := a.iyuuFormInventory(ctx)
	if e != nil {
		return nil, e
	}
	input, e := iyuuSourceMap(raw)
	if e != nil {
		return nil, e
	}
	cfg := originalIYUUConfigurationForInit(ctx)
	init, e := cfg.InitContext(ctx, input, ids)
	captureOriginalNativeState(ctx, cfg)
	if applied, snapshotErr := cfg.Snapshot(); snapshotErr == nil {
		captureOriginalAppliedConfiguration(ctx, applied)
	}
	if e != nil {
		return nil, e
	}
	zone, e := iyuuScheduleTimezone()
	if e != nil {
		return nil, e
	}
	// Scheduler.update_plugin_job separately calls the original get_service.
	jobs := init.Jobs
	at := time.Now()
	definitions := []pluginScheduleDefinition{}
	for _, job := range jobs {
		handler := job.ID
		if job.Kind == "cron" {
			handler = "auto_seed"
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
		definitions = append(definitions, pluginScheduleDefinition{ID: "IYUUAutoSeed." + job.ID, Name: name, Handler: handler, Trigger: trigger})
	}
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
	effect := func(ctx context.Context, tx *sql.Tx, current PluginRun) error {
		if e := a.pluginStore.configureEventSubscriptionTx(ctx, tx, current, "site_deleted", []string{"site.deleted"}, 0, cfg.State()); e != nil {
			return e
		}
		checkpoint := iyuu.RuntimeCheckpoint{Core: iyuu.State{Config: snapshot, Queue: map[string][]string{}, Attempts: []iyuu.Attempt{}}, Helper: iyuu.HelperState{}, Authorities: map[string][]string{}, Revision: uint64(current.ConfigRevision + 1), Run: current.ID, Identity: iyuu.RunIdentity{Instance: current.InstanceID, Generation: uint64(current.Generation), Revision: uint64(current.ConfigRevision), Fingerprint: tokenHash(string(current.Input)), Run: current.ID}}
		result, e := iyuuSourceResult(current, checkpoint)
		if e != nil {
			return e
		}
		if e = a.pluginStore.iyuuCompleteRunTx(ctx, tx, current, result, int64(checkpoint.Revision)); e != nil {
			return e
		}
		return owner.journal.WriteCheckpointTx(ctx, tx, current.InstanceID, uint64(current.Generation), checkpoint)
	}
	if e = a.pluginStore.configurePluginSchedulesWithEffect(ctx, run, "iyuu", definitions, at, patch, effect); e != nil {
		return nil, e
	}
	return json.RawMessage(`{"configured":true}`), nil
}

// Administrative publication waits for the exact native owner gate, then the
// existing App implementation obtains its usual lifecycle locks. This wrapper
// never retains lifecycle/process/file locks while waiting for an active run.
func (a *App) pluginAPI(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	if originalPluginConfigurationRoute(path, r.Method) {
		return a.originalPluginConfigurationHTTP(w, r, path)
	}
	if parts := strings.Split(path, "/"); r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "restore-reviewed" && pluginStorageID.MatchString(parts[0]) {
		return a.pluginRestoreReviewHTTP(w, r, parts[0])
	}
	if parts := strings.Split(path, "/"); r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "update" && pluginStorageID.MatchString(parts[0]) {
		return a.pluginUpdateHTTP(w, r, parts[0])
	}
	if parts := strings.Split(path, "/"); r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "reset" && pluginStorageID.MatchString(parts[0]) {
		return a.pluginResetHTTP(w, r, path, parts[0])
	}
	return a.pluginAPIWithoutResetBarrier(w, r, path)
}

func (a *App) pluginAPIWithoutResetBarrier(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	// Clone reads a source snapshot but never publishes a source lifecycle change.
	if parts := strings.Split(path, "/"); r.Method == "POST" && len(parts) == 2 && parts[1] == "clone" {
		return a.pluginCloneHTTP(w, r, parts[0])
	}
	if r.Method != "GET" {
		instance := strings.Split(path, "/")[0]
		if in, e := a.pluginStore.Installation(r.Context(), instance); e == nil && isTorrentTransfer(in.PluginID) && in.Installed {
			return a.transferAPIWithBarrier(w, r, path, in)
		}
		if in, e := a.pluginStore.Installation(r.Context(), instance); e == nil && in.PluginID == "IYUUAutoSeed" && in.Installed {
			if parts := strings.Split(path, "/"); r.Method == "POST" && len(parts) == 2 && (parts[1] == "reset" || parts[1] == "uninstall") {
				return a.iyuuLifecycleHTTP(r.Context(), in, parts[1])
			}
			owner, e := a.iyuuOwner(r.Context(), in)
			if e != nil {
				return nil, e
			}
			if e = owner.lock(r.Context()); e != nil {
				return nil, e
			}
			defer owner.unlock()
			unknown, e := owner.journal.HasUnconfirmed(r.Context(), in.InstanceID, uint64(in.Generation))
			if e != nil {
				return nil, e
			}
			if unknown {
				return nil, iyuu.ErrUnconfirmed
			}
		}
	}
	return a.pluginAPIWithoutIYUUBarrier(w, r, path)
}

func (a *App) executeIYUU(ctx context.Context, owner *pluginIYUUOwner, in PluginInstallation, run *PluginRun, raw json.RawMessage, settings Settings) (json.RawMessage, error) {
	checkpoint, ok, e := owner.journal.ReadCheckpoint(ctx, run.InstanceID, uint64(run.Generation))
	if e != nil {
		return nil, e
	}
	if !ok || checkpoint.Revision != uint64(run.ConfigRevision) {
		return nil, bad("IYUU_INITIALIZATION_REQUIRED", "IYUU配置尚未完成当前版本初始化")
	}
	handler := "auto_seed"
	if run.Action == "service" || run.Action == "once" {
		var input struct {
			ID string `json:"occurrence_id"`
		}
		if !pluginStrictJSON(raw, &input) {
			return nil, bad("IYUU_OCCURRENCE_DENIED", "IYUU任务缺少持久计划")
		}
		definition, e := a.pluginStore.pluginScheduledInvocation(ctx, *run, input.ID)
		if e != nil {
			return nil, e
		}
		if !strings.HasPrefix(definition.ID, "IYUUAutoSeed.") || (definition.Handler != "auto_seed" && definition.Handler != "check_recheck") {
			return nil, bad("IYUU_OCCURRENCE_DENIED", "IYUU任务计划不匹配")
		}
		handler = definition.Handler
	} else if run.Action == "event" {
		handler = "site_deleted"
	} else if run.Action != "run" {
		return nil, bad("IYUU_ACTION_DENIED", "IYUU操作来源无效")
	}
	immutable := *run
	guard := func(ctx context.Context) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		return a.validatePluginHostRun(ctx, in, run, iyuu.ExecuteCapability)
	}
	identity := iyuu.RunIdentity{Instance: run.InstanceID, Generation: uint64(run.Generation), Revision: uint64(run.ConfigRevision), Fingerprint: tokenHash(string(immutable.Input)), Run: run.ID}
	if initial, exists, err := owner.journal.ReadRunInitial(ctx, identity); err != nil {
		return nil, err
	} else if exists {
		decoder := json.NewDecoder(bytes.NewReader(initial))
		decoder.UseNumber()
		if err = decoder.Decode(&checkpoint); err != nil {
			return nil, err
		}
	}
	jr, e := owner.journal.Begin(ctx, identity, checkpoint, guard)
	if e != nil {
		return nil, e
	}
	if _, e = a.Store.DB.ExecContext(ctx, iyuuNoticeSchema); e != nil {
		return nil, e
	}
	if _, e = a.pluginStore.noticePeriods(ctx); e != nil {
		return nil, e
	}
	wroteConfig := false
	jr.LocalObserved = func(op, key string, value any) {
		if op == "config" {
			wroteConfig = true
		}
	}
	jr.LocalRead = func(ctx context.Context, key string) (any, error) {
		if !strings.HasPrefix(key, "history/") {
			return nil, nil
		}
		stored, e := a.pluginStore.Data(ctx, run.InstanceID, strings.TrimPrefix(key, "history/"))
		if errors.Is(e, sql.ErrNoRows) {
			return nil, nil
		}
		if e != nil {
			return nil, e
		}
		var v any
		d := json.NewDecoder(bytes.NewReader(stored.Value))
		d.UseNumber()
		e = d.Decode(&v)
		return v, e
	}
	jr.LocalCommit = func(ctx context.Context, tx *sql.Tx, id iyuu.RunIdentity, op, key string, value any) (func(), error) {
		switch op {
		case "notification-outbox":
			return nil, a.acceptIYUUNoticeTx(ctx, tx, id, key, value)
		case "runtime-checkpoint":
			state, ok := value.(iyuu.RuntimeCheckpoint)
			if !ok {
				return nil, iyuu.ErrReplayDiverged
			}
			updated := *run
			if wroteConfig {
				b, e := json.Marshal(state.Core.Config)
				if e != nil {
					return nil, e
				}
				if e = a.pluginStore.replaceRunConfigTx(ctx, tx, &updated, b); e != nil {
					return nil, e
				}
			}
			if state.Revision != uint64(updated.ConfigRevision) {
				return nil, iyuu.ErrReplayDiverged
			}
			result, e := iyuuSourceResult(updated, state)
			if e != nil {
				return nil, e
			}
			if e = a.pluginStore.iyuuCompleteRunTx(ctx, tx, updated, result, updated.ConfigRevision); e != nil {
				return nil, e
			}
			return func() { *run = updated }, nil

		case "history":
			item := strings.TrimPrefix(key, "history/")
			var revision int64
			e := tx.QueryRowContext(ctx, "SELECT revision FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?", run.InstanceID, item).Scan(&revision)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			b, e := json.Marshal(value)
			if e != nil {
				return nil, e
			}
			_, e = a.pluginStore.saveValueTx(ctx, tx, run.InstanceID, "data", item, revision, b)
			return nil, e
		}
		return nil, nil
	}
	if handler == "site_deleted" {
		var input struct {
			ID string `json:"batch_id"`
		}
		if !pluginStrictJSON(raw, &input) || input.ID == "" {
			return nil, bad("IYUU_EVENT_DENIED", "IYUU站点事件没有绑定批次")
		}
		page, e := a.pluginStore.pluginBatchEvents(ctx, *run, input.ID, 0, 200)
		if e != nil {
			return nil, e
		}
		if !page.Complete {
			return nil, bad("IYUU_EVENT_DENIED", "IYUU站点事件批次超出单次范围")
		}
		cfg, e := iyuu.RestoreConfig(checkpoint.Core.Config)
		if e != nil {
			return nil, e
		}
		for _, event := range page.Events {
			if event.Type != "site.deleted" {
				return nil, bad("IYUU_EVENT_DENIED", "IYUU事件类型不匹配")
			}
			data, e := a.Store.siteDeletedSourceData(ctx, a.Store.DB, event)
			if e != nil {
				return nil, e
			}
			changed, e := cfg.SiteDeleted(data["site_id"])
			if e != nil {
				return nil, e
			}
			if changed {
				snapshot, e := cfg.Snapshot()
				if e != nil {
					return nil, e
				}
				if e = jr.SourceConfig(ctx, snapshot); e != nil {
					return nil, e
				}
			}
		}
		checkpoint.Core.Config, e = cfg.Snapshot()
		if e != nil {
			return nil, e
		}
		checkpoint.Revision = uint64(run.ConfigRevision)
		if wroteConfig {
			checkpoint.Revision++
		}
		checkpoint.Identity = identity
		if e = jr.Checkpoint(ctx, checkpoint); e != nil {
			return nil, e
		}
		return json.RawMessage(`{"completed":true}`), nil
	}
	roots := append([]string{}, settings.AllowedRoots...)
	for _, root := range settings.Roots {
		roots = append(roots, root.Path)
	}
	bindings := []iyuu.NativeBinding{}
	proxyConfig, e := resolveProxyConfig(settings.ProxyHost)
	if e != nil {
		return nil, bad("IYUU_PROXY_INVALID", "全局代理配置无效")
	}
	for _, d := range settings.Downloaders {
		kind := d.Kind
		if kind == "qb" {
			kind = "qbittorrent"
		}
		if kind == "tr" {
			kind = "transmission"
		}
		bindings = append(bindings, iyuu.NativeBinding{Service: iyuu.Service{Ref: d.ID, Name: d.Name, Kind: kind, Inactive: !downloaderEnabled(d)}, Config: iyuudownload.Config{ID: d.ID, Kind: kind, BaseURL: d.BaseURL, Username: d.Username, Password: d.Password}, Options: iyuudownload.IYUUOptions{APIKey: d.APIKey, AutoCategory: d.AutoCategory, Sequential: d.Sequential, FirstLast: d.FirstLast, Sleep: a.pluginIYUU.pollSleep}, AllowedSaveRoots: roots})
	}
	// A configured native daemon may expose its own directory settings; this
	// grants no App filesystem access and performs no directory mapping.
	for i := range bindings {
		bindings[i].AllowDefaultPath = true
	}
	siteBindings := []iyuu.SiteTransportBinding{}
	sites := []iyuu.Site{}
	for _, s := range settings.Sites {
		mapped, e := a.Store.nativeSiteByAppID(ctx, s.ID)
		if e != nil || mapped.State != "active" {
			return nil, siteIdentityError()
		}
		origin, e := nativeConfiguredSiteOrigin(s)
		if e != nil || origin != mapped.CanonicalOrigin {
			return nil, siteIdentityError()
		}
		u, e := url.Parse(origin)
		if e != nil {
			return nil, e
		}
		ua := s.UserAgent
		if ua == "" {
			ua = "Mozilla/5.0"
		}
		uid, e := a.iyuuSiteUserID(ctx, s)
		if e != nil {
			return nil, e
		}
		fields := map[string]any{"uid": uid, "id": json.Number(itoaSourceSite(mapped.SourceID)), "name": s.Name, "url": origin + "/", "cookie": s.Cookie, "apikey": s.APIKey, "ua": ua, "passkey": s.Token, "rss": s.RSS, "proxy": s.Proxy, "domain": u.Host}
		sites = append(sites, iyuu.Site{Ref: s.ID, Fields: fields})
		origins := []string{origin}
		if s.Kind == "mteam" && (u.Hostname() == "kp.m-team.cc" || u.Hostname() == "kp.m-team.io" || u.Hostname() == "api.m-team.cc" || u.Hostname() == "api.m-team.io") {
			apiOrigin := u.Scheme + "://api.m-team." + strings.TrimPrefix(u.Hostname()[strings.Index(u.Hostname(), "m-team."):], "m-team.")
			if u.Port() != "" {
				apiOrigin += ":" + u.Port()
			}
			if apiOrigin != origin {
				origins = append(origins, apiOrigin)
			}
		}
		siteBindings = append(siteBindings, iyuu.SiteTransportBinding{Ref: s.ID, Fields: fields, AllowedOrigins: origins, ProxyConfig: proxyConfig})
		domain, e := iyuu.SourceDomain(origin)
		if e != nil {
			return nil, e
		}
		if owner.limiters[domain] == nil {
			owner.limiters[domain] = iyuu.NewSiteRateLimiter(s.LimitInterval, s.LimitCount, s.LimitSeconds, nil, nil)
		}
	}
	custom, e := a.iyuuCustomSites(ctx)
	if e != nil {
		return nil, e
	}
	for _, fields := range custom {
		id := fields["id"].(string)
		rawURL, _ := fields["url"].(string)
		u, e := url.Parse(rawURL)
		if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, bad("IYUU_CUSTOM_SITE_INVALID", "自定义站点地址无效")
		}
		origin := u.Scheme + "://" + u.Host
		ref := "custom:" + id
		sites = append(sites, iyuu.Site{Ref: ref, Fields: fields})
		siteBindings = append(siteBindings, iyuu.SiteTransportBinding{Ref: ref, Fields: fields, AllowedOrigins: []string{origin}, ProxyConfig: proxyConfig})
	}
	siteHTTP, e := iyuu.NewNativeSiteHTTP(siteBindings, iyuu.SiteTransportOptions{Guard: guard, DefaultUA: "Mozilla/5.0"})
	if e != nil {
		return nil, e
	}
	defer siteHTTP.Close()
	cacheDir := filepath.Join(a.Dir, "plugin-state", "iyuu", run.InstanceID, "torrent-cache")
	if e = os.MkdirAll(cacheDir, 0700); e != nil {
		return nil, e
	}
	cache, e := iyuu.OpenFileTorrentCache(cacheDir)
	if e != nil {
		return nil, e
	}
	defer cache.Close()
	host, e := iyuu.NewNativeHost(iyuu.NativeHostOptions{Context: ctx, Run: jr, Bindings: bindings, Sites: sites, SiteHTTP: siteHTTP, Cache: cache, Limiters: owner.limiters, PendingAuthorities: checkpoint.Authorities, PendingProofs: checkpoint.AuthorityProofs, NoticeAccepted: func(ctx context.Context, key string) error { return a.routeIYUUNotice(ctx, identity, key) }})
	if e != nil {
		return nil, e
	}
	defer host.Close()
	token, ok := checkpoint.Core.Config["token"].(string)
	if !ok || token == "" {
		return nil, bad("IYUU_TOKEN_REQUIRED", "请先配置IYUU凭据")
	}
	api, e := iyuu.NewAPITransport(iyuu.TrustedAPIBinding{Token: token}, iyuu.APITransportOptions{Guard: guard, LoopbackOrigin: a.pluginIYUU.loopbackAPI})
	if e != nil {
		return nil, e
	}
	defer api.Close()
	durable, e := iyuu.NewDurableAPI(ctx, jr, api)
	if e != nil {
		return nil, e
	}
	helper, e := iyuu.RestoreHelper(durable, func() time.Time { return jr.Started }, checkpoint.Helper)
	if e != nil {
		return nil, e
	}
	core, e := iyuu.RestoreCore(checkpoint.Core, host, helper)
	if e != nil {
		return nil, e
	}
	switch handler {
	case "auto_seed":
		e = core.AutoSeed(ctx)
	case "check_recheck":
		e = core.CheckRecheck(ctx)
	}
	if e != nil {
		return nil, e
	}
	checkpoint.Core, e = core.Snapshot()
	if e != nil {
		return nil, e
	}
	checkpoint.Helper, e = helper.Snapshot()
	if e != nil {
		return nil, e
	}
	checkpoint.Authorities, checkpoint.AuthorityProofs = host.AuthoritiesForQueue(checkpoint.Core.Queue)
	checkpoint.Revision = uint64(run.ConfigRevision)
	if wroteConfig {
		checkpoint.Revision++
	}
	checkpoint.Identity = identity
	if e = jr.Checkpoint(ctx, checkpoint); e != nil {
		return nil, e
	}
	if a.pluginIYUU.afterCheckpoint != nil {
		if e = a.pluginIYUU.afterCheckpoint(); e != nil {
			return nil, e
		}
	}
	return json.Marshal(map[string]any{"completed": true, "counts": checkpoint.Core.Counters})
}

// Custom IDs retain their exact string identity even when they spell a native
// number or opaque local ID. Imported/staged peer data never grants HTTP access.
func (a *App) iyuuCustomSites(ctx context.Context) ([]map[string]any, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	peer, ok, e := a.pluginStore.siteControlDependencyPeerTx(ctx, tx, "CustomSites")
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	if !ok || !peer.Desired {
		return out, nil
	}
	var enabled bool
	if json.Unmarshal(peer.Config["enabled"], &enabled) != nil || !enabled {
		return out, nil
	}
	d := json.NewDecoder(bytes.NewReader(peer.Config["sites"]))
	d.UseNumber()
	if d.Decode(&out) != nil {
		return nil, bad("IYUU_CUSTOM_SITE_INVALID", "自定义站点配置无法读取")
	}
	seen := map[string]bool{}
	for _, fields := range out {
		id, ok := fields["id"].(string)
		if !ok || id == "" || seen[id] {
			return nil, bad("IYUU_CUSTOM_SITE_INVALID", "自定义站点标识无效或重复")
		}
		seen[id] = true
	}
	return out, nil
}

func iyuuTypedConfig(raw []byte) (map[string]any, error) {
	fields, e := iyuuSourceMap(raw)
	if e != nil {
		return nil, e
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(raw, &config) != nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "IYUU配置必须为对象")
	}
	if selected, ok := config["sites"]; ok {
		var refs []builtin.SiteReference
		if json.Unmarshal(selected, &refs) != nil {
			return nil, bad("PLUGIN_SITE_REFERENCE_UNMAPPED", "站点选择须为原生数字编号或自定义站点字符串")
		}
	}
	return fields, nil
}

// A UID belongs to the exact current site/credential snapshot. Stale account
// data cannot be repurposed after the site address or credentials change.
func (a *App) iyuuSiteUserID(ctx context.Context, site SiteConfig) (string, error) {
	var snapshot siteSnapshot
	e := a.Store.Get(ctx, "site_stats_latest", site.ID, &snapshot)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	if snapshot.SiteID != site.ID || snapshot.ConfigKey != a.managedSiteKey(site) || snapshot.Stats == nil {
		return "", nil
	}
	return snapshot.Stats.UserID, nil
}
