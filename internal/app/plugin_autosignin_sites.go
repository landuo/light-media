package app

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"light-media/internal/networkproxy"
	source "light-media/internal/plugins/autosignin"
	signinjournal "light-media/internal/plugins/autosignin/durable"
)

// This snapshot never crosses the worker boundary. All three dependencies use
// this one read, including the credentials and transport policy it authorizes.
type autoSignInSiteSnapshot struct {
	Inventory   source.Inventory
	Rows        map[string]source.Object
	Proxy       networkproxy.Config
	Environment []string
	Custom      any
	Identities  []nativeSiteIdentity
	Native      []SiteConfig
	Revision    string
}

// Keep order, spelling and empty values: urllib's two-pass environment rules
// distinguish uppercase and lowercase keys, including host-specific mappings.
func autoSignInProxyEnvironment(environment []string) []string {
	out := []string{}
	for _, entry := range environment {
		name, _, ok := strings.Cut(entry, "=")
		if ok && (strings.HasSuffix(strings.ToLower(name), "_proxy") || name == "REQUEST_METHOD") {
			out = append(out, entry)
		}
	}
	return out
}

func autoSignInSiteKey(id any) (string, error) {
	switch v := id.(type) {
	case string:
		if v != "" && v != "all" {
			return "c:" + v, nil
		}
	case json.Number:
		n, e := v.Int64()
		if e == nil && n > 0 {
			return "n:" + strconv.FormatInt(n, 10), nil
		}
	case int64:
		if v > 0 {
			return "n:" + strconv.FormatInt(v, 10), nil
		}
	case int:
		if v > 0 {
			return "n:" + strconv.Itoa(v), nil
		}
	}
	return "", signinjournal.ErrStale
}
func autoSignInPublicSite(row source.Object) source.Object {
	return source.Object{"id": row["id"], "name": row["name"], "public": row["public"]}
}
func autoSignInClone[T any](v T) T {
	raw, _ := json.Marshal(v)
	var out T
	_ = source.Decode(raw, &out)
	return out
}

// The lock order matches settings publication and site-control snapshots.
func (a *App) readAutoSignInSites(ctx context.Context, owner *pluginAutoSignInOwner) (autoSignInSiteSnapshot, error) {
	out := autoSignInSiteSnapshot{Inventory: source.Inventory{Configured: []source.Object{}, Indexers: []source.Object{}, Custom: []source.Object{}}, Rows: map[string]source.Object{}}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	tx, e := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if owner != nil {
		if e = a.pluginStore.requireSiteReferenceProtocolTx(ctx, tx, owner.binding.Instance); e != nil {
			return out, e
		}
	}
	settings := a.Settings()
	// Capture only routing inputs once. Resolution and requests trust-env use
	// the same ordered snapshot, never live process values inside a bound request.
	environment := os.Environ
	if a.autoSignInEnvironment != nil {
		environment = a.autoSignInEnvironment
	}
	out.Environment = autoSignInProxyEnvironment(environment())
	proxy, e := networkproxy.Resolve(settings.ProxyHost, func(key string) string {
		for i := len(out.Environment) - 1; i >= 0; i-- {
			name, value, ok := strings.Cut(out.Environment[i], "=")
			if ok && name == key {
				return value
			}
		}
		return ""
	})
	if e != nil {
		return out, bad("AUTOSIGNIN_PROXY_UNAVAILABLE", "签到代理配置无法验证")
	}
	out.Proxy = proxy
	sites := append([]SiteConfig(nil), settings.Sites...)
	out.Native = sites
	sort.SliceStable(sites, func(i, j int) bool { return sites[i].Priority < sites[j].Priority })
	for _, site := range sites {
		identity, e := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", site.ID)
		if e != nil {
			return out, e
		}
		origin, e := nativeConfiguredSiteOrigin(site)
		if e != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
			return out, signinjournal.ErrStale
		}
		out.Identities = append(out.Identities, identity)
		row := source.Object{"id": identity.SourceID, "name": site.Name, "url": site.BaseURL, "cookie": site.Cookie, "ua": site.UserAgent, "token": site.Token, "proxy": site.Proxy, "render": site.Render, "public": site.Public, "timeout": site.Timeout}
		key, _ := autoSignInSiteKey(identity.SourceID)
		out.Rows[key] = row
		out.Inventory.Configured = append(out.Inventory.Configured, autoSignInPublicSite(row))
		if site.Enabled {
			out.Inventory.Indexers = append(out.Inventory.Indexers, autoSignInPublicSite(row))
		}
	}
	// Resolve only CustomSites: unrelated AutoSignIn clones are not ambiguous.
	rows, e := tx.QueryContext(ctx, `SELECT instance_id,version,generation,desired_enabled FROM plugin_installations WHERE installed=1 AND plugin_id='CustomSites' ORDER BY instance_id`)
	if e != nil {
		return out, e
	}
	type peer struct {
		ID, Version string
		Generation  int64
		Desired     bool
		Revision    int64
		Config      source.Object
	}
	var peers []peer
	for rows.Next() {
		var p peer
		if e = rows.Scan(&p.ID, &p.Version, &p.Generation, &p.Desired); e != nil {
			rows.Close()
			return out, e
		}
		peers = append(peers, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(peers) > 1 {
		return out, bad("PLUGIN_REFERENCE_AMBIGUOUS", "多个自定义站点实例尚未明确映射")
	}
	if len(peers) == 1 {
		p := peers[0]
		var sealed []byte
		e = tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, p.ID).Scan(&p.Revision, &sealed)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if e == nil {
			raw, e := a.pluginStore.unseal("config", p.ID, "main", sealed)
			if e != nil {
				return out, e
			}
			if source.Decode(raw, &p.Config) != nil || p.Config == nil {
				return out, signinjournal.ErrStale
			}
		}
		out.Custom = source.Object{"peer": p}
		out.Inventory.CustomEnabled = source.Truth(p.Config["enabled"])
		if source.Truth(p.Config["enabled"]) {
			raw, _ := json.Marshal(p.Config["sites"])
			var custom []source.Object
			if source.Decode(raw, &custom) != nil {
				return out, bad("PLUGIN_SITE_REFERENCE_UNMAPPED", "自定义站点配置格式无效")
			}
			for _, row := range custom {
				id, ok := row["id"].(string)
				if !ok {
					return out, signinjournal.ErrStale
				}
				key, e := autoSignInSiteKey(id)
				if e != nil {
					return out, e
				}
				if _, ok := out.Rows[key]; ok {
					return out, signinjournal.ErrStale
				}
				out.Rows[key] = autoSignInClone(row)
				out.Inventory.Custom = append(out.Inventory.Custom, autoSignInPublicSite(row))
			}
		}
	}
	raw, e := json.Marshal(out)
	if e != nil {
		return out, e
	}
	if owner != nil {
		out.Revision = hex.EncodeToString(owner.mac("trusted-site-snapshot/v2:frozen-requests-proxy:tls-system-min12", raw))
	}
	return out, nil
}

