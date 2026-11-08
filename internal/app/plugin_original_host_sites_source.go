package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/sourceindexer"
)

// Only public helper metadata survives in this process-local source singleton.
// Credentials are reacquired from the current canonical site for every refresh.
type originalSourceSites struct {
	mu       sync.Mutex
	registry *sourceindexer.Registry
	limits   map[string]originalSourceSiteLimit
}

func (a *App) originalSourceSiteRegistryLocked() *sourceindexer.Registry {
	state := &a.pluginOriginalSourceSites
	if state.registry == nil {
		var templates []map[string]any
		if json.Unmarshal([]byte(originalSourceSiteMetadata), &templates) != nil {
			panic("invalid embedded source site metadata")
		}
		state.registry = sourceindexer.New(templates, originalPluginServerUserAgent())
	}
	return state.registry
}

// Reuses the existing registry's explicit custom-template hook. Only consumed
// public fields enter the shared cache; this is not a worker RPC or URL grant.
func (a *App) addOriginalSourceSiteIndexer(domain string, template map[string]any) bool {
	state := &a.pluginOriginalSourceSites
	state.mu.Lock()
	defer state.mu.Unlock()
	safe := originalSourceSiteSafeFields(template)
	if v, ok := template["ext_domains"]; ok {
		safe["ext_domains"] = v
	}
	return a.originalSourceSiteRegistryLocked().Add(domain, safe)
}
func originalSourceSiteSafeFields(fields map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"id", "name", "domain", "url", "is_active", "schema", "public", "proxy", "render", "timeout"} {
		if v, ok := fields[key]; ok {
			out[key] = v
		}
	}
	return out
}

type originalSourceSiteRow struct {
	site       SiteConfig
	fields     map[string]any
	sourceID   int64
	provenance string
}

