package app

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/artifacts"
)

type ownedGitHubRequest struct {
	Stage, Host, Path               string
	AuthExpected, UserAgentExpected bool
}
type ownedGitHubProcessFixture struct {
	CAFile, Mirror, PreferredProxy, EnvironmentProxy string
	mirrorOK, mirrorHTTPError, originFailed          atomic.Bool
	mu                                               sync.Mutex
	requests                                         []ownedGitHubRequest
	tunnels                                          []net.Conn
}

const ownedGitHubRepository = "https://github.com/owned-fixture/plugin-module"
const ownedGitHubRepoPath = "owned-fixture/plugin-module"
const ownedGitHubGlobalToken = "owned-global-github-token"
const ownedGitHubRepoToken = "owned-repository-github-token"

// Both proxies and all origin requests terminate at owned loopback fixtures.
// A temporary CA is trusted only by the dedicated child process; normal TLS
// verification, exact official HTTPS names and port443 remain in effect.
func newOwnedGitHubProcessFixture(t *testing.T, dir string, repositoryURL string, index artifacts.Index, v1, v2 []byte) *ownedGitHubProcessFixture {
	t.Helper()
	f := &ownedGitHubProcessFixture{}
	localRepository, err := url.Parse(repositoryURL)
	if err != nil || localRepository.Scheme != "http" || localRepository.Hostname() != "127.0.0.1" {
		t.Fatal("owned loopback repository required")
	}
	direct := http.DefaultTransport.(*http.Transport).Clone()
	direct.Proxy = nil
	t.Cleanup(direct.CloseIdleConnections)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(13), Subject: pkix.Name{CommonName: "owned plugin network protocol fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(2 * time.Hour), DNSNames: []string{"raw.githubusercontent.com", "api.github.com"}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert, err := tls.X509KeyPair(ca, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	if err != nil {
		t.Fatal(err)
	}
	f.CAFile = filepath.Join(dir, "owned-github-fixture-ca.pem")
	if err = os.WriteFile(f.CAFile, ca, 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(index)
	var copied artifacts.Index
	if json.Unmarshal(raw, &copied) != nil {
		t.Fatal("cannot copy owned index")
	}
	for i := range copied.Releases {
		copied.Releases[i].Artifacts[0].URL = fmt.Sprintf("https://api.github.com/repos/%s/releases/assets/owned-%d", ownedGitHubRepoPath, i+1)
	}
	record := func(stage string, r *http.Request, auth string) {
		f.mu.Lock()
		f.requests = append(f.requests, ownedGitHubRequest{stage, r.Host, r.URL.Path, r.Header.Get("Authorization") == auth, r.Header.Get("User-Agent") == originalGitHubUserAgent})
		f.mu.Unlock()
	}
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record("official-origin", r, "Bearer "+ownedGitHubRepoToken)
		if r.TLS == nil || r.Method != http.MethodGet {
			t.Error("owned official origin was not real HTTPS GET")
			http.Error(w, "owned request invalid", 400)
			return
		}
		if f.originFailed.Load() {
			http.Error(w, "owned origin failure", 503)
			return
		}
		if r.Host == "raw.githubusercontent.com" && r.URL.Path == "/"+ownedGitHubRepoPath+"/main/"+artifacts.IndexName {
			_ = json.NewEncoder(w).Encode(copied)
			return
		}
		if r.Host == "api.github.com" {
			switch r.URL.Path {
			case "/repos/" + ownedGitHubRepoPath + "/releases/assets/owned-1":
				_, _ = w.Write(v1)
				return
			case "/repos/" + ownedGitHubRepoPath + "/releases/assets/owned-2":
				_, _ = w.Write(v2)
				return
			}
		}
		t.Error("owned official fixture received unexpected host/path")
		http.NotFound(w, r)
	}))
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	origin.StartTLS()
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record("mirror", r, "")
		if r.Method != http.MethodGet || r.URL.Path != "/prefix/https://raw.githubusercontent.com/"+ownedGitHubRepoPath+"/main/"+artifacts.IndexName {
			t.Error("mirror used for API asset or unexpected request")
			http.Error(w, "owned mirror request invalid", 400)
			return
		}
		w.Header().Set("Connection", "close")
		if f.mirrorHTTPError.Load() {
			http.Error(w, "owned mirror HTTP response unavailable", http.StatusServiceUnavailable)
			return
		}
		if !f.mirrorOK.Load() {
			closeOwnedGitHubMirrorConnection(t, w)
			return
		}
		_ = json.NewEncoder(w).Encode(copied)
	}))
	preferred := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record("configured-proxy", r, "")
		if r.Method != http.MethodConnect {
			t.Error("configured proxy did not receive CONNECT")
		}
		http.Error(w, "owned configured proxy unavailable", 502)
	}))
	environment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// MoviePilot's explicit PROXY mapping also takes environment values and
		// deliberately ignores NO_PROXY. Forward only the exact owned local
		// repository used by the preceding lifecycle checks.
		if r.Method == http.MethodGet && r.URL.Scheme == "http" && r.URL.Host == localRepository.Host {
			if r.Header.Get("Authorization") != "" {
				t.Error("GitHub credential crossed to owned ordinary repository")
			}
			outgoing := r.Clone(r.Context())
			outgoing.RequestURI = ""
			response, err := direct.RoundTrip(outgoing)
			if err != nil {
				http.Error(w, "owned repository unavailable", 502)
				return
			}
			defer response.Body.Close()
			for key, values := range response.Header {
				w.Header()[key] = values
			}
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, response.Body)
			return
		}
		record("environment-proxy", r, "")
		if r.Method != http.MethodConnect || (r.Host != "raw.githubusercontent.com:443" && r.Host != "api.github.com:443") {
			t.Error("environment proxy destination escaped owned allowlist")
			http.Error(w, "owned proxy destination invalid", 400)
			return
		}
		upstream, err := net.DialTimeout("tcp", origin.Listener.Addr().String(), 5*time.Second)
		if err != nil {
			http.Error(w, "owned origin unavailable", 502)
			return
		}
		client, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			t.Error(err)
			return
		}
		f.mu.Lock()
		f.tunnels = append(f.tunnels, client, upstream)
		f.mu.Unlock()
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if err = rw.Flush(); err != nil {
			client.Close()
			upstream.Close()
			return
		}
		go func(reader *bufio.Reader) { _, _ = io.Copy(upstream, reader); upstream.Close(); client.Close() }(rw.Reader)
		_, _ = io.Copy(client, upstream)
		client.Close()
		upstream.Close()
	}))
	f.Mirror, f.PreferredProxy, f.EnvironmentProxy = mirror.URL+"/prefix", preferred.URL, environment.URL
	t.Cleanup(func() {
		preferred.Close()
		environment.Close()
		mirror.Close()
		f.mu.Lock()
		for _, connection := range f.tunnels {
			_ = connection.Close()
		}
		f.mu.Unlock()
		origin.Close()
	})
	return f
}
func (f *ownedGitHubProcessFixture) childEnvironment() []string {
	return []string{"SSL_CERT_FILE=" + f.CAFile, "HTTPS_PROXY=" + f.EnvironmentProxy, "https_proxy=" + f.EnvironmentProxy, "HTTP_PROXY=" + f.EnvironmentProxy, "http_proxy=" + f.EnvironmentProxy, "ALL_PROXY=", "all_proxy=", "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost"}
}
func (f *ownedGitHubProcessFixture) resetRequests() { f.mu.Lock(); f.requests = nil; f.mu.Unlock() }
func (f *ownedGitHubProcessFixture) snapshot() []ownedGitHubRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ownedGitHubRequest{}, f.requests...)
}
func (f *ownedGitHubProcessFixture) check(t *testing.T, want []string) {
	t.Helper()
	got := f.snapshot()
	stages := []string{}
	for _, call := range got {
		stages = append(stages, call.Stage)
		if !call.AuthExpected {
			t.Fatal("credential was missing at its own origin or crossed a mirror/proxy boundary", call.Stage)
		}
		if (call.Stage == "official-origin" || call.Stage == "mirror") && !call.UserAgentExpected {
			t.Fatal("pinned selected User-Agent not preserved", call.Stage)
		}
	}
	if strings.Join(stages, ",") != strings.Join(want, ",") {
		t.Fatal("actual production network order differs from source fallback", stages, want)
	}
}

