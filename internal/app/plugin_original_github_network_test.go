package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOriginalGitHubHeadersPinnedSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-market/github-network-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Routes []struct {
			Mirror, Address string
			API             bool
			Attempts        []struct{ URL string }
		}
		Headers []struct {
			Token, Pairs, Repository string
			Headers                  map[string]string
		}
	}
	if json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("invalid fixed header oracle")
	}
	for _, row := range oracle.Routes {
		if row.Mirror != "" && !row.API {
			base, err := originalGitHubMirrorBase(row.Mirror)
			if err != nil || base+row.Address != row.Attempts[0].URL {
				t.Fatal("mirror URL differs from fixed UrlUtils and PluginHelper AST", err)
			}
		}
	}
	for _, row := range oracle.Headers {
		got := originalGitHubHeaders(Settings{GitHubToken: row.Token, RepoGitHubToken: row.Pairs}, row.Repository)
		actual := map[string]string{}
		for key := range got {
			actual[key] = got.Get(key)
		}
		if !reflect.DeepEqual(actual, row.Headers) {
			t.Fatal("header selection differs from fixed config AST; owned token values withheld")
		}
	}
}

func TestOriginalGitHubSettingsActualHTTPEncryptedPreservedAndSecretEvents(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventFixture(t, a)
	const token = "OWNED_GITHUB_GLOBAL_SENTINEL"
	const repoToken = "OWNED_GITHUB_REPO_SENTINEL"
	body := `{"github_proxy":"https://owned-mirror.invalid/prefix","github_token":"` + token + `","repo_github_token":"owner/repo:` + repoToken + `"}`
	anonymous := callTest(t, a.Handler(), "PUT", "/api/plugins/markets", body, nil, "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatal("plugin network settings bypassed existing app authentication")
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/markets", body, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	for _, path := range []string{"/api/settings", "/api/plugins/markets"} {
		response = callTest(t, a.Handler(), "GET", path, "", cookie, "")
		if response.Code != 200 || strings.Contains(response.Body.String(), token) || strings.Contains(response.Body.String(), repoToken) {
			t.Fatal("ordinary settings query exposed saved credentials")
		}
	}
	before := a.Settings()
	if before.GitHubToken != token || before.RepoGitHubToken != "owner/repo:"+repoToken {
		t.Fatal("actual committed credentials missing")
	}
	events := requireBusinessEventCount(t, a, ownedEventFixtureID, "config.updated", 1)
	if bytes.Contains(events[0].Data, []byte(token)) || bytes.Contains(events[0].Data, []byte(repoToken)) {
		t.Fatal("config event exposed saved credential")
	}
	var event struct {
		Key   []string
		Value json.RawMessage
	}
	if json.Unmarshal(events[0].Data, &event) != nil || !reflect.DeepEqual(event.Key, []string{"GITHUB_PROXY", "GITHUB_TOKEN", "REPO_GITHUB_TOKEN"}) || string(event.Value) != "null" {
		t.Fatal("credential change event did not carry key-only bulk semantics")
	}
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte(token)) || bytes.Contains(sealed, []byte(repoToken)) {
		t.Fatal("credential configuration was not encrypted", err)
	}
	var decoded Settings
	if err := a.Store.Unseal(sealed, &decoded); err != nil || decoded.GitHubToken != token {
		t.Fatal("encrypted configuration cannot restore credential", err)
	}
	if strings.Contains(a.clean(token+" "+repoToken), "SENTINEL") {
		t.Fatal("individual GitHub tokens escaped host redaction")
	}
	public := a.publicSettings()
	delete(public, "secret_state")
	public["github_proxy"] = "https://stale-mirror.invalid"
	raw, _ := json.Marshal(public)
	response = callTest(t, a.Handler(), "PUT", "/api/settings", string(raw), cookie, "")
	if response.Code != 200 || a.Settings().GitHubProxy != before.GitHubProxy || a.Settings().GitHubToken != token {
		t.Fatal("cached unrelated full settings overwrote plugin-network controls")
	}
	for _, invalid := range []string{`{"github_proxy":"https://user:private@owned.invalid"}`, `{"github_proxy":"ftp://owned.invalid"}`, `{"github_token":"bad\r\nheader"}`} {
		response = callTest(t, a.Handler(), "PUT", "/api/plugins/markets", invalid, cookie, "")
		if response.Code == 200 || a.Settings().GitHubToken != token || a.Settings().GitHubProxy != before.GitHubProxy {
			t.Fatal("invalid network settings changed saved state")
		}
	}
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/markets", `{"github_token":"","repo_github_token":""}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if a.Settings().GitHubToken != token || a.Settings().RepoGitHubToken != "owner/repo:"+repoToken {
		t.Fatal("blank secret fields did not retain existing host secret contract")
	}
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/markets", `{"clear_github_token":true,"clear_repo_github_token":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if a.Settings().GitHubToken != "" || a.Settings().RepoGitHubToken != "" {
		t.Fatal("explicit credential clear failed")
	}
}

