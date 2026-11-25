package requestsproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	xproxy "golang.org/x/net/proxy"
)

func requestsRoute(base *http.Transport, proxy *url.URL, proxyRoots *x509.CertPool, originTLS func(context.Context, net.Conn, string) (net.Conn, error), plain func(net.Conn) net.Conn, proxyResponse func(net.Conn, *http.Request) (net.Conn, error)) (*http.Transport, error) {
	t := base.Clone()
	t.Proxy = nil
	t.DialTLS = nil
	if plain == nil {
		plain = func(c net.Conn) net.Conn { return c }
	}
	if originTLS == nil {
		originTLS = func(ctx context.Context, c net.Conn, host string) (net.Conn, error) {
			cfg := t.TLSClientConfig
			if cfg == nil {
				cfg = &tls.Config{}
			} else {
				cfg = cfg.Clone()
			}
			cfg.ServerName = host
			secured := tls.Client(c, cfg)
			if e := secured.HandshakeContext(ctx); e != nil {
				c.Close()
				return nil, e
			}
			return secured, nil
		}
	}
	raw := base.DialContext
	if raw == nil {
		raw = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: -1}).DialContext
	}
	originDial := raw
	if proxy != nil {
		switch proxy.Scheme {
		case "http", "https":
			p := *proxy
			auth := ""
			if p.User != nil {
				username := p.User.Username()
				password, has := p.User.Password()
				if username != "" && has {
					u, e := requestsAuthLatin1(username)
					if e != nil {
						return nil, e
					}
					v, e := requestsAuthLatin1(password)
					if e != nil {
						return nil, e
					}
					p.User = url.UserPassword(u, v)
					auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(u+":"+v))
				} else {
					p.User = nil
				}
			}
			endpoint := proxy.Host
			if proxy.Port() == "" {
				port := "80"
				if proxy.Scheme == "https" {
					port = "443"
				}
				endpoint = net.JoinHostPort(proxy.Hostname(), port)
			}
			proxyDial := func(ctx context.Context, network, _ string) (net.Conn, error) {
				c, e := raw(ctx, network, endpoint)
				if e != nil {
					return nil, e
				}
				if proxy.Scheme == "https" {
					verified := tls.Client(c, &tls.Config{ServerName: proxy.Hostname(), RootCAs: proxyRoots, NextProtos: []string{"http/1.1"}})
					if e = verified.HandshakeContext(ctx); e != nil {
						c.Close()
						return nil, e
					}
					c = verified
				}
				return c, nil
			}
			originDial = func(ctx context.Context, network, address string) (net.Conn, error) {
				c, e := proxyDial(ctx, network, endpoint)
				if e != nil {
					return nil, e
				}
				success := false
				defer func() {
					if !success {
						c.Close()
					}
				}()
				stop := context.AfterFunc(ctx, func() { c.Close() })
				defer stop()
				connect := &http.Request{Method: "CONNECT", URL: &url.URL{Opaque: address}, Host: address, Header: http.Header{"User-Agent": []string{""}}}
				if auth != "" {
					connect.Header.Set("Proxy-Authorization", auth)
				}
				if e = connect.Write(c); e != nil {
					return nil, e
				}
				if proxyResponse != nil {
					parsed, e := proxyResponse(c, connect)
					if e != nil {
						return nil, e
					}
					success = true
					return parsed, nil
				}
				response, e := http.ReadResponse(bufio.NewReader(c), connect)
				if e != nil {
					return nil, e
				}
				if response.StatusCode != 200 {
					return nil, errors.New("requests_proxy_connect_rejected")
				}
				success = true
				return c, nil
			}
			// HTTPS targets are tunneled by DialTLSContext so the source meter can
			// observe decrypted origin response lines. HTTP forward requests retain
			// standard Transport absolute-form/proxy-auth behavior inside proxy TLS.
			p.Scheme = "http"
			p.Host = endpoint
			t.Proxy = func(req *http.Request) (*url.URL, error) {
				if req.URL.Scheme == "https" {
					return nil, nil
				}
				return &p, nil
			}
			t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				c, e := proxyDial(ctx, network, address)
				if e != nil {
					return nil, e
				}
				return plain(c), nil
			}
		case "socks5":
			p := *proxy
			p.Path = ""
			p.RawPath = ""
			p.RawQuery = ""
			p.ForceQuery = false
			p.Fragment = ""
			local, e := Apply(base, p.String())
			if e != nil {
				return nil, e
			}
			originDial = local.DialContext
		case "socks5h":
			endpoint := proxy.Host
			if proxy.Port() == "" {
				endpoint = net.JoinHostPort(proxy.Hostname(), "1080")
			}
			var auth *xproxy.Auth
			if proxy.User != nil {
				password, _ := proxy.User.Password()
				auth = &xproxy.Auth{User: proxy.User.Username(), Password: password}
			}
			socks, e := xproxy.SOCKS5("tcp", endpoint, auth, contextDialer{raw})
			if e != nil {
				return nil, errors.New("requests_proxy_invalid_url")
			}
			contextSocks, ok := socks.(xproxy.ContextDialer)
			if !ok {
				return nil, errors.New("proxy_context_dialer_required")
			}
			originDial = contextSocks.DialContext
		case "socks4", "socks4a":
			originDial = requestsSOCKS4(t, proxy).DialContext
		default:
			return nil, errors.New("requests_proxy_unsupported_scheme")
		}
	}
	if t.DialContext == nil || proxy == nil || (proxy.Scheme != "http" && proxy.Scheme != "https") {
		t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			c, e := originDial(ctx, network, address)
			if e != nil {
				return nil, e
			}
			return plain(c), nil
		}
	}
	t.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, e := originDial(ctx, network, address)
		if e != nil {
			return nil, e
		}
		host, _, e := net.SplitHostPort(address)
		if e != nil {
			c.Close()
			return nil, e
		}
		return originTLS(ctx, c, host)
	}
	return requestsBindDial(t), nil
}
