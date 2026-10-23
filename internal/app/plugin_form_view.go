package app

import "context"

// pluginFormView is a detached view of this synchronous HTTP Form request's
// captured state (including its own successfully committed Form writes). It is
// not a current DB read or a cross-transaction authorization token. Adapters
// must still authenticate exact business source state; all Form writes remain
// subject to the common transaction's same-TX session.admit revalidation.
//
// Display may differ from durable config. Durable is the config/CAS preimage; display is presentation-only.
// Preview has an authenticated generation-zero resolved installation, never a
// fabricated initialized owner, and may retain a tombstone's config for prefill.
type pluginFormView struct {
	mode         pluginFormMode
	installation PluginInstallation
	durable      PluginStoredValue
	display      PluginStoredValue
	configExists bool
}

func pluginFormViewDenied() error {
	return bad("PLUGIN_CAPABILITY_DENIED", "插件表单请求上下文或实例身份不匹配")
}

// trustedPluginFormView is available only to compiled host adapters in this
// package. It reads no SQL and exposes no session pointer or mutable byte alias.
// The requested identity must match the authenticated resolved target exactly;
// caller manifests and display values never select identity or write authority.
func (p *PluginStore) trustedPluginFormView(ctx context.Context, requested PluginInstallation) (pluginFormView, error) {
	var out pluginFormView
	if p == nil || ctx == nil {
		return out, pluginFormViewDenied()
	}
	session := pluginFormSessionFromContext(ctx)
	if session == nil || session.store != p {
		return out, pluginFormViewDenied()
	}
	resolved := session.resolution.installation
	if requested.InstanceID != resolved.InstanceID || requested.PluginID != resolved.PluginID || requested.Version != resolved.Version || requested.ManifestSchemaVersion != resolved.ManifestSchemaVersion || requested.Generation != resolved.Generation || requested.Installed != resolved.Installed {
		return out, pluginFormViewDenied()
	}
	if session.hostFailure != nil {
		failure := *session.hostFailure
		return out, &failure
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return pluginFormView{
		mode:         session.resolution.mode,
		installation: pluginFormInstallationCopy(resolved),
		durable:      pluginFormValueCopy(session.durable),
		display:      pluginFormValueCopy(session.display),
		configExists: session.configExists,
	}, nil
}