func verifyOwnedGitHubProductionNetwork(t *testing.T, f *ownedGitHubProcessFixture, repository string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	// All credentials are owned sentinels. No Bitwarden or user token is read.
	success("PUT", "/api/plugins/markets", map[string]any{"github_proxy": f.Mirror, "github_token": ownedGitHubGlobalToken, "repo_github_token": ownedGitHubRepoPath + ":" + ownedGitHubRepoToken, "plugin_markets": []string{ownedGitHubRepository}})
	// Ordinary settings use the same current encrypted host store and preserve
	// the plugin settings section while adding only this owned failing proxy.
	var settingsResponse struct{ Data map[string]any }
	if json.Unmarshal(request("GET", "/api/settings", nil, 200), &settingsResponse) != nil || settingsResponse.Data == nil {
		t.Fatal("public settings unavailable")
	}
	settings := settingsResponse.Data
	delete(settings, "secret_state")
	settings["proxy_host"] = f.PreferredProxy
	request("PUT", "/api/settings", settings, 200)
	f.resetRequests()
	response := request("POST", "/api/plugins/install", map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": ownedGitHubRepository, "force": true}, 200)
	var installed struct{ Data struct{ Success bool } }
	if json.Unmarshal(response, &installed) != nil || !installed.Data.Success {
		t.Fatal("actual production GitHub install failed")
	}
	f.check(t, []string{"mirror", "configured-proxy", "environment-proxy", "official-origin", "configured-proxy", "environment-proxy", "official-origin"})
	// API assets skip the mirror, while per-repository PAT is sent only inside
	// verified TLS to the exact origin. Actual worker API proves acquisition.
	success("PUT", "/api/plugins/"+ownedStandardFixtureID, map[string]any{"enabled": true, "key": "owned-github-network-value"})
	var workerBefore map[string]any
	if json.Unmarshal(request("GET", "/api/v1/plugin/"+ownedStandardFixtureID+"/echo", nil, 200), &workerBefore) != nil {
		t.Fatal("installed owned SDK unavailable")
	}
	// A complete mirror HTTP error is not a transport exception. A forced
	// install must fail without contacting origin or disturbing the live worker.
	f.mirrorHTTPError.Store(true)
	f.resetRequests()
	response = request("POST", "/api/plugins/install", map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": ownedGitHubRepository, "force": true}, 200)
	if json.Unmarshal(response, &installed) != nil || installed.Data.Success {
		t.Fatal("mirror HTTP503 incorrectly fell back and installed")
	}
	f.check(t, []string{"mirror"})
	var httpFailureWorker map[string]any
	if json.Unmarshal(request("GET", "/api/v1/plugin/"+ownedStandardFixtureID+"/echo", nil, 200), &httpFailureWorker) != nil {
		t.Fatal("mirror HTTP response failure stopped the worker")
	}
	for _, field := range []string{"nonce", "init_count", "config", "value"} {
		if !originalBaseJSONEqual(mustCleanSeedJSON(workerBefore[field]), mustCleanSeedJSON(httpFailureWorker[field])) {
			t.Fatal("mirror HTTP response failure changed live worker/config/KV", field)
		}
	}
	f.mirrorHTTPError.Store(false)
	f.mirrorOK.Store(true)
	f.resetRequests()
	request("GET", "/api/plugins/history/"+ownedStandardFixtureID+"?force=true", nil, 200)
	f.check(t, []string{"mirror"})
	f.mirrorOK.Store(false)
	f.originFailed.Store(true)
	f.resetRequests()
	response = request("POST", "/api/plugins/install", map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": ownedGitHubRepository, "force": true}, 200)
	if json.Unmarshal(response, &installed) != nil || installed.Data.Success {
		t.Fatal("all failed network routes accepted an installation")
	}
	f.check(t, []string{"mirror", "configured-proxy", "environment-proxy", "official-origin"})
	var workerAfter map[string]any
	if json.Unmarshal(request("GET", "/api/v1/plugin/"+ownedStandardFixtureID+"/echo", nil, 200), &workerAfter) != nil {
		t.Fatal("failed source request stopped actual installed SDK")
	}
	for _, field := range []string{"nonce", "init_count", "config", "value"} {
		if !originalBaseJSONEqual(mustCleanSeedJSON(workerBefore[field]), mustCleanSeedJSON(workerAfter[field])) {
			t.Fatal("network failure replaced existing worker state", field)
		}
	}
	f.originFailed.Store(false)
	f.resetRequests()
	stop()
	start()
	// The startup sync and this ordinary read share one source cache/pending
	// fetch. Await it before checking the actual cold-process route sequence.
	request("GET", "/api/plugins/market?details=true", nil, 200)
	f.check(t, []string{"mirror", "configured-proxy", "environment-proxy", "official-origin"})
	var coldWorker map[string]any
	if json.Unmarshal(request("GET", "/api/v1/plugin/"+ownedStandardFixtureID+"/echo", nil, 200), &coldWorker) != nil || coldWorker["nonce"] == workerAfter["nonce"] || !originalBaseJSONEqual(mustCleanSeedJSON(coldWorker["config"]), mustCleanSeedJSON(workerBefore["config"])) || !originalBaseJSONEqual(mustCleanSeedJSON(coldWorker["value"]), mustCleanSeedJSON(workerBefore["value"])) {
		t.Fatal("cold production SDK lost actual process/config/KV recovery")
	}
	success("DELETE", "/api/plugins/"+ownedStandardFixtureID, nil)
	success("PUT", "/api/plugins/markets", map[string]any{"github_proxy": "", "clear_github_token": true, "clear_repo_github_token": true, "plugin_markets": []string{repository + "/index"}})
	if json.Unmarshal(request("GET", "/api/settings", nil, 200), &settingsResponse) != nil || settingsResponse.Data == nil {
		t.Fatal("public restore unavailable")
	}
	settings = settingsResponse.Data
	delete(settings, "secret_state")
	settings["clear_proxy_host"] = true
	request("PUT", "/api/settings", settings, 200)
	t.Log("real production GitHub network verified: owned strict TLS origins; mirror→configured proxy→environment fallback, API asset skips mirror; repository PAT never reaches mirror/CONNECT; actual Go artifact install/SDK call; all-route failure preserves live PID/config/KV; restart restores encrypted headers/proxy; own network settings cleared")
}
