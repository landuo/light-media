package download

import (
	"crypto/tls"
	"net/http"

	"light-media/internal/logging"
)

// NewOriginalCorePlugin is only for the ordinary plugin-module core fallback
// consumers. New retains its existing validation, credentials, cookie jar,
// timeout and redirect policy; unrelated download-page callers still use New.
// The 2026-11-02 plugin-only certificate policy does not modify global roots,
// proxy routing or the process default transport.
func NewOriginalCorePlugin(config Config) (*Client, error) {
	client, err := New(config)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	client.http.Transport = logging.Transport(transport)
	return client, nil
}

// CloseIdleConnections retires only this client's idle transport resources.
// The plugin core caller owns the private transport and calls this after its
// exact list/control operation. It never cancels another client's requests.
func (c *Client) CloseIdleConnections() {
	c.http.CloseIdleConnections()
}
