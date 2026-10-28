package artifacts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

type repositoryReplayTransport func(*http.Request) (*http.Response, error)

func (f repositoryReplayTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubRepositoryRootSelectsGoIndexOrDisabledOriginalPackageMetadata(t *testing.T) {
	req, _, index, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
	var mode, payloads, metadata atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Owner/Repo/main/" + IndexName:
			if mode.Load() != 0 {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(RepositoryIndex{SchemaVersion: 1, Plugins: []Index{*index}})
		case "/Owner/Repo/main/package.v2.json":
			metadata.Add(1)
			if mode.Load() == 2 {
				http.Error(w, "owned outage", 503)
				return
			}
			w.Write([]byte(`{"Original":{"name":"原插件","version":"2.0"}}`))
		case "/Owner/Repo/main/package.json":
			metadata.Add(1)
			w.Write([]byte(`{"Original":{"name":"原插件","version":"1.0","v2":true},"Legacy":{"name":"旧版","version":"9.0"},"Base":{"name":"基础兼容插件","version":"1.0","v2":true}}`))
		case "/payload":
			payloads.Add(1)
			w.Write([]byte("inert-owned-bytes"))
		default:
			t.Error("unexpected request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	index.Releases[0].Artifacts[0].URL = server.URL + "/payload"
	local, _ := url.Parse(server.URL)
	client := &http.Client{Transport: repositoryReplayTransport(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		target := *r.URL
		if target.Host == "raw.githubusercontent.com" {
			if !strings.HasPrefix(target.Path, "/Owner/Repo/main/") {
				t.Fatal("escaped source repository", target.Path)
			}
			target.Scheme, target.Host = local.Scheme, local.Host
		} else if target.Host != local.Host {
			t.Fatal("unexpected external request", target.Host)
		}
		copy.URL, copy.Host = &target, target.Host
		return http.DefaultTransport.RoundTrip(copy)
	})}
	const repository = "https://github.com/Owner/Repo.git/"
	values, err := DiscoverRepository(context.Background(), repository, req.Platform, req.Validate, client)
	if err != nil || len(values) != 1 || values[0].PluginID != "Owned" || values[0].UnavailableReason != "" || metadata.Load() != 0 || payloads.Load() != 0 {
		t.Fatal(values, err)
	}
	req.RepositoryURL = repository
	if got, err := Acquire(context.Background(), req, client); err != nil || got.RepositoryURL != repository {
		t.Fatal(got, err)
	}
	if payloads.Load() != 1 {
		t.Fatal("Go root did not fetch exact payload")
	}
	mode.Store(1)
	values, err = DiscoverRepository(context.Background(), repository+"?_refresh=123", req.Platform, req.Validate, client)
	if err != nil || len(values) != 3 || values[0].PluginID != "Original" || values[0].CurrentVersion != "2.0" || values[1].PluginID != "Original" || values[2].PluginID != "Base" {
		t.Fatal("package branch order/v2 filtering", values, err)
	}
	for _, value := range values {
		if value.UnavailableReason == "" || value.Current.Manifest.Runtime != "python" {
			t.Fatal("Python package became installable", value)
		}
	}
	req.PluginID = "Original"
	if _, err := Acquire(context.Background(), req, client); err == nil {
		t.Fatal("Python root was installed")
	}
	if metadata.Load() != 2 || payloads.Load() != 1 {
		t.Fatal("install path parsed Python or fetched executable", metadata.Load(), payloads.Load())
	}
	mode.Store(2)
	if _, err := DiscoverRepository(context.Background(), repository, req.Platform, req.Validate, client); err == nil {
		t.Fatal("remote metadata outage claimed empty success")
	}
	for _, bad := range []string{"https://github.com/Owner/Repo?token=private", "https://github.com/Owner/Repo/tree/main", "https://user@github.com/Owner/Repo", "https://github.com.evil/Owner/Repo", "https://github.com/../Repo"} {
		if _, ok := githubRepositoryBase(bad); ok {
			t.Fatal("unexpected root normalization", bad)
		}
	}
}
