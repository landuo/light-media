package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"light-media/internal/plugins"
)

const sourceCustomSitesNamespace = "CustomSites"

type originalResolvedSourceSite struct {
	reference plugins.SiteSourceReferenceV1
	fields    map[string]json.RawMessage
	base      *url.URL
	guard     func(context.Context) error
	rawFields json.RawMessage
}

func originalSourceSiteRequestCapability(method string) bool {
	switch method {
	case plugins.SitesSourceCustomConfigCapability, plugins.SitesSourceCredentialsCapability, plugins.SitesSourceRequestCapability, plugins.SitesSourceCheckCapability, plugins.SitesSourceResultWriteCapability:
		return true
	}
	return false
}
func (a *App) originalHostSourceSiteRequestResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if scope.object == nil || scope.object.app != a {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "源站点资源需要当前插件对象")
	}
	switch method {
	case plugins.SitesSourceCustomConfigCapability:
		var in struct{}
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		value, snapshot, err := a.originalSourceCustomConfig(ctx, scope)
		if err != nil {
			return nil, err
		}
		scope.object.sourceLogSecrets.registerCustomConfig(value)
		return json.Marshal(plugins.SiteSourceCustomConfigV1{SchemaVersion: 1, Config: value, Snapshot: snapshot})
	case plugins.SitesSourceCredentialsCapability:
		var in plugins.SiteSourceReferenceRequestV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		value, err := a.resolveOriginalSourceSite(ctx, scope, in.Reference, true)
		if err != nil {
			return nil, err
		}
		if err = value.guard(ctx); err != nil {
			return nil, err
		}
		fields := append(json.RawMessage(nil), value.rawFields...)
		scope.object.sourceLogSecrets.registerFields(fields)
		return json.Marshal(plugins.SiteSourceCredentialsV1{SchemaVersion: 1, Reference: value.reference, Fields: fields})
	case plugins.SitesSourceRequestCapability:
		return a.originalHostSourceSiteHTTPResource(ctx, scope, raw)
	case plugins.SitesSourceCheckCapability:
		return a.originalHostSourceSiteCheck(ctx, scope, raw)
	case plugins.SitesSourceResultWriteCapability:
		return a.originalHostSourceSiteAccess(ctx, scope, raw)
	}
	return nil, bad("PLUGIN_CAPABILITY_DENIED", "源站点资源方法未获允许")
}
func sourceSiteReferenceError() error {
	return bad("SOURCE_SITE_CONFIGURATION_CHANGED", "源站点引用或配置已变化")
}
func originalSourceSiteReferenceShape(ref plugins.SiteSourceReferenceV1, unbound bool) bool {
	if ref.SiteID != "" && ref.CustomOrdinal != nil || ref.SiteID == "" && ref.CustomOrdinal == nil || len(ref.SiteID) > 4096 {
		return false
	}
	if ref.CustomOrdinal != nil && *ref.CustomOrdinal < 0 {
		return false
	}
	return unbound && ref.SiteID != "" && ref.Snapshot == "" || len(ref.Snapshot) == 64
}
func (a *App) sourceSiteMAC(scope originalBaseCallScope, domain string, value any) string {
	return a.Store.siteIdentityMAC("source-site-"+domain, struct {
		Plugin, Instance, Version string
		Generation                int64
		Value                     any
	}{scope.installation.PluginID, scope.installation.InstanceID, scope.installation.Version, scope.installation.Generation, value})
}

