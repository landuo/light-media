package requestsproxy

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// requestsProxyMerge belongs to one logical request and its redirect chain.
// Its environment and initial mapping are frozen, never process-global.
type requestsProxyMerge struct {
	environment []environmentItem
	proxies     map[string]string
	first       bool
}

// NewRequestsProxyTransport mirrors requests.Session.request's initial
// merge_environment_settings and subsequent redirect resolve_proxies calls.
// Create one transport per logical request (including all of its redirects),
// not one shared across independent requests. Explicit empty values remain
// present and suppress environment setdefault for their exact keys. NO_PROXY
// suppresses environment additions only; it never removes an existing route.
// Environment, explicit, initialURL and base are not retained as mutable inputs.
func NewRequestsProxyTransport(base *http.Transport, environment []string, explicit map[string]string, initialURL *url.URL) (*RequestsTransport, error) {
	if initialURL == nil {
		return nil, errors.New("requests_proxy_missing_target")
	}
	state := &requestsProxyMerge{environment: environmentValues(environment), proxies: requestsCopyProxies(explicit), first: true}
	for key, value := range state.environmentProxies(initialURL) {
		if _, present := state.proxies[key]; !present {
			state.proxies[key] = value
		}
	}
	// A nonnil environment callback prevents even a fallback live environment read.
	t := NewRequestsTransport(base, func() []string { return nil })
	t.merged = state
	return t, nil
}

func requestsCopyProxies(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (m *requestsProxyMerge) environmentProxies(target *url.URL) map[string]string {
	proxies := requestsGetproxies(m.environment)
	var override *string
	if value, present := m.proxies["no_proxy"]; present {
		override = &value
	}
	host := strings.ToLower(target.Hostname())
	if host == "" || requestsBypassOverride(m.environment, proxies, host, target.Port(), override) {
		return nil
	}
	return proxies
}

func (t *RequestsTransport) requestProxy(target *url.URL) (*url.URL, error) {
	if t.merged == nil {
		return RequestsEnvironmentProxy(t.environment(), target)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.merged
	if m.first {
		m.first = false
	} else {
		// Source copies the prior map, then adds only scheme (not host keys).
		next := requestsCopyProxies(m.proxies)
		env := m.environmentProxies(target)
		scheme := strings.ToLower(target.Scheme)
		proxy, present := env[scheme]
		if !present {
			proxy = env["all"]
		}
		if _, present := next[scheme]; !present && proxy != "" {
			next[scheme] = proxy
		}
		m.proxies = next
	}
	return requestsSelectProxy(m.proxies, target)
}
