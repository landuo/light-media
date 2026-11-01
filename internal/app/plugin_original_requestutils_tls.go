package app

import (
	"crypto/tls"
	"net/http"
	"time"
)

// originalPluginSourceTLSBase is a private template, never a mutation of the
// default transport, system roots, environment, or another consumer's client.
// Fixed RequestUtils.request uses verify=False. This A-only candidate covers
// only plugin material requests below. Statistics, Wiki and future callers of
// the existing originalPluginHTTPClient entry point retain their old policy.
func originalPluginSourceTLSBase() *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	if base.TLSClientConfig == nil {
		base.TLSClientConfig = &tls.Config{}
	} else {
		base.TLSClientConfig = base.TLSClientConfig.Clone()
	}
	base.TLSClientConfig.InsecureSkipVerify = true
	return base
}

// A: exact existing index/release/compatibility/acquisition requests. Proxy
// selection, mirror/API routing, credential isolation and budgets are unchanged.
func (a *App) originalPluginMaterialHTTPClient(timeout time.Duration) (*http.Client, error) {
	return a.originalPluginHTTPClientWithBase(timeout, true, originalPluginSourceTLSBase())
}