func (a *App) autoSignInSiteDependencies(ctx context.Context, owner *pluginAutoSignInOwner, run PluginRun) (signinjournal.Dependencies, error) {
	var deps signinjournal.Dependencies
	if owner == nil || owner.a != a || run.InstanceID != owner.binding.Instance || run.Generation != int64(owner.binding.Generation) {
		return deps, signinjournal.ErrStale
	}
	snapshot, e := a.readAutoSignInSites(ctx, owner)
	if e != nil {
		return deps, e
	}
	backend, e := owner.backend(&run)
	if e != nil {
		return deps, e
	}
	guard := func(ctx context.Context) error {
		if e := backend.View(ctx, owner.binding, func(*signinjournal.State) error { return nil }); e != nil {
			return e
		}
		// API authority is distinct from the durable owner's run/config proof.
		// Validate the original admission outside the plugin/settings locks.
		if run.Action == "api" {
			if e := a.validatePluginHTTPRun(ctx, run); e != nil {
				return e
			}
		}
		current, e := a.readAutoSignInSites(ctx, owner)
		if e != nil {
			return e
		}
		if current.Revision != snapshot.Revision {
			return signinjournal.ErrStale
		}
		return nil
	}
	deps.Inventory = func(context.Context) (source.Inventory, error) { return autoSignInClone(snapshot.Inventory), nil }
	deps.Lookup = func(_ context.Context, domain string) (source.Object, error) {
		var found source.Object
		// The fixed source API uses SitesHelper.get_indexer, not CustomSites.
		for _, ref := range snapshot.Inventory.Indexers {
			key, _ := autoSignInSiteKey(ref["id"])
			row := snapshot.Rows[key]
			address, _ := row["url"].(string)
			if source.DomainKey(address) == domain {
				if found != nil {
					return nil, bad("PLUGIN_REFERENCE_AMBIGUOUS", "站点域名对应多个可信站点")
				}
				found = autoSignInPublicSite(row)
			}
		}
		return autoSignInClone(found), nil
	}
	deps.Factory = func(ctx context.Context, ref source.Object) (signinjournal.BoundSite, error) {
		var out signinjournal.BoundSite
		key, e := autoSignInSiteKey(ref["id"])
		if e != nil {
			return out, e
		}
		row, ok := snapshot.Rows[key]
		if !ok {
			return out, source.ErrSiteTargetDenied
		}
		// A reference is never a transport grant, even when it contains extra fields.
		out.Site = autoSignInClone(row)
		out.Revision = snapshot.Revision
		if e = guard(ctx); e != nil {
			return out, e
		}
		if source.Truth(row["render"]) {
			return out, bad("AUTOSIGNIN_BROWSER_UNAVAILABLE", "站点浏览器仿真尚未提供可信执行能力")
		}
		address, _ := row["url"].(string)
		for _, kind := range []source.FixedAdapter{source.AdapterHDSky, source.AdapterOpencd, source.AdapterTjupt} {
			if yes, _ := source.FixedAdapterMatch(kind, address); yes {
				return out, bad("AUTOSIGNIN_ADAPTER_DEPENDENCY_UNAVAILABLE", "此站点需要尚未接入的可信OCR或题库执行能力")
			}
		}
		policy := &source.RequestsProxyPolicy{Environment: append([]string(nil), snapshot.Environment...), Explicit: map[string]string{}}
		if snapshot.Proxy.HTTP != "" {
			policy.Explicit["http"] = snapshot.Proxy.HTTP
		}
		if snapshot.Proxy.HTTPS != "" {
			policy.Explicit["https"] = snapshot.Proxy.HTTPS
		}
		registration := source.Registration{Site: out.Site, RequestsProxy: policy, BeforeRequest: guard}
		generic, e := source.BindRegistrationHooks(registration, false)
		if e != nil {
			return out, e
		}
		specialized, e := source.BindRegistrationHooks(registration, true)
		if e != nil {
			return out, e
		}
		origin, e := autoSignInOrigin(address)
		if e != nil {
			return out, e
		}
		out.Hooks = source.SiteVisitHooks{HTTP: autoSignInHTTP{guard: guard, origin: origin, generic: generic.HTTP, fixed: specialized.HTTP}}
		if specialized.MTeam != nil {
			out.Hooks.MTeam = autoSignInMTeam{guard: guard, next: specialized.MTeam}
		}
		return out, nil
	}
	return deps, nil
}