// Uses the exact existing SystemConfig cache path, including its deliberate
// failed-write cache state. A DB revision alone would not fence that state.
func (a *App) originalSourceCustomConfig(ctx context.Context, scope originalBaseCallScope) (json.RawMessage, string, error) {
	if err := scope.validate(ctx); err != nil {
		return nil, "", err
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	state := &a.pluginOriginalBase
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, "", err
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return a.originalSourceCustomConfigLocked(ctx, scope)
}

// Caller owns pluginMu then pluginOriginalBase.mu, exactly like Base methods.
func (a *App) originalSourceCustomConfigLocked(ctx context.Context, scope originalBaseCallScope) (json.RawMessage, string, error) {
	observed, sealed, err := a.pluginStore.readOriginalBaseConfigRow(ctx, a.pluginStore.store.DB, sourceCustomSitesNamespace)
	if err != nil {
		return nil, "", err
	}
	value, err := a.originalBaseObserveConfigLocked(sourceCustomSitesNamespace, observed, sealed)
	if err != nil {
		return nil, "", err
	}
	proof := a.sourceSiteMAC(scope, "custom-snapshot", struct {
		Namespace         string
		Present           bool
		Revision          int64
		Sealed, Effective [sha256.Size]byte
	}{sourceCustomSitesNamespace, observed.present, observed.revision, observed.sealedDigest, sha256.Sum256(value)})
	if err := scope.validate(ctx); err != nil {
		return nil, "", err
	}
	return value, proof, nil
}
func sourceSiteString(raw json.RawMessage) (string, bool) {
	var value string
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}
func sourceSiteFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, bad("INVALID_SOURCE_SITE", "源站点配置不是对象")
	}
	return value, nil
}
func sourceSiteJSON(value any) json.RawMessage    { b, _ := json.Marshal(value); return b }
func sourceSiteRawTruth(raw json.RawMessage) bool { return originalBaseJSONTruthy(raw) }

