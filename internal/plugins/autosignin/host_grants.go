package autosignin

import (
	"context"
	"net/url"
	"time"
)

// Registration is a trusted host snapshot. Instances are never serialized or
// accepted from a worker, and no general HTTP RPC is exposed. Reconstruct it
// after a host SiteStore change, with current credential/version validation.
type Registration struct {
	Site     Object
	ProxyURL *url.URL
	Browser  BrowserTransport
	OCR      func(context.Context, string) (any, error)
	Tjupt    *TjuptDependencies
}
type RegisteredSites struct {
	rows  []Registration
	Stats func(context.Context, string, bool, int) error
	Now   func() time.Time
}

func NewRegisteredSites(rows []Registration) *RegisteredSites {
	cloned := make([]Registration, len(rows))
	for i, row := range rows {
		cloned[i] = row
		cloned[i].Site = clone(row.Site).(Object)
		if row.ProxyURL != nil {
			proxy := *row.ProxyURL
			cloned[i].ProxyURL = &proxy
		}
	}
	return &RegisteredSites{rows: cloned}
}
func (r *RegisteredSites) Lookup(_ context.Context, domain string) (Object, error) {
	if r == nil {
		return nil, ErrAdapterNotReady
	}
	for _, row := range r.rows {
		if sourceDomain(textValue(row.Site["url"])) == domain {
			return publicInventory(Inventory{Indexers: []Object{row.Site}}).Indexers[0], nil
		}
	}
	return nil, nil
}
func (r *RegisteredSites) Visit(ctx context.Context, ref Object, operation string, loaded bool) (Result, error) {
	if r == nil {
		return Result{}, ErrAdapterNotReady
	}
	var registration *Registration
	for i := range r.rows {
		if equal(r.rows[i].Site["id"], ref["id"]) {
			registration = &r.rows[i]
			break
		}
	}
	if registration == nil {
		return Result{}, ErrSiteTargetDenied
	}
	site := registration.Site
	proxy := registration.ProxyURL
	if !Truth(site["proxy"]) {
		proxy = nil
	}
	binding := SiteHTTPBinding{URL: textValue(site["url"]), Cookie: textValue(site["cookie"]), UserAgent: site["ua"], ProxyURL: proxy}
	transport, err := BindSiteHTTP(binding)
	if err != nil {
		return Result{}, err
	}
	h := SiteVisitHooks{HTTP: transport, Browser: registration.Browser, OCR: registration.OCR, Tjupt: registration.Tjupt, Stats: r.Stats, Now: r.Now}
	adapters := InstalledAdapterRegistry()
	if loaded {
		if MTeamMatch(binding.URL) {
			h.MTeam, err = BindMTeam(Credentials{URL: binding.URL, Token: textValue(site["token"]), UserAgent: textValue(site["ua"]), Proxy: site["proxy"], Timeout: site["timeout"]}, proxy)
			if err != nil {
				return Result{}, err
			}
		} else {
			// A dedicated grant authorizes only its installed adapter's known target
			// family; the source's permissive match never grants arbitrary domains.
			for _, kind := range []FixedAdapter{AdapterPt52, AdapterBTSchool, AdapterCHDBits, AdapterHaiDan, AdapterHares, AdapterHDArea, AdapterHDChina, AdapterHDCity, AdapterHDSky, AdapterHDUpt, AdapterNexusHD, AdapterOpencd, AdapterPTerClub, AdapterPTTime, AdapterRousiPro, AdapterTjupt, AdapterTTG, AdapterU2, AdapterYemaPT, AdapterZhuQue} {
				yes, _ := FixedAdapterMatch(kind, binding.URL)
				if yes {
					h.HTTP, err = BindFixedAdapter(kind, binding)
					if err != nil {
						return Result{}, err
					}
					break
				}
			}
		}
	}
	return (SiteVisitor{Adapters: adapters, Hooks: h}).Visit(ctx, site, operation, loaded)
}
