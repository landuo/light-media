package sites

import (
	"crypto/tls"
	"errors"
	"net/http"

	"light-media/internal/logging"
	"light-media/internal/networkproxy"
)

// NewOriginalPlugin is restricted to trusted original-plugin host consumers.
// New remains the sole validator and the ordinary client is never mutated.
func NewOriginalPlugin(config Config) (*Client, error) {
	client, err := New(config)
	if err != nil {
		return nil, err
	}
	var base *http.Transport
	if config.Kind == "nexusphp" {
		// Keep the exact public-site DialContext (DNS validation and address
		// pinning) and Proxy=nil. Do NOT replace it with DefaultTransport.
		var ok bool
		base, ok = publicSiteTransport().(*http.Transport)
		if !ok || base == nil {
			return nil, errors.New("site_transport_error")
		}
	} else {
		var ok bool
		base, ok = http.DefaultTransport.(*http.Transport)
		if !ok || base == nil {
			return nil, errors.New("site_transport_error")
		}
	}
	transport := originalPluginTLSClone(base)
	client.http.Transport = logging.Transport(transport)
	client.closeProxyTransport = transport.CloseIdleConnections
	return client, nil
}

func NewOriginalPluginWithProxy(config Config, proxy networkproxy.Config) (*Client, error) {
	if proxy.HTTP == "" && proxy.HTTPS == "" {
		return NewOriginalPlugin(config)
	}
	client, err := New(config)
	if err != nil {
		return nil, err
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok || base == nil {
		return nil, errors.New("site_transport_error")
	}
	// Match existing NewWithProxy's explicit route exactly. Do not add a
	// direct fallback or change its SOCKS/proxy/DNS policy in this TLS delta.
	transport, err := proxy.Transport(originalPluginTLSClone(base))
	if err != nil {
		return nil, err
	}
	client.http.Transport = logging.Transport(transport)
	client.closeProxyTransport = transport.(interface{ CloseIdleConnections() }).CloseIdleConnections
	return client, nil
}

func originalPluginTLSClone(base *http.Transport) *http.Transport {
	transport := base.Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	return transport
}
