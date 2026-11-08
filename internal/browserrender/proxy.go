package browserrender

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/net/proxy"
	"light-media/internal/networkproxy"
)

// A private loopback relay keeps proxy credentials out of Chromium's process
// arguments. The source wrapper puts inline credentials in --proxy-server;
// that would violate this application's explicit credential boundary.
func browserProxy(ctx context.Context, raw string, guard func(context.Context) error) (string, func(), error) {
	return browserOriginProxy(ctx, raw, guard, nil)
}

// Credentialed actions need a process-wide network boundary as well as CDP
// interception, which is target-local and does not cover new windows.
func browserOriginProxy(ctx context.Context, raw string, guard func(context.Context) error, origin *url.URL) (string, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	var u *url.URL
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if raw != "" {
		var err error
		u, err = networkproxy.Parse(raw)
		if err != nil {
			cancel()
			return "", nil, &Error{Code: "browser_proxy_invalid"}
		}
		transportURL := *u
		// Browser SOCKS resolves target domains at the proxy. RequestUtils' local
		// socks5 DNS adaptation is deliberately not reused for this browser path.
		if transportURL.Scheme == "socks5" {
			transportURL.Scheme = "socks5h"
		}
		transport, err = networkproxy.Apply(transport, transportURL.String())
		if err != nil {
			cancel()
			return "", nil, &Error{Code: "browser_proxy_invalid"}
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		transport.CloseIdleConnections()
		return "", nil, &Error{Code: "browser_proxy_failed"}
	}
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if guard != nil {
			if guard(ctx) != nil {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		if request.Method != http.MethodConnect {
			if origin != nil && (origin.Scheme != "http" || request.URL.User != nil || !sameOrigin(request.URL, origin)) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			request.RequestURI = ""
			request = request.WithContext(ctx)
			if origin != nil {
				request.Host = origin.Host
			}
			request.Header.Del("Proxy-Authorization")
			response, err := transport.RoundTrip(request)
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			defer response.Body.Close()
			for key, values := range response.Header {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, response.Body)
			return
		}
		if origin != nil {
			target := &url.URL{Scheme: "https", Host: request.Host}
			if _, _, err := net.SplitHostPort(request.Host); err != nil || !sameOrigin(target, origin) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		upstream, err := proxyTunnel(ctx, u, request.Host)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		downstream, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer downstream.Close()
		mu.Lock()
		connections[upstream], connections[downstream] = true, true
		mu.Unlock()
		defer func() { mu.Lock(); delete(connections, upstream); delete(connections, downstream); mu.Unlock() }()
		if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if buffered.Flush() != nil {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffered); upstream.Close(); close(done) }()
		_, _ = io.Copy(downstream, upstream)
		downstream.Close()
		<-done
	})
	go func() { _ = server.Serve(listener) }()
	cleanup := func() {
		cancel()
		server.Close()
		mu.Lock()
		for connection := range connections {
			connection.Close()
		}
		mu.Unlock()
		transport.CloseIdleConnections()
	}
	return "http://" + listener.Addr().String(), cleanup, nil
}

func proxyTunnel(ctx context.Context, upstream *url.URL, target string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	if upstream == nil {
		return dialer.DialContext(ctx, "tcp", target)
	}
	address := upstream.Host
	if upstream.Port() == "" {
		port := "80"
		if upstream.Scheme == "https" {
			port = "443"
		} else if upstream.Scheme == "socks5" || upstream.Scheme == "socks5h" {
			port = "1080"
		}
		address = net.JoinHostPort(upstream.Hostname(), port)
	}
	if upstream.Scheme == "socks5" || upstream.Scheme == "socks5h" {
		var auth *proxy.Auth
		if upstream.User != nil {
			password, _ := upstream.User.Password()
			auth = &proxy.Auth{User: upstream.User.Username(), Password: password}
		}
		d, err := proxy.SOCKS5("tcp", address, auth, dialer)
		if err != nil {
			return nil, err
		}
		return d.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
	}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if upstream.Scheme == "https" {
		tlsConnection := tls.Client(connection, &tls.Config{ServerName: upstream.Hostname(), MinVersion: tls.VersionTLS12})
		if err = tlsConnection.HandshakeContext(ctx); err != nil {
			connection.Close()
			return nil, err
		}
		connection = tlsConnection
	}
	_ = connection.SetDeadline(time.Now().Add(30 * time.Second))
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: http.Header{}}
	if upstream.User != nil {
		password, _ := upstream.User.Password()
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(upstream.User.Username()+":"+password)))
	}
	if err = request.Write(connection); err != nil {
		connection.Close()
		return nil, err
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, request)
	if err != nil || response.StatusCode != http.StatusOK {
		connection.Close()
		return nil, &Error{Code: "browser_proxy_connect_failed"}
	}
	_ = connection.SetDeadline(time.Time{})
	return &bufferedConnection{Conn: connection, reader: reader}, nil
}

type bufferedConnection struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConnection) Read(b []byte) (int, error) { return c.reader.Read(b) }
