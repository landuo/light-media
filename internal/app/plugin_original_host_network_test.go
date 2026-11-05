package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func ownedHostNetwork(t *testing.T) (*App, *originalPluginInstance) {
	t.Helper()
	worker := os.Getenv("OWNED_HOST_NETWORK_FIXTURE_BINARY")
	if worker == "" {
		t.Fatal("OWNED_HOST_NETWORK_FIXTURE_BINARY must name the actual SDK fixture")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	manifest := installOwnedResourceProcessWorker(t, a, cookie, worker)
	object, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok {
		t.Fatal("installed fixture not running")
	}
	return a, object.(*originalPluginInstance)
}
func ownedNetworkCall(t *testing.T, object *originalPluginInstance, address string, github bool) (plugins.NetworkGetResultV1, error) {
	t.Helper()
	raw, _ := json.Marshal(plugins.NetworkGetRequestV1{URL: address, GitHubHeaders: github})
	response, err := object.callOriginalMethod(context.Background(), "resource", raw)
	var out plugins.NetworkGetResultV1
	if err == nil {
		err = json.Unmarshal(response.Result, &out)
	}
	return out, err
}

func TestOriginalHostNetworkActualSDKScalarTimeout(t *testing.T) {
	a, worker := ownedHostNetwork(t)
	// Fixed RequestUtils + requests 2.34.2 in timeout-oracle.json permits
	// progress beyond its scalar timeout; an idle socket must still fail.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/idle" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(18 * time.Second):
				w.WriteHeader(200)
				return
			}
		}
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(200)
		for i := 0; i < 5; i++ {
			_, _ = w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			if i < 4 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(4 * time.Second):
				}
			}
		}
	}))
	defer proxy.Close()
	settings := a.Settings()
	settings.ProxyHost = proxy.URL
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out, err := ownedNetworkCall(t, worker, "http://owned.test/progress", false)
	if err != nil || out.Response == nil || string(out.Response.Content) != "xxxxx" || time.Since(start) < 15*time.Second {
		t.Fatal("source progressing response truncated by total deadline", out, err)
	}
	start = time.Now()
	out, err = ownedNetworkCall(t, worker, "http://owned.test/idle", false)
	if err != nil || out.Response != nil || time.Since(start) < 14*time.Second || time.Since(start) > 18*time.Second {
		t.Fatal("source 15 second idle timeout not preserved", out, err, time.Since(start))
	}
}

func TestOriginalHostNetworkActualSDKUntrustedTLS(t *testing.T) {
	// Explicit project credential/TLS policy adaptation: pinned RequestUtils
	// uses verify=False, while the existing Go host requires trusted TLS.
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, "")
	}
	a, worker := ownedHostNetwork(t)
	var reached atomic.Int32
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		_, _ = io.WriteString(w, "owned-untrusted-content")
	}))
	defer peer.Close()
	settings := a.Settings()
	settings.ProxyHost = ""
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	out, err := ownedNetworkCall(t, worker, peer.URL, true)
	if err != nil || out.Response != nil || reached.Load() != 0 {
		t.Fatal("untrusted TLS exposed a response", out, err, reached.Load())
	}
}

