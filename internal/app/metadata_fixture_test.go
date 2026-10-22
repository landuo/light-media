package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"light-media/internal/catalog"
)

// fixedFixtureTransport never delegates an unexpected request. Its responder
// must be an in-memory fixture or ownedLoopbackFixtureTransport, not a default
// transport or a transport that can fall back to the public origin.
type fixedFixtureTransport struct {
	t       *testing.T
	host    string
	paths   []string
	apiKey  string
	respond func(*http.Request) (*http.Response, error)
}

func (f fixedFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	allowedPath := false
	if r != nil && r.URL != nil {
		for _, path := range f.paths {
			allowedPath = allowedPath || r.URL.Path == path
		}
	}
	if r == nil || r.URL == nil || r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != f.host || r.URL.User != nil || r.URL.Opaque != "" || r.URL.Fragment != "" || r.URL.RawPath != "" || !allowedPath || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" || r.URL.Query().Get("api_key") != f.apiKey || f.respond == nil {
		if f.t != nil {
			f.t.Error("unexpected synthetic fixture request was blocked")
		}
		return nil, errors.New("unexpected synthetic fixture request")
	}
	return f.respond(r)
}

func installMetadataFixture(t *testing.T, a *App, apiKey string, paths []string, respond func(*http.Request) (*http.Response, error)) *catalog.Client {
	t.Helper()
	a.cfgMu.Lock()
	a.config.TMDB = TMDBConfig{APIKey: apiKey}
	a.cfgMu.Unlock()
	settings := a.Settings()
	proxy, err := resolveProxyConfig(settings.ProxyHost)
	if err != nil {
		t.Fatal("synthetic metadata fixture requires a valid proxy configuration")
	}
	client := catalog.NewWithTransport(catalog.Credentials{APIKey: apiKey}, fixedFixtureTransport{t: t, host: "api.themoviedb.org", paths: paths, apiKey: apiKey, respond: respond})
	a.catMu.Lock()
	a.catClient, a.catCreds, a.catProxy = client, settings.TMDB, proxy
	a.catMu.Unlock()
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func fixtureLoopbackDestination(rawURL, listenerAddress string) (*url.URL, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme != "http" || target.Host != listenerAddress || target.User != nil || target.Path != "" || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" || target.Opaque != "" || target.Port() == "" {
		return nil, errors.New("fixture destination is not its owned loopback listener")
	}
	if ip := net.ParseIP(target.Hostname()); ip == nil || !ip.IsLoopback() {
		return nil, errors.New("fixture destination is not a literal loopback address")
	}
	return target, nil
}

// Only the exact listener of this test's own httptest server is dialable.
// The fresh transport has no environment proxy, DNS name, TLS dial hook,
// redirect client, or public-network fallback.
func ownedLoopbackFixtureTransport(t *testing.T, server *httptest.Server) http.RoundTripper {
	t.Helper()
	if server == nil || server.Listener == nil {
		t.Fatal("fixture requires an owned httptest listener")
	}
	target, err := fixtureLoopbackDestination(server.URL, server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != target.Host {
			return nil, errors.New("fixture refused an unowned dial target")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return imageTransport(func(r *http.Request) (*http.Response, error) {
		if r == nil || r.URL == nil {
			return nil, errors.New("fixture request has no URL")
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		clone.Host = target.Host
		return transport.RoundTrip(clone)
	})
}

func TestMetadataFixtureRejectsUnexpectedRequestsWithoutDelegating(t *testing.T) {
	delegated := 0
	transport := fixedFixtureTransport{host: "api.themoviedb.org", paths: []string{"/3/movie/123"}, apiKey: "synthetic-key", respond: func(*http.Request) (*http.Response, error) {
		delegated++
		return nil, errors.New("in-memory fixture sentinel")
	}}
	for _, rawURL := range []string{
		"https://unowned.invalid/3/movie/123?api_key=synthetic-key",
		"https://api.themoviedb.org.unowned.invalid/3/movie/123?api_key=synthetic-key",
		"http://api.themoviedb.org/3/movie/123?api_key=synthetic-key",
		"https://api.themoviedb.org:443/3/movie/123?api_key=synthetic-key",
		"https://api.themoviedb.org/3/movie/456?api_key=synthetic-key",
		"https://api.themoviedb.org/3/movie/123?api_key=unowned-key",
		"https://user@api.themoviedb.org/3/movie/123?api_key=synthetic-key",
	} {
		r, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal("invalid synthetic guard input")
		}
		if _, err = transport.RoundTrip(r); err == nil {
			t.Fatal("unexpected request was accepted")
		}
	}
	r, _ := http.NewRequest(http.MethodGet, "https://api.themoviedb.org/3/movie/123?api_key=synthetic-key", nil)
	r.Header.Set("Authorization", "Bearer synthetic-unexpected-token")
	if _, err := transport.RoundTrip(r); err == nil || delegated != 0 {
		t.Fatal("rejected request reached the fixture responder")
	}
	r.Header.Del("Authorization")
	_, _ = transport.RoundTrip(r)
	if delegated != 1 {
		t.Fatal("expected request did not reach the in-memory responder")
	}
}

func TestMetadataFixtureRejectsUnownedLoopbackDestinationsWithoutDialing(t *testing.T) {
	const owner = "127.0.0.1:12345"
	for _, destination := range []string{
		"http://127.0.0.1:12346", "http://[::1]:12345",
		"http://localhost:12345", "http://192.0.2.1:12345",
		"https://127.0.0.1:12345", "http://user@127.0.0.1:12345",
		"http://127.0.0.1:12345/path", "http://127.0.0.1:12345?query=value",
	} {
		if _, err := fixtureLoopbackDestination(destination, owner); err == nil {
			t.Fatal("unowned fixture destination was accepted")
		}
	}
	if _, err := fixtureLoopbackDestination("http://192.0.2.1:12345", "192.0.2.1:12345"); err == nil {
		t.Fatal("non-loopback listener was accepted")
	}
	if _, err := fixtureLoopbackDestination("http://"+owner, owner); err != nil {
		t.Fatal("owned literal loopback listener was rejected")
	}
}

func TestMetadataFixtureRetainsCatalogWithResolvedProxy(t *testing.T) {
	withoutProxyEnv(t)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	a := &App{}
	client := installMetadataFixture(t, a, "synthetic-key", []string{"/3/movie/123"}, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("in-memory fixture sentinel")
	})
	if a.catProxy.HTTP == "" || a.catalog() != client {
		t.Fatal("resolved environment proxy replaced the synthetic metadata client")
	}
}