func sourceSiteURL(fields map[string]json.RawMessage) (*url.URL, error) {
	value, ok := sourceSiteString(fields["url"])
	if !ok || len(value) > 8192 {
		return nil, bad("INVALID_SOURCE_SITE", "源站点地址无效")
	}
	address, err := url.Parse(value)
	if err != nil || address.User != nil || address.Opaque != "" || address.Fragment != "" || address.Hostname() == "" || address.Scheme != "https" && address.Scheme != "http" || strings.ContainsAny(value, "\r\n\x00") {
		return nil, bad("INVALID_SOURCE_SITE", "源站点地址无效")
	}
	// CustomSites is a real independent source config, not an implicit grant to
	// arbitrary local resources. Reuse the ordinary public-origin boundary; keep
	// the existing explicit loopback fixture allowance and never allocate an ID.
	root := *address
	root.RawQuery = ""
	root.ForceQuery = false
	root.Path = ""
	root.RawPath = ""
	if _, err := nativeSiteCanonicalOrigin(root.String()); err != nil {
		return nil, err
	}
	return address, nil
}
func (a *App) resolveOriginalSourceSite(ctx context.Context, scope originalBaseCallScope, ref plugins.SiteSourceReferenceV1, unboundNative bool) (originalResolvedSourceSite, error) {
	var out originalResolvedSourceSite
	if !originalSourceSiteReferenceShape(ref, unboundNative) {
		return out, sourceSiteReferenceError()
	}
	if err := scope.validate(ctx); err != nil {
		return out, err
	}
	if ref.CustomOrdinal != nil {
		raw, proof, err := a.originalSourceCustomConfig(ctx, scope)
		if err != nil {
			return out, err
		}
		if !hmac.Equal([]byte(proof), []byte(ref.Snapshot)) {
			return out, sourceSiteReferenceError()
		}
		config, err := sourceSiteFields(raw)
		if err != nil || !sourceSiteRawTruth(config["enabled"]) {
			return out, sourceSiteReferenceError()
		}
		var entries []json.RawMessage
		if json.Unmarshal(config["sites"], &entries) != nil || *ref.CustomOrdinal >= len(entries) {
			return out, sourceSiteReferenceError()
		}
		fields, err := sourceSiteFields(entries[*ref.CustomOrdinal])
		if err != nil {
			return out, err
		}
		base, err := sourceSiteURL(fields)
		if err != nil {
			return out, err
		}
		// Raw credential/source fields remain in host memory, and only the explicit
		// private SourceCredentials capability may return them to trusted Go code.
		out = originalResolvedSourceSite{reference: ref, fields: fields, base: base, rawFields: append(json.RawMessage(nil), entries[*ref.CustomOrdinal]...)}
		out.guard = func(call context.Context) error {
			if err := call.Err(); err != nil {
				return err
			}
			_, current, err := a.originalSourceCustomConfig(call, scope)
			if err != nil {
				return err
			}
			if !hmac.Equal([]byte(current), []byte(ref.Snapshot)) {
				return sourceSiteReferenceError()
			}
			return nil
		}
		return out, nil
	}
	site, err := a.siteForRequest(allowDisabledSiteRequests(ctx), ref.SiteID)
	if err != nil {
		return out, err
	}
	identity, err := a.Store.nativeSiteByAppID(ctx, site.ID)
	if err != nil {
		return out, err
	}
	origin, err := nativeConfiguredSiteOrigin(site)
	if err != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
		return out, siteIdentityError()
	}
	source, mappedID, provenance, err := a.Store.nativeSourceSiteFields(ctx, site, identity)
	if err != nil {
		return out, err
	}
	proof := a.originalNativeSourceProof(scope, site, provenance)
	if ref.Snapshot != "" && !hmac.Equal([]byte(proof), []byte(ref.Snapshot)) {
		return out, sourceSiteReferenceError()
	}
	if !unboundNative && ref.Snapshot == "" {
		return out, sourceSiteReferenceError()
	}
	ref.Snapshot = proof
	ua := site.UserAgent
	if ua == "" {
		ua = originalPluginServerUserAgent()
	}
	fields := map[string]json.RawMessage{"id": sourceSiteJSON(mappedID), "name": sourceSiteJSON(site.Name), "url": sourceSiteJSON(site.BaseURL), "cookie": sourceSiteJSON(site.Cookie), "ua": sourceSiteJSON(ua), "apikey": sourceSiteJSON(site.APIKey), "token": sourceSiteJSON(site.Token), "proxy": sourceSiteJSON(site.Proxy), "render": sourceSiteJSON(site.Render), "timeout": sourceSiteJSON(site.Timeout)}
	if source != nil {
		projected := source.row()
		// SitesHelper merges DB options but retains template public metadata.
		delete(projected, "public")
		address, _ := url.Parse(source.URL)
		projected["domain"] = address.Scheme + "://" + address.Host + "/"
		if source.UA == nil || *source.UA == "" {
			projected["ua"] = originalPluginServerUserAgent()
		}
		fields = map[string]json.RawMessage{}
		for key, value := range projected {
			fields[key] = sourceSiteJSON(value)
		}
	}
	base, err := sourceSiteURL(fields)
	if err != nil {
		return out, err
	}
	out = originalResolvedSourceSite{reference: ref, fields: fields, base: base, rawFields: sourceSiteJSON(fields)}
	out.guard = func(call context.Context) error {
		if err := call.Err(); err != nil {
			return err
		}
		if err := scope.validate(call); err != nil {
			return err
		}
		current, err := a.siteForRequest(allowDisabledSiteRequests(call), ref.SiteID)
		if err != nil {
			return err
		}
		id, e := a.Store.nativeSiteByAppID(call, ref.SiteID)
		if e != nil {
			return siteIdentityError()
		}
		_, _, currentProvenance, e := a.Store.nativeSourceSiteFields(call, current, id)
		if e != nil {
			return e
		}
		if !hmac.Equal([]byte(a.originalNativeSourceProof(scope, current, currentProvenance)), []byte(ref.Snapshot)) {
			return sourceSiteReferenceError()
		}
		if e != nil || id.State != "active" || id.CanonicalOrigin != origin {
			return siteIdentityError()
		}
		return nil
	}
	return out, nil
}
func originalSourceSiteFieldString(fields map[string]json.RawMessage, key string) (string, error) {
	raw := fields[key]
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	value, ok := sourceSiteString(raw)
	if !ok {
		return "", bad("INVALID_SOURCE_SITE", fmt.Sprintf("源站点字段%s不是字符串", key))
	}
	return value, nil
}

func (a *App) originalNativeSourceProof(scope originalBaseCallScope, site SiteConfig, provenance string) string {
	if provenance == "" {
		return a.sourceSiteMAC(scope, "native-snapshot", site)
	}
	return a.sourceSiteMAC(scope, "native-snapshot", struct {
		Site       SiteConfig
		Provenance string
	}{site, provenance})
}
