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
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	source "light-media/internal/plugins/autosignin"
	signinjournal "light-media/internal/plugins/autosignin/durable"
)

const signinTjuptImageOrigin = "https://images.synthetic.test"

// All domain traffic terminates inside this owned CONNECT fixture. The actual
// compiled hostnames are certificate-verified; no DNS or production contact is
// needed, and neither the machine roots nor TLS verification are changed.
func signinTjuptCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Owned synthetic Tjupt root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Owned synthetic Tjupt endpoints"}, DNSNames: []string{"www.tjupt.org", "movie.douban.com", "images.synthetic.test"}, NotBefore: root.NotBefore, NotAfter: root.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{leafDER, rootDER}, PrivateKey: leafKey}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
}

type signinTjuptStartupFixture struct {
	mu    sync.Mutex
	value autoSignInTjuptStartup
}

func (s *signinTjuptStartupFixture) read() autoSignInTjuptStartup {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.value
	v.RootCAPEM = append([]byte(nil), v.RootCAPEM...)
	return v
}
func (s *signinTjuptStartupFixture) set(v autoSignInTjuptStartup) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = v
	s.value.RootCAPEM = append([]byte(nil), v.RootCAPEM...)
}

type signinTjuptTLSFixture struct {
	server           *httptest.Server
	environmentProxy *httptest.Server
	siteProxy        *httptest.Server
	roots            []byte
	mu               sync.Mutex
	requests         []string
	proxyRequests    map[string][]string
	before           func(http.ResponseWriter, *http.Request) bool
	posts            atomic.Int32
}

func signinTjuptTLS(t *testing.T) *signinTjuptTLSFixture {
	t.Helper()
	certificate, roots := signinTjuptCertificate(t)
	f := &signinTjuptTLSFixture{roots: roots, proxyRequests: map[string][]string{}}
	img := image.NewGray(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(y*10 + x)})
		}
	}
	var imageBody bytes.Buffer
	if err := png.Encode(&imageBody, img); err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.Host+r.URL.RequestURI())
		hook := f.before
		f.mu.Unlock()
		w.Header().Set("Connection", "close")
		if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "" {
			t.Error("authentication leaked through TLS")
		}
		if r.Host == "www.tjupt.org" {
			if r.Header.Get("Cookie") != "sid=synthetic-native" || r.Header.Get("User-Agent") != "Owned-signin-UA" {
				t.Error("trusted site identity changed")
			}
		} else if r.Header.Get("Cookie") != "" || r.Header.Get("User-Agent") == "Owned-signin-UA" {
			t.Error("site identity leaked to public endpoint")
		}
		if hook != nil && hook(w, r) {
			return
		}
		switch r.Host + r.URL.Path {
		case "www.tjupt.org/attendance.php":
			if r.Method == "POST" {
				f.posts.Add(1)
				if err := r.ParseForm(); err != nil || r.Form.Get("ban_robot") != "1" || r.Form.Get("submit") != "提交" {
					t.Error("source answer form changed")
				}
				fmt.Fprint(w, "这是您的首次签到，本次签到获得10个魔力值。")
			} else {
				fmt.Fprint(w, `<table class="captcha"><tr><td><img src="/captcha.png"></td></tr></table><p><input name="ban_robot" value="1">甲片<input name="ban_robot" value="2">乙片</p>`)
			}
		case "www.tjupt.org/captcha.png", "images.synthetic.test/synthetic.png":
			if r.Host == "images.synthetic.test" && r.Header.Get("Referer") != "https://movie.douban.com" {
				t.Error("incorrect public image referer")
			}
			w.Header().Set("Content-Type", "image/png")
			w.Write(imageBody.Bytes())
		case "movie.douban.com/j/subject_suggest":
			if r.URL.Query().Get("q") != "甲片" {
				t.Error("source option label changed")
			}
			w.Header().Set("Set-Cookie", "untrusted=never-replayed; Path=/; Secure")
			fmt.Fprint(w, `[{"img":"`+signinTjuptImageOrigin+`/synthetic.png"}]`)
		default:
			t.Error("unrecognized synthetic request", r.Host, r.URL.Path)
			http.Error(w, "not found", 404)
		}
	}))
	f.server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	f.server.StartTLS()
	newProxy := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			f.proxyRequests[name] = append(f.proxyRequests[name], r.Host)
			f.mu.Unlock()
			if r.Method != "CONNECT" || (r.Host != "www.tjupt.org:443" && r.Host != "movie.douban.com:443" && r.Host != "images.synthetic.test:443") {
				t.Error("ungranted CONNECT target", r.Method, r.Host)
				http.Error(w, "denied", 403)
				return
			}
			// Never dial the requested hostname. Only the owned loopback server.
			upstream, err := net.Dial("tcp", f.server.Listener.Addr().String())
			if err != nil {
				t.Error(err)
				return
			}
			defer upstream.Close()
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
			done := make(chan struct{})
			go func() { io.Copy(upstream, conn); upstream.Close(); close(done) }()
			io.Copy(conn, upstream)
			conn.Close()
			<-done
		}))
	}
	f.environmentProxy = newProxy("environment")
	f.siteProxy = newProxy("site")
	t.Cleanup(func() { f.siteProxy.Close(); f.environmentProxy.Close(); f.server.Close() })
	return f
}
func (f *signinTjuptTLSFixture) setBefore(fn func(http.ResponseWriter, *http.Request) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.before = fn
}

