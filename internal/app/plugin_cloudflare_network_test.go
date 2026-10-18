package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	cf "light-media/internal/plugins/cloudflarespeedtest"
)

func TestPluginCloudflareNetworkClientsUseSeparateActualRoutes(t *testing.T) {
	withoutProxyEnv(t)
	a := newTestApp(t)
	var direct, proxied atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		http.Error(w, "owned direct failure", 503)
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		if r.URL.String() != origin.URL+"/release" {
			t.Error("wrong proxy target")
		}
		io.WriteString(w, "owned proxy success")
	}))
	defer proxy.Close()
	a.cfgMu.Lock()
	a.config.ProxyHost = proxy.URL
	a.cfgMu.Unlock()
	first, retry, configured, close, err := a.cloudflareNetworkClients()
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if !configured {
		t.Fatal("HTTPS PROXY mapping missing")
	}
	response, err := first.Get(origin.URL + "/release")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatal("first route not preserved")
	}
	response, err = retry.Get(origin.URL + "/release")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "owned proxy success" || direct.Load() != 1 || proxied.Load() != 1 {
		t.Fatal("actual proxy retry not separate")
	}
}

func TestPluginCloudflareArchiveRouteOriginalBranches(t *testing.T) {
	req := cf.InstallRequest{URL: cf.ReleasePrefix + "/v2.3.5/cfst_linux_amd64.tar.gz", Platform: cf.Platform{OS: "linux"}}
	t.Setenv("PROXY_HOST", "https://owned-mirror.invalid/base/")
	url, proxy, err := cloudflareDownloadRoute(req, false)
	if err != nil || proxy || url != "https://owned-mirror.invalid/base/"+req.URL {
		t.Fatal("Unix mirror branch changed", err)
	}
	url, proxy, err = cloudflareDownloadRoute(req, true)
	if err != nil || !proxy || url != req.URL {
		t.Fatal("Unix HTTPS proxy branch changed", err)
	}
	req.Platform.OS = "windows"
	url, proxy, err = cloudflareDownloadRoute(req, false)
	if err != nil || !proxy || url != req.URL {
		t.Fatal("Windows canonical branch changed", err)
	}
	req.Platform.OS = "linux"
	t.Setenv("PROXY_HOST", "")
	if _, _, err = cloudflareDownloadRoute(req, false); err == nil {
		t.Fatal("explicit malformed mirror silently replaced")
	}
}

func TestPluginCloudflareActualSDKDirectFailureThenProxySuccess(t *testing.T) {
	a := newTestApp(t)
	cookie, downloads := cloudflareTestInstall(t, a)
	original := a.pluginCloudflare.client
	var first, retry atomic.Int32
	a.pluginCloudflare.proxyClient = &http.Client{Transport: cloudflareTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == cf.ReleaseAPI {
			retry.Add(1)
		}
		return original.Transport.RoundTrip(r)
	})}
	a.pluginCloudflare.client = &http.Client{Transport: cloudflareTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == cf.ReleaseAPI {
			first.Add(1)
			return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(http.NoBody)}, nil
		}
		return original.Transport.RoundTrip(r)
	})}
	cloudflareTestConfigure(t, a, cookie, cloudflareTestConfig("-h"))
	if _, err := a.pluginStore.EnqueueRun(context.Background(), cf.PluginID, "run", nil); err != nil {
		t.Fatal(err)
	}
	cloudflareTestExecute(t, a, cloudflareTestClaim(t, a, cf.PluginID))
	if first.Load() != 1 || retry.Load() != 1 || downloads.Load() != 1 {
		t.Fatal("actual SDK source fallback/archive path incomplete", first.Load(), retry.Load(), downloads.Load())
	}
	state, _, err := a.pluginStore.cloudflareRead(context.Background(), a.Store.DB, cf.PluginID, 1)
	if err != nil || len(state.Artifacts) != 1 {
		t.Fatal(fmt.Sprint("official archive verification missing: ", err))
	}
}