func (a *App) originalSourceSiteRows(ctx context.Context) ([]originalSourceSiteRow, error) {
	out := []originalSourceSiteRow{}
	for _, site := range a.Settings().Sites {
		identity, err := a.Store.nativeSiteByAppID(ctx, site.ID)
		if err != nil {
			return nil, err
		}
		origin, err := nativeConfiguredSiteOrigin(site)
		if err != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
			return nil, siteIdentityError()
		}
		row := originalSourceSiteRow{site: site, sourceID: identity.SourceID, fields: map[string]any{"id": identity.SourceID, "name": site.Name, "domain": siteStatisticDomain(site), "url": site.BaseURL, "is_active": site.Enabled, "pri": site.Priority, "proxy": site.Proxy, "render": site.Render, "public": site.Public, "timeout": site.Timeout, "ua": site.UserAgent}}
		source, mappedID, proof, err := a.Store.nativeSourceSiteFields(ctx, site, identity)
		if err != nil {
			return nil, err
		}
		row.sourceID = mappedID
		row.fields["id"] = mappedID
		if source != nil {
			row.sourceID = source.ID
			row.provenance = proof
			row.fields = source.row()
			delete(row.fields, "cookie")
			delete(row.fields, "apikey")
			delete(row.fields, "token")
		}
		out = append(out, row)
	}
	// Fixed source SQLite schema has only its domain index; actual WHERE
	// is_active plan is SCAN site, hence integer-primary-key traversal. This
	// is bound source behavior, not a general no-ORDER-BY SQL guarantee.
	sort.SliceStable(out, func(i, j int) bool { return out[i].sourceID < out[j].sourceID })
	return out, nil
}
func originalSourceSiteProjection(fields map[string]any, rows []originalSourceSiteRow) (*plugins.SiteSourceIndexerV1, error) {
	if fields == nil {
		return nil, nil
	}
	out := &plugins.SiteSourceIndexerV1{}
	// Source id comes from host-owned registry/DB projections, never worker input.
	for _, row := range rows {
		if fields["id"] == row.sourceID && fields["url"] == row.fields["url"] {
			id := row.site.ID
			out.SiteID = &id
			break
		}
	}
	var err error
	out.Fields, err = json.Marshal(originalSourceSiteSafeFields(fields))
	return out, err
}
func (a *App) originalSourceSiteIndexers(ctx context.Context, domain *string) (*plugins.SiteSourceIndexerV1, []plugins.SiteSourceIndexerV1, error) {
	rows, err := a.originalSourceSiteRows(ctx)
	if err != nil {
		return nil, nil, err
	}
	state := &a.pluginOriginalSourceSites
	state.mu.Lock()
	defer state.mu.Unlock()
	registry := a.originalSourceSiteRegistryLocked()
	if domain != nil {
		var db map[string]any
		for _, row := range rows {
			if row.fields["domain"] == *domain {
				db = row.fields
				break
			}
		}
		out, err := originalSourceSiteProjection(registry.Get(*domain, db), rows)
		return out, nil, err
	}
	active := []map[string]any{}
	for _, row := range rows {
		if sourceNativeActive(row.fields["is_active"]) {
			active = append(active, row.fields)
		}
	}
	out := []plugins.SiteSourceIndexerV1{}
	for _, fields := range registry.List(active) {
		row, err := originalSourceSiteProjection(fields, rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, *row)
	}
	return nil, out, nil
}
func (a *App) originalSourceSiteStopped() bool {
	state := &a.pluginOriginalBackground
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.closed
}
func (a *App) originalSourceSiteRefresh(ctx context.Context, scope originalBaseCallScope, id string) (json.RawMessage, error) {
	if id == "" {
		return nil, bad("INVALID_PLUGIN_VALUE", "刷新站点须指定准确ID")
	}
	ctx = allowDisabledSiteRequests(ctx)
	site, err := a.siteForRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	expected := a.managedSiteKey(site)
	var firstDenied error
	var denialMu sync.Mutex
	guard := func(requestCtx context.Context) error {
		denialMu.Lock()
		defer denialMu.Unlock()
		if firstDenied != nil {
			return firstDenied
		}
		err := scope.validate(requestCtx)
		if err == nil {
			current, e := a.siteForRequest(requestCtx, id)
			if e != nil || a.managedSiteKey(current) != expected {
				err = bad("SITE_CHANGED", "站点设置已变化，请重新刷新")
			}
		}
		if err == nil {
			identity, e := a.Store.nativeSiteByAppID(requestCtx, id)
			origin, e2 := nativeConfiguredSiteOrigin(site)
			if e != nil || e2 != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
				err = siteIdentityError()
			}
		}
		if err != nil {
			firstDenied = err
		}
		return err
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	current := json.RawMessage(`null`)
	sourceDomain := sourceindexer.Domain(site.BaseURL)
	hooks := &siteCollectionHooks{OrdinaryEvent: true, SourceDomain: &sourceDomain, SourceResult: &current, RequestGuard: guard, Guard: func(*sql.Tx) error { denialMu.Lock(); defer denialMu.Unlock(); return firstDenied }}
	snapshot, err := a.collectManagedSiteObserved(ctx, id, "", hooks)
	if err != nil {
		return nil, err
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	guarded := scope
	guarded.validate = guard
	if err = a.originalHostSiteRefreshMessages(ctx, guarded, site, snapshot); err != nil {
		return nil, err
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	return current, nil
}

// This is the existing public SiteChain owner: caller supplies no loop, site
// configs or credentials. Stop checks and source effects are ordered per site.
func (a *App) originalSourceSiteRefreshAll(ctx context.Context, scope originalBaseCallScope) (json.RawMessage, error) {
	selected := map[string]string{}
	for _, site := range a.Settings().Sites {
		if site.Enabled {
			selected[site.ID] = a.managedSiteKey(site)
		}
	}
	_, indexers, err := a.originalSourceSiteIndexers(ctx, nil)
	if err != nil {
		return nil, err
	}
	type result struct {
		name string
		data json.RawMessage
	}
	ordered := []result{}
	slots := map[string]int{}
	for _, indexer := range indexers {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if a.originalSourceSiteStopped() {
			return json.RawMessage(`null`), nil
		}
		if err = scope.validate(ctx); err != nil {
			return nil, err
		}
		if indexer.SiteID == nil {
			return nil, siteIdentityError()
		}
		var fields struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(indexer.Fields, &fields) != nil {
			return nil, errors.New("invalid source site metadata")
		}
		currentID := *indexer.SiteID
		selectedScope := scope
		selectedScope.validate = func(callCtx context.Context) error {
			if err := scope.validate(callCtx); err != nil {
				return err
			}
			current, err := a.siteForRequest(allowDisabledSiteRequests(callCtx), currentID)
			if err != nil || !current.Enabled || selected[currentID] == "" || a.managedSiteKey(current) != selected[currentID] {
				return bad("SITE_CHANGED", "站点设置已变化，请重新刷新")
			}
			return nil
		}
		data, err := a.originalSourceSiteRefresh(ctx, selectedScope, currentID)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(data, []byte("null")) {
			continue
		}
		if slot, ok := slots[fields.Name]; ok {
			ordered[slot].data = data
		} else {
			slots[fields.Name] = len(ordered)
			ordered = append(ordered, result{fields.Name, data})
		}
	}
	if err = scope.validate(ctx); err != nil {
		return nil, err
	}
	if len(ordered) > 0 {
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if err = recordDomainEventTx(ctx, tx, DomainEvent{EventID: domainEventID("site.refreshed", id("source_refresh_all_")), Type: "site.refreshed", SiteID: "*"}); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		a.emitStandardSiteRefreshed(ctx, "*")
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, row := range ordered {
		if i > 0 {
			out.WriteByte(',')
		}
		name, _ := json.Marshal(row.name)
		out.Write(name)
		out.WriteByte(':')
		out.Write(row.data)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
func (a *App) originalHostSourceSitesResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	switch method {
	case plugins.SitesSourceAccountsCapability:
		var in plugins.SiteAccountHostRequestV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		return a.originalHostSiteAccountsMode(ctx, scope, in, true)
	case plugins.SitesSourceIndexerCapability:
		var in plugins.SiteSourceIndexerRequestV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		row, _, err := a.originalSourceSiteIndexers(ctx, &in.Domain)
		if err != nil {
			return nil, err
		}
		if err = scope.validate(ctx); err != nil {
			return nil, err
		}
		return json.Marshal(plugins.SiteSourceIndexerResultV1{SchemaVersion: 1, Indexer: row})
	case plugins.SitesSourceIndexersCapability:
		var in struct{}
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		_, rows, err := a.originalSourceSiteIndexers(ctx, nil)
		if err != nil {
			return nil, err
		}
		if err = scope.validate(ctx); err != nil {
			return nil, err
		}
		return json.Marshal(plugins.SiteSourceIndexersResultV1{SchemaVersion: 1, Items: rows})
	case plugins.SitesSourceRefreshCapability, plugins.SitesSourceRefreshAllCapability:
		if err := originalHostNestedModuleScope(scope); err != nil {
			return nil, err
		}
		var data json.RawMessage
		var err error
		if method == plugins.SitesSourceRefreshCapability {
			var in plugins.SiteHostRefreshV1
			if err = originalResourceDecode(raw, &in); err != nil {
				return nil, err
			}
			data, err = a.originalSourceSiteRefresh(ctx, scope, in.ID)
		} else {
			var in struct{}
			if err = originalResourceDecode(raw, &in); err != nil {
				return nil, err
			}
			data, err = a.originalSourceSiteRefreshAll(ctx, scope)
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(plugins.SiteSourceRefreshResultV1{SchemaVersion: 1, Data: data})
	}
	return nil, bad("PLUGIN_CAPABILITY_DENIED", "未知源站点操作")
}

// datetime.now() in the fixed source is process-local, independently of the
// tzlocal/APScheduler resolver. Persist both strings before any cold TZ change.
func sourceSiteStorageClock(at time.Time) (string, *string) {
	local := at.In(time.Local)
	clock := local.Format("15:04:05")
	return local.Format("2006-01-02"), &clock
}