func TestOriginalGitHubActualTLSMirrorFallbackAPIAndCredentialOrigin(t *testing.T) {
	// A fixture CA certifies the exact HTTPS names under test. Trust and dial
	// mapping are scoped to this one client; certificate checking stays enabled.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "owned GitHub protocol fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"raw.githubusercontent.com", "api.github.com"}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(certificate)
	var mu sync.Mutex
	seen := []string{}
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "Bearer owned-default"
		if strings.Contains(r.URL.Path, "owner/repo/") {
			want = "Bearer owned-specific"
		}
		if r.Header.Get("Authorization") != want || r.Header.Get("User-Agent") != originalGitHubUserAgent {
			t.Error("official HTTPS origin did not receive its own selected source headers")
		}
		mu.Lock()
		seen = append(seen, r.Host+r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"owned":true}`))
	}))
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	origin.StartTLS()
	defer origin.Close()
	var mirrorHits atomic.Int32
	var mirrorOK atomic.Bool
	var mirrorHTTPError atomic.Bool
	var mirrorWait atomic.Bool
	mirrorStarted := make(chan struct{}, 1)
	mirrorHTTPError.Store(true)
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mirrorHits.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("GitHub credential crossed to configured third-party mirror")
		}
		if !strings.HasPrefix(r.URL.Path, "/prefix/https://raw.githubusercontent.com/") {
			t.Error("mirror URL did not preserve source prefix plus absolute original URL")
		}
		if mirrorWait.Load() {
			mirrorStarted <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Connection", "close")
		if mirrorHTTPError.Load() {
			w.Header().Set("X-Owned-Mirror", "received-response")
			http.Error(w, "owned mirror failure", http.StatusServiceUnavailable)
			return
		}
		if !mirrorOK.Load() {
			closeOwnedGitHubMirrorConnection(t, w)
			return
		}
		_, _ = w.Write([]byte(`{"mirror":true}`))
	}))
	defer mirror.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(address)
		if host != "raw.githubusercontent.com" && host != "api.github.com" {
			t.Error("unexpected fixture outbound authority")
			return nil, context.Canceled
		}
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	}
	mirrorTransport := http.DefaultTransport.(*http.Transport).Clone()
	mirrorTransport.Proxy = nil
	transport := &originalGitHubTransport{base: base, mirror: mirrorTransport, settings: Settings{GitHubProxy: mirror.URL + "/prefix", GitHubToken: "owned-default", RepoGitHubToken: "owner/repo:owned-specific"}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	fetch := func(address string) (int, string, http.Header) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, address, nil)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("actual verified TLS request failed", err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatal("response body was closed before its caller consumed it", readErr)
		}
		if request.Header.Get("Authorization") != "" {
			t.Fatal("transport mutated the original redirect header source")
		}
		return response.StatusCode, string(body), response.Header
	}
	status, body, headers := fetch("https://raw.githubusercontent.com/owner/repo/main/index")
	mu.Lock()
	firstOriginHits := len(seen)
	mu.Unlock()
	if status != 503 || body != "owned mirror failure\n" || headers.Get("X-Owned-Mirror") != "received-response" || firstOriginHits != 0 || mirrorHits.Load() != 1 {
		t.Fatal("received mirror HTTP503 must remain the caller's response, not origin fallback", status, firstOriginHits, mirrorHits.Load())
	}
	mirrorHTTPError.Store(false)
	fetch("https://raw.githubusercontent.com/owner/repo/main/index")
	if mirrorHits.Load() != 2 {
		t.Fatal("non-API request did not try mirror first")
	}
	fetch("https://api.github.com/repos/owner/repo/releases/assets/1")
	if mirrorHits.Load() != 2 {
		t.Fatal("GitHub API request incorrectly used mirror")
	}
	fetch("https://raw.githubusercontent.com/other/repo/main/index")
	mirrorOK.Store(true)
	fetch("https://raw.githubusercontent.com/owner/repo/main/index")
	mu.Lock()
	count := len(seen)
	mu.Unlock()
	if count != 3 || mirrorHits.Load() != 4 {
		t.Fatal("mirror success did not short-circuit origin fallback", count, mirrorHits.Load())
	}
	mirrorWait.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://raw.githubusercontent.com/owner/repo/main/canceled", nil)
	completed := make(chan error, 1)
	go func() {
		response, e := client.Do(request)
		if response != nil {
			response.Body.Close()
		}
		completed <- e
	}()
	select {
	case <-mirrorStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("owned mirror was not reached")
	}
	cancel()
	select {
	case e := <-completed:
		if !errors.Is(e, context.Canceled) {
			t.Fatal("canceled mirror request did not retain cancellation", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled request did not finish")
	}
	mu.Lock()
	count = len(seen)
	mu.Unlock()
	if count != 3 || mirrorHits.Load() != 5 {
		t.Fatal("cancellation attempted an origin fallback", count, mirrorHits.Load())
	}
}

// An owned server closes its accepted connection without an HTTP response.
// This is a real transport failure, not an HTTP error response.
func closeOwnedGitHubMirrorConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	connection, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	_ = connection.Close()
}