func autoSignInOrigin(address string) (string, error) {
	u, e := url.Parse(address)
	if e != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", source.ErrSiteTargetDenied
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + strings.ToLower(u.Hostname()) + ":" + port, nil
}

type autoSignInHTTP struct {
	guard          func(context.Context) error
	origin         string
	generic, fixed source.PageTransport
}

func (h autoSignInHTTP) Do(ctx context.Context, r source.PageRequest) (*source.PageResponse, error) {
	if e := h.guard(ctx); e != nil {
		return nil, e
	}
	origin, e := autoSignInOrigin(r.URL)
	if e != nil {
		return nil, e
	}
	if origin == h.origin {
		return h.generic.Do(ctx, r)
	}
	// No retry/fallback after a failed request: only compiled cross-origin grants.
	return h.fixed.Do(ctx, r)
}

type autoSignInMTeam struct {
	guard func(context.Context) error
	next  source.SiteTransport
}

func (h autoSignInMTeam) Post(ctx context.Context, p source.RequestPlan) (*source.AdapterResponse, error) {
	if e := h.guard(ctx); e != nil {
		return nil, e
	}
	return h.next.Post(ctx, p)
}

// Read-only SDK form/page calls have no execution run and receive no transport.
func (a *App) autoSignInReadInventory(ctx context.Context, owner *pluginAutoSignInOwner) (source.Inventory, error) {
	if owner == nil || owner.a != a {
		return source.Inventory{}, signinjournal.ErrStale
	}
	backend, err := owner.backend(nil)
	if err != nil {
		return source.Inventory{}, err
	}
	if err = backend.View(ctx, owner.binding, func(*signinjournal.State) error { return nil }); err != nil {
		return source.Inventory{}, err
	}
	snapshot, err := a.readAutoSignInSites(ctx, owner)
	if err != nil {
		return source.Inventory{}, err
	}
	return autoSignInClone(snapshot.Inventory), nil
}

// Form inventory is available before installation. This read neither allocates
// an owner nor offers any network capability or returns private site fields.
func (a *App) autoSignInFormInventory(ctx context.Context) (source.Inventory, error) {
	snapshot, err := a.readAutoSignInSites(ctx, nil)
	if err != nil {
		return source.Inventory{}, err
	}
	return autoSignInClone(snapshot.Inventory), nil
}