func (f *signinTjuptTLSFixture) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }
func (f *signinTjuptTLSFixture) configure(a *App) *signinTjuptStartupFixture {
	startup := &signinTjuptStartupFixture{}
	startup.set(autoSignInTjuptStartup{ImageOriginsJSON: `["` + signinTjuptImageOrigin + `"]`, RootCAPEM: f.roots})
	a.autoSignInTjuptStartup = startup.read
	a.autoSignInEnvironment = func() []string { return []string{"https_proxy=" + f.environmentProxy.URL} }
	return startup
}
func signinTjuptSettings(a *App, f *signinTjuptTLSFixture) Settings {
	settings := a.Settings()
	settings.ProxyHost = f.siteProxy.URL
	settings.Sites = []SiteConfig{signinSitesNative("https://www.tjupt.org")}
	settings.Sites[0].Proxy = true
	return settings
}

func TestAutoSignInTjuptStartupTrustStrictJSONAndPrivateSnapshot(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `"https://images.synthetic.test"`, `[null]`, `[1]`, `["http://images.synthetic.test"]`, `["https://*.synthetic.test"]`, `["https://user@images.synthetic.test"]`, `["https://images.synthetic.test/path"]`, `["https://images.synthetic.test?"]`, `["https://images.synthetic.test#x"]`, `["https://images.synthetic.test#"]`, `["https://images.synthetic.test:0"]`, `["https://images.synthetic.test:65536"]`, `["https://images.synthetic.test:"]`, `[] {}`} {
		t.Run(raw, func(t *testing.T) {
			if _, _, _, err := autoSignInTjuptTrust(autoSignInTjuptStartup{ImageOriginsJSON: raw}); err == nil {
				t.Fatal("invalid trust accepted")
			}
		})
	}
	if _, _, _, err := autoSignInTjuptTrust(autoSignInTjuptStartup{RootCAPEM: []byte("not a certificate")}); err == nil {
		t.Fatal("invalid trusted test CA accepted")
	}
	f := signinSQLSetup(t)
	env := &signinSyntheticEnvironment{}
	env.set(autoSignInTjuptOriginsEnvironment + `=["https://images.synthetic.test"]`)
	f.a.autoSignInEnvironment = env.read
	first, err := f.a.readAutoSignInSites(context.Background(), f.o)
	if err != nil || !reflect.DeepEqual(first.TjuptImages, []string{signinTjuptImageOrigin + ":443"}) || first.TjuptRoots != nil {
		t.Fatal("startup origin not bound", err)
	}
	env.set(autoSignInTjuptOriginsEnvironment + `=["https://other.synthetic.test"]`)
	second, err := f.a.readAutoSignInSites(context.Background(), f.o)
	if err != nil || first.Revision == second.Revision {
		t.Fatal("origin omitted from HMAC", err)
	}
	f.a.autoSignInTjuptStartup = func() autoSignInTjuptStartup { return autoSignInTjuptStartup{} }
	empty, err := f.a.readAutoSignInSites(context.Background(), f.o)
	if err != nil || len(empty.TjuptImages) != 0 || empty.TjuptRoots != nil {
		t.Fatal("private empty provider read environment", err)
	}
	settings := f.a.Settings()
	settings.Sites = []SiteConfig{signinSitesNative("https://www.tjupt.org")}
	signinSitesPublish(t, f.a, settings)
	deps := signinSitesDependencies(t, f)
	inv, _ := deps.Inventory(context.Background())
	bound, err := deps.Factory(context.Background(), inv.Configured[0])
	if err != nil || bound.Hooks.Tjupt == nil || bound.Hooks.Tjupt.Public == nil || bound.Hooks.Tjupt.Cache != nil {
		t.Fatal("host capability or durable cache ownership missing", err)
	}
	if _, err = bound.Hooks.Tjupt.Public.Image(context.Background(), signinTjuptImageOrigin+"/synthetic.png"); !errors.Is(err, source.ErrSiteTargetDenied) {
		t.Fatal("empty policy granted image origin", err)
	}
}

