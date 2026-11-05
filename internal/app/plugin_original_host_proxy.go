package app

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"

	"light-media/internal/plugins"
)

func originalProxyEnvironment(environment []string) []string {
	out := []string{}
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found && (name == "REQUEST_METHOD" || strings.HasSuffix(strings.ToLower(name), "_proxy")) {
			out = append(out, entry)
		}
	}
	return out
}

// This is config.py PROXY, before requests performs its separate trust-env
// merge. In particular ALL_PROXY is not part of this getter, invalid URL text
// is not parsed here, and a missing scheme borrows the other environment value.
func originalProxySettingsValue(setting string, environment []string) map[string]string {
	if explicit := originalIncomingStrip(setting); explicit != "" {
		return map[string]string{"http": explicit, "https": explicit}
	}
	values := map[string]string{}
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if found {
			values[name] = value
		}
	}
	first := func(names ...string) string {
		for _, name := range names {
			if value := originalIncomingStrip(values[name]); value != "" {
				return value
			}
		}
		return ""
	}
	https, http := first("HTTPS_PROXY", "https_proxy"), first("HTTP_PROXY", "http_proxy")
	if https == "" {
		https = http
	}
	if http == "" {
		http = https
	}
	if http == "" {
		return nil
	}
	return map[string]string{"http": http, "https": https}
}

func (a *App) originalHostProxyResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var input struct{}
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	if scope.object == nil || scope.object.app != a {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "私有代理读取需要当前插件对象")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	setting := a.Settings().ProxyHost
	environment := originalProxyEnvironment(os.Environ())
	out := plugins.NetworkProxySettingsV1{SchemaVersion: 1, Proxies: originalProxySettingsValue(setting, environment), Environment: environment}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if a.Settings().ProxyHost != setting || !slices.Equal(environment, originalProxyEnvironment(os.Environ())) {
		return nil, bad("NETWORK_CONFIGURATION_CHANGED", "宿主代理配置已变化")
	}
	encoded, err := json.Marshal(out)
	if err == nil && len(encoded) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "私有代理快照超出共享传输上限")
	}
	return encoded, err
}
