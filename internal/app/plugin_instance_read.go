package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"light-media/internal/plugins"
	"reflect"
	"slices"
)

// Configuration readers use the existing authenticated instance session, not
// an execution run, a clone-name heuristic or a second instance state. Every
// mode is read-only and the existing dispatcher checks the snapshot afterward.
func (p *PluginStore) readPluginInstanceView(ctx context.Context, in PluginInstallation, read pluginFormCallback) (json.RawMessage, error) {
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil {
		return nil, err
	}
	requested := session.anchor
	requested.installation = in
	if !samePluginFormIdentity(requested, session.anchor) {
		return nil, pluginFormStale()
	}
	ctx = withPluginFormSession(ctx, session)
	readonly := func(ctx context.Context, view pluginFormView) (json.RawMessage, error) {
		ctx = context.WithValue(ctx, pluginFormCallbackScopeKey{}, &pluginFormCallbackScope{session: session, readOnly: true})
		return read(ctx, view)
	}
	return p.dispatchPluginForm(ctx, in, pluginFormCallbacks{Cold: readonly, Started: readonly, LegacyUnknown: readonly})
}

func (p *PluginStore) validatePluginInstanceReadTx(ctx context.Context, q PluginProofReader, in PluginInstallation) error {
	session := pluginFormSessionFromContext(ctx)
	if session == nil || session.store != p {
		return pluginFormViewDenied()
	}
	if _, err := p.trustedPluginFormView(ctx, in); err != nil {
		return err
	}
	current, err := p.pluginFormAnchorRead(ctx, q, in, pluginFormOriginInstalled)
	if err != nil {
		return err
	}
	return comparePluginFormAnchors(session.anchor, current)
}

// A host-compiled registration binds a private decoder to an existing catalog
// capability and its exact HTTP declarations. It is never accepted from a
// worker, stored manifest, request, instance name, or configuration value.
type pluginConfigurationHTTPRead func(context.Context, pluginFormView, plugins.HTTPRequest) (json.RawMessage, error)
type pluginConfigurationHTTPRegistration struct {
	manifest   plugins.Manifest
	capability string
	keys       []string
	read       pluginConfigurationHTTPRead
}

func resolvePluginConfigurationHTTPReader(registrations []pluginConfigurationHTTPRegistration, manifest plugins.Manifest, api plugins.HTTPAPI, method string) pluginConfigurationHTTPRead {
	if method != "GET" {
		return nil
	}
	for _, registration := range registrations {
		trusted := registration.manifest
		if trusted.PluginID != manifest.PluginID || trusted.Version != manifest.Version || trusted.SchemaVersion != manifest.SchemaVersion || registration.read == nil || !slices.Contains(trusted.Capabilities, registration.capability) || !slices.Contains(registration.keys, api.Key) {
			continue
		}
		for _, declared := range trusted.APIs {
			if declared.Key == api.Key && slices.Contains(declared.Methods, method) && reflect.DeepEqual(declared, api) {
				return registration.read
			}
		}
	}
	return nil
}

func (a *App) pluginConfigurationHTTPReader(m plugins.Manifest, api plugins.HTTPAPI, method string) pluginConfigurationHTTPRead {
	return resolvePluginConfigurationHTTPReader(a.compiledPluginConfigurationReaders(), m, api, method)
}

func (a *App) validatePluginReadAuthority(ctx context.Context, authority pluginAPIAuthority) error {
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return a.validatePluginHTTPAuthorityTx(ctx, tx, authority)
}