func TestAutoSignInTjuptHostPerHopTrustRevocation(t *testing.T) {
	for _, endpoint := range []string{"site", "suggest", "image"} {
		for _, mutation := range []string{"origins", "malformed", "ca", "proxy", "cookie", "run"} {
			t.Run(endpoint+"/"+mutation, func(t *testing.T) {
				f := signinSQLSetup(t)
				network := signinTjuptTLS(t)
				startup := network.configure(f.a)
				environment := &signinSyntheticEnvironment{}
				environment.set("https_proxy=" + network.environmentProxy.URL)
				f.a.autoSignInEnvironment = environment.read
				settings := signinTjuptSettings(f.a, network)
				signinSitesPublish(t, f.a, settings)
				deps := signinSitesDependencies(t, f)
				inv, _ := deps.Inventory(context.Background())
				bound, err := deps.Factory(context.Background(), inv.Configured[0])
				if err != nil {
					t.Fatal(err)
				}
				_, replacementCA := signinTjuptCertificate(t)
				var second atomic.Int32
				network.setBefore(func(w http.ResponseWriter, r *http.Request) bool {
					if r.URL.Path == "/next" {
						second.Add(1)
						return true
					}
					switch mutation {
					case "origins":
						v := startup.read()
						v.ImageOriginsJSON = `[]`
						startup.set(v)
					case "malformed":
						v := startup.read()
						v.ImageOriginsJSON = `null`
						startup.set(v)
					case "ca":
						v := startup.read()
						v.RootCAPEM = replacementCA
						startup.set(v)
					case "proxy":
						environment.set()
					case "cookie":
						settings.Sites[0].Cookie = "synthetic-replaced"
						if err := signinSitesPublishConfig(f.a, settings); err != nil {
							t.Error(err)
						}
					case "run":
						if _, err := f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='failed' WHERE id=?`, f.run.ID); err != nil {
							t.Error(err)
						}
					}
					http.Redirect(w, r, "/next", http.StatusFound)
					return true
				})
				switch endpoint {
				case "site":
					_, err = bound.Hooks.HTTP.Do(context.Background(), source.PageRequest{Method: "GET", URL: "https://www.tjupt.org/attendance.php"})
				case "suggest":
					_, err = bound.Hooks.Tjupt.Public.Suggest(context.Background(), "甲片")
				case "image":
					_, err = bound.Hooks.Tjupt.Public.Image(context.Background(), signinTjuptImageOrigin+"/synthetic.png")
				}
				if (!(errors.Is(err, signinjournal.ErrStale) || mutation == "malformed" && pluginErrorCode(err) == "AUTOSIGNIN_TJUPT_TRUST_INVALID")) || network.count() != 1 || second.Load() != 0 {
					t.Fatal("stale authority continued redirect", err, network.count(), second.Load())
				}
			})
		}
	}
}

func TestAutoSignInTjuptHostSystemRootsRejectSyntheticCA(t *testing.T) {
	f := signinSQLSetup(t)
	network := signinTjuptTLS(t)
	startup := network.configure(f.a)
	v := startup.read()
	v.RootCAPEM = nil
	startup.set(v)
	signinSitesPublish(t, f.a, signinTjuptSettings(f.a, network))
	deps := signinSitesDependencies(t, f)
	inv, _ := deps.Inventory(context.Background())
	bound, err := deps.Factory(context.Background(), inv.Configured[0])
	if err != nil {
		t.Fatal(err)
	}
	response, err := bound.Hooks.Tjupt.Public.Image(context.Background(), signinTjuptImageOrigin+"/synthetic.png")
	if response != nil || network.count() != 0 {
		t.Fatal("untrusted synthetic certificate accepted", err)
	}
}

func TestPluginAutoSignInSDKTjuptCONNECTCacheClonesAndColdNoResend(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	network := signinTjuptTLS(t)
	network.configure(a)
	settings := signinTjuptSettings(a, network)
	a.settingsMu.Lock()
	err := a.saveSettings(ctx, settings)
	a.settingsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"enabled": true, "onlyonce": false, "notify": false, "cron": "0 0 * * *", "sign_sites": []any{"all"}, "login_sites": []any{}, "queue_cnt": 1}
	for _, instance := range []string{"AutoSignIn", "AutoSignIn_clone"} {
		autoSignInSDKInstall(t, a, instance, config)
	}
	if network.count() != 0 {
		t.Fatal("initialization contacted a site")
	}
	transfer, err := a.ExportAutoSignInTjuptCache(ctx, "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	seed := []byte(`{"older":"旧答案"}`)
	transfer.Cache = AutoSignInTjuptCachePayload{Mode: "source-bytes", SourceSHA256: AutoSignInTjuptSourceSHA256, Key: "tjupt", Present: true, ReadyPresent: true, Ready: true, Bytes: seed, SHA256: sourceSettingsDigest(seed)}
	raw, err := json.Marshal(transfer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.ImportAutoSignInTjuptCache(ctx, "AutoSignIn", raw); err != nil {
		t.Fatal(err)
	}
	if len(autoSignInSDKState(t, a, "AutoSignIn_clone").Cache) != 0 {
		t.Fatal("cache import leaked into clone")
	}
	var first PluginRun
	for _, instance := range []string{"AutoSignIn", "AutoSignIn_clone"} {
		if _, err = a.queueAutoSignInCommand(ctx, instance, pluginAPIAuthority{Kind: "command"}, "飞书", "owned-user"); err != nil {
			t.Fatal(err)
		}
		final := autoSignInSDKExecute(t, a, instance)
		if instance == "AutoSignIn" {
			first = final
		}
		state := autoSignInSDKState(t, a, instance)
		if state.Stats["tjupt.org"].Success != 1 || len(state.History) == 0 || !state.CacheReady["tjupt"] {
			t.Fatal("actual SDK did not establish source success", state.Stats)
		}
		if instance == "AutoSignIn" {
			var cache map[string]string
			if err = json.Unmarshal(state.Cache["tjupt"], &cache); err != nil || len(cache) != 2 || cache["older"] != "旧答案" {
				t.Fatal("nonempty source cache not durably updated", err)
			}
			for hash, answer := range cache {
				if hash != "older" && answer != "1" {
					t.Fatal("original value-versus-label bug was changed")
				}
			}
		} else if len(state.Cache["tjupt"]) != 0 {
			t.Fatal("original empty-cache population bug was changed")
		}
	}
	if network.count() != 10 || network.posts.Load() != 2 {
		t.Fatal("incorrect actual SDK operation count", network.count(), network.posts.Load())
	}
	network.mu.Lock()
	siteRoutes := append([]string(nil), network.proxyRequests["site"]...)
	publicRoutes := append([]string(nil), network.proxyRequests["environment"]...)
	network.mu.Unlock()
	if len(siteRoutes) != 6 || len(publicRoutes) != 4 {
		t.Fatal("site/public proxy boundary changed", siteRoutes, publicRoutes)
	}
	for _, host := range siteRoutes {
		if host != "www.tjupt.org:443" {
			t.Fatal("public request used explicit site proxy")
		}
	}
	for _, host := range publicRoutes {
		if host == "www.tjupt.org:443" {
			t.Fatal("credentialed site did not use its selected explicit proxy")
		}
	}
	before := autoSignInSDKState(t, a, "AutoSignIn")
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	useBuiltInPluginProcess(t, a)
	network.configure(a)
	if err = a.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	// Inspect the same sealed completed run and cache after a real cold App.
	proof, code, found, err := a.pluginStore.autoSignInDeferredOutcome(ctx, first, source.RunCapability)
	if err != nil || !found || code != "" || !json.Valid(proof) {
		t.Fatal("cold terminal proof unavailable", code, found, err)
	}
	after := autoSignInSDKState(t, a, "AutoSignIn")
	if !bytes.Equal(before.Cache["tjupt"], after.Cache["tjupt"]) || len(autoSignInSDKState(t, a, "AutoSignIn_clone").Cache["tjupt"]) != 0 {
		t.Fatal("cold cache bytes/isolation lost")
	}
	if network.count() != 10 || network.posts.Load() != 2 {
		t.Fatal("cold recovery resent site/public/POST IO")
	}
	// An explicit source command deliberately clears the daily skip. Stored
	// value "1" does not match label "甲片", so the source repeats public lookup.
	if _, err = a.queueAutoSignInCommand(ctx, "AutoSignIn", pluginAPIAuthority{Kind: "command"}, "飞书", "owned-user"); err != nil {
		t.Fatal(err)
	}
	autoSignInSDKExecute(t, a, "AutoSignIn")
	if network.count() != 15 || network.posts.Load() != 3 {
		t.Fatal("source value-versus-label cache bug or explicit command changed", network.count(), network.posts.Load())
	}
	serialized, _ := json.Marshal(after.Runtime)
	if strings.Contains(string(serialized), "synthetic-native") || strings.Contains(string(serialized), "RootCAPEM") {
		t.Fatal("transport capability leaked into runtime")
	}
}

func TestPluginAutoSignInSDKTjuptStopPublicIOKeepsUnknownWithoutResend(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	network := signinTjuptTLS(t)
	network.configure(a)
	signinSitesPublish(t, a, signinTjuptSettings(a, network))
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	network.setBefore(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Host == "images.synthetic.test" {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return true
		}
		return false
	})
	autoSignInSDKInstall(t, a, "AutoSignIn", map[string]any{"enabled": true, "onlyonce": false, "notify": false, "cron": "0 0 * * *", "sign_sites": []any{"all"}, "login_sites": []any{}, "queue_cnt": 1})
	run := signinConcurrentClaim(t, a, "AutoSignIn")
	running, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.executePluginRun(running, run) }()
	defer func() { cancel(); unblock(); signinConcurrentWait(t, done, "Tjupt stop cleanup") }()
	signinConcurrentWait(t, entered, "Tjupt public image request")
	cancel()
	unblock()
	signinConcurrentWait(t, done, "Tjupt stopped invocation")
	final, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || final.State != "reconcile_pending" {
		t.Fatal("in-flight public IO not retained as unknown", final.State, final.ErrorCode, err)
	}
	if network.count() != 4 || network.posts.Load() != 0 {
		t.Fatal("stop allowed answer POST or retry", network.count(), network.posts.Load())
	}
	state := autoSignInSDKState(t, a, "AutoSignIn")
	if state.ActiveRun != run.ID || len(state.History) != 0 || len(state.Cache["tjupt"]) != 0 {
		t.Fatal("stopped run wrote source/cache completion")
	}
	if err = a.pluginStore.recoverPluginRuns(ctx); err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	if network.count() != 4 || network.posts.Load() != 0 {
		t.Fatal("unknown public IO was automatically or explicitly resent")
	}
}