func TestOriginalHostNetworkActualSDKFixedGETAndCompression(t *testing.T) {
	a, worker := ownedHostNetwork(t)
	var oracle struct {
		Commit string
		Cases  []struct {
			Name     string
			Response *struct {
				StatusCode int    `json:"status_code"`
				Content    string `json:"content_b64"`
			}
			Calls []any
		}
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-network-20261031/source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 10 {
		t.Fatal("fixed source GET oracle unavailable", err)
	}
	mu := sync.Mutex{}
	counts := map[string]int{}
	statuses := map[string]int{}
	bodies := map[string][]byte{}
	encodings := map[string]string{}
	for _, sample := range oracle.Cases {
		if sample.Response != nil {
			statuses["/"+sample.Name] = sample.Response.StatusCode
			bodies["/"+sample.Name], _ = base64.StdEncoding.DecodeString(sample.Response.Content)
		}
	}
	type vendorOracle struct {
		RequestsVersion string `json:"requests_version"`
		CanonicalHeader struct {
			Content string `json:"content_b64"`
		} `json:"go_header_ows_adaptation"`
		Cases []struct {
			Name, Encoding string
			Wire           string  `json:"wire_b64"`
			Content        *string `json:"content_b64"`
			Exception      *string
		}
	}
	var vendor vendorOracle
	vraw, err := os.ReadFile("../../testdata/plugin-host-network-20261031/vendor-content-oracle.json")
	if err != nil || json.Unmarshal(vraw, &vendor) != nil || vendor.RequestsVersion != "2.34.2" || len(vendor.Cases) != 7 {
		t.Fatal("deployed Response.content oracle unavailable", err)
	}
	var edges vendorOracle
	eraw, err := os.ReadFile("../../testdata/plugin-host-network-20261031/vendor-http-oracle.json")
	if err != nil || json.Unmarshal(eraw, &edges) != nil || edges.RequestsVersion != "2.34.2" || len(edges.Cases) != 15 {
		t.Fatal("actual source HTTP decoder oracle unavailable", err)
	}
	for i := range edges.Cases {
		if edges.Cases[i].Name == "single-whitespace" {
			// net/http normalizes header OWS. The source's actual GET with
			// that canonical header is separately captured, not invented.
			edges.Cases[i].Content = &edges.CanonicalHeader.Content
		}
	}
	vendor.Cases = append(vendor.Cases, edges.Cases...)
	decodedBodies := map[string][]byte{}
	for _, sample := range vendor.Cases {
		path := "/codec-" + sample.Name
		statuses[path] = 200
		bodies[path], _ = base64.StdEncoding.DecodeString(sample.Wire)
		encodings[path] = sample.Encoding
		if sample.Content != nil {
			decodedBodies[path], _ = base64.StdEncoding.DecodeString(*sample.Content)
		}
	}
	var wireOracle struct {
		Ordinary map[string]string
	}
	headerRaw, err := os.ReadFile("../../testdata/plugin-host-network-20261031/wire-headers-oracle.json")
	if err != nil || json.Unmarshal(headerRaw, &wireOracle) != nil || len(wireOracle.Ordinary) != 4 {
		t.Fatal("actual source wire headers missing", err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		if r.Method != "GET" || r.URL.Host != "owned.test" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("GET origin or credential boundary failed")
		}
		for key, expected := range wireOracle.Ordinary {
			if r.Header.Get(key) != expected {
				t.Errorf("source wire header %s differs: %q != %q", key, r.Header.Get(key), expected)
			}
		}
		if r.Header.Get("Accept") != "" {
			t.Error("source ordinary RequestUtils removes session Accept")
		}
		if status, ok := statuses[r.URL.Path]; ok {
			w.Header().Set("Content-Encoding", encodings[r.URL.Path])
			w.WriteHeader(status)
			_, _ = w.Write(bodies[r.URL.Path])
			return
		}
		c, _, _ := w.(http.Hijacker).Hijack()
		_ = c.Close()
	}))
	defer proxy.Close()
	settings := a.Settings()
	settings.ProxyHost = proxy.URL
	settings.GitHubToken = "owned-global-secret"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			out, err := ownedNetworkCall(t, worker, "http://owned.test/"+sample.Name, true)
			if err != nil {
				t.Fatal(err)
			}
			if sample.Response == nil {
				if out.Response != nil {
					t.Fatal("source request exception did not produce None")
				}
			} else {
				want, _ := base64.StdEncoding.DecodeString(sample.Response.Content)
				if out.Response == nil || out.Response.StatusCode != sample.Response.StatusCode || !bytes.Equal(out.Response.Content, want) {
					t.Fatal("GET altered source status or content")
				}
			}
			mu.Lock()
			n := counts["/"+sample.Name]
			mu.Unlock()
			if n != len(sample.Calls) {
				t.Fatalf("source GET without Session attempts=%d target=%d", len(sample.Calls), n)
			}
		})
	}
	for path, encoding := range encodings {
		t.Run(path, func(t *testing.T) {
			out, err := ownedNetworkCall(t, worker, "http://owned.test"+path, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, hasContent := decodedBodies[path]; !hasContent {
				if out.Response != nil {
					t.Fatal("bad content coding must produce source None")
				}
				return
			}
			if out.Response == nil || !bytes.Equal(out.Response.Content, decodedBodies[path]) {
				t.Fatalf("decoded response differs for %s", encoding)
			}
		})
	}
}

func TestOriginalHostNetworkActualSDKRedirectGuardsAndCancellation(t *testing.T) {
	a, worker := ownedHostNetwork(t)
	mu := sync.Mutex{}
	calls := []string{}
	entered := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/redirect":
			http.SetCookie(w, &http.Cookie{Name: "owned", Value: "redirect", Path: "/"})
			http.Redirect(w, r, "http://owned.test/cookie", 302)
		case "/cookie":
			if c, e := r.Cookie("owned"); e != nil || c.Value != "redirect" {
				t.Error("source per-request redirect cookie missing")
			}
			_, _ = fmt.Fprint(w, "redirected")
		case "/changed":
			a.cfgMu.Lock()
			a.config.ProxyHost = ""
			a.cfgMu.Unlock()
			_, _ = fmt.Fprint(w, "stale")
		case "/blocked":
			close(entered)
			<-r.Context().Done()
		default:
			_, _ = fmt.Fprint(w, "owned")
		}
	}))
	defer proxy.Close()
	settings := a.Settings()
	settings.ProxyHost = proxy.URL
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	out, err := ownedNetworkCall(t, worker, "http://owned.test/redirect", false)
	if err != nil || out.Response == nil || string(out.Response.Content) != "redirected" {
		t.Fatal("actual redirect/cookie path", err)
	}
	mu.Lock()
	before := len(calls)
	mu.Unlock()
	for _, invalid := range []string{"file:///owned", "https://user:private@owned.test/file", "http://owned.test/file#fragment"} {
		if _, err := ownedNetworkCall(t, worker, invalid, true); err == nil {
			t.Fatal("invalid network selector accepted")
		}
	}
	mu.Lock()
	after := len(calls)
	mu.Unlock()
	if after != before {
		t.Fatal("invalid selector contacted peer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := worker.callOriginalMethod(ctx, "resource", json.RawMessage(`{"url":"http://owned.test/blocked"}`))
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owned request did not reach peer")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled invocation returned success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled native request did not return")
	}
	if _, err := ownedNetworkCall(t, worker, "http://owned.test/changed", false); err == nil {
		t.Fatal("changed host network configuration returned stale response")
	}
}
