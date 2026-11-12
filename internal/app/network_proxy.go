package app

import (
	"light-media/internal/networkproxy"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func (a *App) proxyConfig() (networkproxy.Config, error) {
	a.cfgMu.RLock()
	setting := a.config.ProxyHost
	a.cfgMu.RUnlock()
	return resolveProxyConfig(setting)
}

func resolveProxyConfig(setting string) (networkproxy.Config, error) {
	return networkproxy.Resolve(setting, os.Getenv)
}

func (a *App) metadataImageRequest(req *http.Request) (*http.Response, error) {
	config, err := a.proxyConfig()
	if err != nil {
		return nil, err
	}
	if config.HTTP == "" && config.HTTPS == "" {
		return assetImageClient.Do(req)
	}
	a.metadataProxyMu.Lock()
	if a.metadataProxyClient == nil || a.metadataProxyConfig != config {
		if a.metadataProxyClient != nil {
			a.metadataProxyClient.CloseIdleConnections()
		}
		var base *http.Transport
		if assetImageClient.Transport != nil {
			var ok bool
			base, ok = assetImageClient.Transport.(*http.Transport)
			if !ok {
				a.metadataProxyMu.Unlock()
				return nil, bad("IMAGE_PROXY_TRANSPORT", "图片传输配置不支持代理")
			}
		}
		transport, err := config.Transport(base)
		if err != nil {
			a.metadataProxyMu.Unlock()
			return nil, err
		}
		client := *assetImageClient
		client.Transport = transport
		a.metadataProxyClient = &client
		a.metadataProxyConfig = config
	}
	client := a.metadataProxyClient
	a.metadataProxyMu.Unlock()
	return client.Do(req)
}

func normalizeProxySettings(c *Settings) error {
	c.ProxyHost = strings.TrimSpace(c.ProxyHost)
	if c.ClearProxyHost {
		c.ProxyHost = ""
	}
	c.ClearProxyHost = false
	if c.ProxyHost != "" {
		if _, err := networkproxy.Parse(c.ProxyHost); err != nil {
			return bad("INVALID_PROXY", "代理地址格式无效，支持 HTTP、HTTPS、SOCKS5、SOCKS5h")
		}
	}
	return nil
}

func proxySecrets(raw string) []string {
	values := []string{raw}
	u, err := url.Parse(raw)
	if err == nil && u.User != nil {
		password, _ := u.User.Password()
		values = append(values, u.User.String(), u.User.Username(), password)
	}
	return values
}

// Keep the pool across requests, replacing it only when outbound routing changes.
func (a *App) metadataAPITransportForConfig() (http.RoundTripper, error) {
	config, err := a.proxyConfig()
	if err != nil {
		return nil, err
	}
	a.metadataProxyMu.Lock()
	defer a.metadataProxyMu.Unlock()
	if a.metadataAPITransport == nil || a.metadataAPIConfig != config {
		transport, err := config.Transport(nil)
		if err != nil {
			return nil, err
		}
		if a.metadataAPITransport != nil {
			a.closeMetadataAPITransportLocked()
		}
		a.metadataAPITransport, a.metadataAPIConfig = transport, config
	}
	return a.metadataAPITransport, nil
}

func (a *App) closeMetadataAPITransportLocked() {
	if closer, ok := a.metadataAPITransport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
