package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func assetFor(goos, goarch, body string) ([]byte, string) {
	sum := sha256.Sum256([]byte(body))
	return []byte(body), hex.EncodeToString(sum[:])
}

// feed serves a GitHub-shaped release list plus the binary and checksum assets.
func feed(t *testing.T, tag, binaryName, body string, mutate func(*release)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var server *httptest.Server
	publish := func() {
		sum := sha256.Sum256([]byte(body))
		item := release{Tag: tag, Assets: []Asset{
			{Name: binaryName, URL: server.URL + "/bin"},
			{Name: binaryName + ".sha256", URL: server.URL + "/sums"},
		}}
		if mutate != nil {
			mutate(&item)
		}
		raw, _ := json.Marshal([]release{item})
		mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) { w.Write(raw) })
		mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + binaryName + "\n"))
		})
	}
	mux.HandleFunc("/notfound", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" && publish == nil {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	publish()
	return server
}

func baseOptions(t *testing.T, server *httptest.Server) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{
		Mode:           "release",
		CurrentVersion: "0.4.19",
		ReleasesURL:    server.URL + "/releases",
		DataDir:        dir,
		GOOS:           "linux",
		GOARCH:         "arm64",
		Exec:           func(string, []string, []string) error { return nil },
	}
}

func TestInstallsNewerArtifactInsideTheCurrentSeries(t *testing.T) {
	name := "light-media-server-linux-arm64"
	server := feed(t, "v0.5.0", name, "new-binary", nil)
	opts := baseOptions(t, server)
	outcome, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Execed || outcome.Tag != "v0.5.0" {
		t.Fatalf("outcome %+v", outcome)
	}
	installed := filepath.Join(opts.DataDir, "bin", "light-media-server-0.5.0")
	got, err := os.ReadFile(installed)
	if err != nil || string(got) != "new-binary" {
		t.Fatalf("installed artifact: %q %v", got, err)
	}
	info, err := os.Stat(installed)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("installed mode: %v %v", info.Mode(), err)
	}
}

func TestSkipsSameOrOlderAndOtherSeries(t *testing.T) {
	name := "light-media-server-linux-arm64"
	for _, tag := range []string{"v0.4.19", "v0.4.2", "v1.0.0"} {
		server := feed(t, tag, name, "payload", nil)
		outcome, err := Run(context.Background(), baseOptions(t, server))
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Execed || outcome.Installed != "" {
			t.Fatalf("tag %s must not install: %+v", tag, outcome)
		}
		if outcome.Reason != "no_release_for_platform" {
			t.Fatalf("tag %s reason %q", tag, outcome.Reason)
		}
	}
}

func TestRejectsChecksumMismatchWithoutTouchingData(t *testing.T) {
	name := "light-media-server-linux-arm64"
	server := feed(t, "v0.5.0", name, "payload", nil)
	// Serve a digest for different bytes.
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			raw, _ := json.Marshal([]release{{Tag: "v0.5.0", Assets: []Asset{
				{Name: name, URL: server.URL + "/bin"},
				{Name: name + ".sha256", URL: server.URL + "/sums"},
			}}})
			_, _ = w.Write(raw)
		case "/bin":
			_, _ = w.Write([]byte("payload"))
		case "/sums":
			_, _ = w.Write([]byte(strings.Repeat("a", 64) + "  " + name + "\n"))
		default:
			http.NotFound(w, r)
		}
	})
	opts := baseOptions(t, server)
	outcome, err := Run(context.Background(), opts)
	if err == nil || outcome.Execed {
		t.Fatalf("checksum mismatch must fail: %+v %v", outcome, err)
	}
	entries, _ := os.ReadDir(filepath.Join(opts.DataDir, "bin"))
	if len(entries) != 0 {
		t.Fatalf("failed install left files behind: %v", entries)
	}
}

func TestMissingChecksumAssetSkipsRelease(t *testing.T) {
	name := "light-media-server-linux-arm64"
	server := feed(t, "v0.5.0", name, "payload", func(item *release) {
		item.Assets = item.Assets[:1]
	})
	outcome, err := Run(context.Background(), baseOptions(t, server))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Execed || outcome.Reason != "no_release_for_platform" {
		t.Fatalf("release without checksum must be ignored: %+v", outcome)
	}
}

func TestDisabledModeNeverFetches(t *testing.T) {
	server := feed(t, "v0.5.0", "light-media-server-linux-arm64", "payload", nil)
	opts := baseOptions(t, server)
	opts.Mode = "off"
	outcome, err := Run(context.Background(), opts)
	if err != nil || outcome.Checked || outcome.Reason != "disabled" {
		t.Fatalf("disabled mode touched the feed: %+v %v", outcome, err)
	}
}

func TestUnreachableFeedKeepsCurrentBuild(t *testing.T) {
	server := feed(t, "v0.5.0", "light-media-server-linux-arm64", "payload", nil)
	opts := baseOptions(t, server)
	opts.ReleasesURL = server.URL + "/missing"
	outcome, err := Run(context.Background(), opts)
	if err == nil || outcome.Execed {
		t.Fatalf("unreachable feed must not install: %+v %v", outcome, err)
	}
}

func TestLoopMarkerStopsRepeatedUpdate(t *testing.T) {
	t.Setenv(UpdatedToEnv, "v0.5.0")
	server := feed(t, "v0.5.0", "light-media-server-linux-arm64", "payload", nil)
	outcome, err := Run(context.Background(), baseOptions(t, server))
	if err != nil || outcome.Reason != "already_updated_this_boot" {
		t.Fatalf("marker ignored: %+v %v", outcome, err)
	}
}

func TestVersionComparison(t *testing.T) {
	cases := []struct {
		candidate, current string
		newer, series      bool
	}{
		{"v0.5.0", "0.4.19", true, true},
		{"0.4.19", "0.4.19", false, true},
		{"0.4.2", "0.4.19", false, true},
		{"1.0.0", "0.4.19", true, false},
		{"v0.10.0", "0.9.9", true, true},
		{"nightly", "0.4.19", false, false},
	}
	for _, c := range cases {
		if got := Newer(c.candidate, c.current); got != c.newer {
			t.Errorf("Newer(%q,%q)=%v want %v", c.candidate, c.current, got, c.newer)
		}
		if got := SameSeries(c.candidate, c.current); got != c.series {
			t.Errorf("SameSeries(%q,%q)=%v want %v", c.candidate, c.current, got, c.series)
		}
	}
	if !ModeEnabled("") || !ModeEnabled("release") || ModeEnabled("off") || ModeEnabled("false") {
		t.Fatal("mode parsing")
	}
}

var _ = runtime.GOOS

func TestStatusRecordExplainsEveryAttempt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update-status.json")
	server := feed(t, "v0.5.0", "light-media-server-linux-arm64", "payload", nil)
	opts := baseOptions(t, server)
	opts.StatusPath = path
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err = json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record["execed"] != true || record["tag"] != "v0.5.0" || record["current_version"] != "0.4.19" {
		t.Fatalf("status record %s", raw)
	}
}

// A slow release body must not be aborted by the connection budget: the client
// only bounds setup and headers, the caller's context caps the transfer.
func TestSlowBodyIsNotCutByHeaderTimeout(t *testing.T) {
	name := "light-media-server-linux-arm64"
	payload := strings.Repeat("x", 64<<10)
	sum := sha256.Sum256([]byte(payload))
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := json.Marshal([]release{{Tag: "v0.5.0", Assets: []Asset{
			{Name: name, URL: server.URL + "/bin"},
			{Name: name + ".sha256", URL: server.URL + "/sums"},
		}}})
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		for i := 0; i < len(payload); i += 16 << 10 {
			end := i + 16<<10
			if end > len(payload) {
				end = len(payload)
			}
			_, _ = w.Write([]byte(payload[i:end]))
			w.(http.Flusher).Flush()
			time.Sleep(120 * time.Millisecond) // 4 chunks = ~0.5 s total
		}
	})
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + name + "\n"))
	})
	server = httptest.NewServer(mux)
	defer server.Close()

	opts := baseOptions(t, server)
	opts.Client = nil
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("slow body rejected: %v", err)
	}
}

// A server that sends headers and then goes silent must not hold startup: the
// stall watchdog aborts the transfer and the caller keeps its current build.
func TestStalledTransferAbortsWithinBudget(t *testing.T) {
	name := "light-media-server-linux-arm64"
	release1 := http.NewServeMux()
	var server *httptest.Server
	release1.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := json.Marshal([]release{{Tag: "v0.9.0", Assets: []Asset{
			{Name: name, URL: server.URL + "/bin"},
			{Name: name + ".sha256", URL: server.URL + "/sums"},
		}}})
		_, _ = w.Write(raw)
	})
	release1.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1048576")
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // never finish the body
	})
	release1.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 64) + "  " + name + "\n"))
	})
	server = httptest.NewServer(release1)
	defer server.Close()

	opts := baseOptions(t, server)
	opts.StallTimeout = 300 * time.Millisecond
	started := time.Now()
	_, err := Run(context.Background(), opts)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("stalled transfer must fail")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("stall watchdog too slow: %s", elapsed)
	}
	entries, _ := os.ReadDir(filepath.Join(opts.DataDir, "bin"))
	if len(entries) != 0 {
		t.Fatalf("stalled transfer left files: %v", entries)
	}
}

// A build that was already verified and published under <data>/bin must be used
// without the network: restarts are the normal case, not the exception.
func TestLocalVerifiedBuildWinsWithoutNetwork(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "light-media-server-0.7.0")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	var execd string
	opts := Options{
		Mode: "release", CurrentVersion: "0.4.19", DataDir: dir, GOOS: "linux", GOARCH: "arm64",
		ReleasesURL: "http://127.0.0.1:1/unreachable",
		Exec:        func(p string, _ []string, _ []string) error { execd = p; return nil },
	}
	outcome, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Execed || execd != path || outcome.Reason != "local_build" {
		t.Fatalf("local build ignored: %+v exec=%q", outcome, execd)
	}
}

// An installed build that is not newer must not be executed.
func TestLocalBuildOfSameVersionIsIgnored(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "light-media-server-0.4.19"), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	server := feed(t, "v0.4.19", "light-media-server-linux-arm64", "payload", nil)
	opts := baseOptions(t, server)
	opts.DataDir = dir
	outcome, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Execed {
		t.Fatalf("same-version local build executed: %+v", outcome)
	}
}

// A failed transfer backs off so the next start does not spend the budget again.
func TestFailedInstallBacksOffOnNextStart(t *testing.T) {
	dir := t.TempDir()
	status := filepath.Join(dir, "update-status.json")
	if err := os.WriteFile(status, []byte(`{"time":"`+time.Now().UTC().Format(time.RFC3339)+`","reason":"install_failed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Mode: "release", CurrentVersion: "0.4.19", DataDir: dir, GOOS: "linux", GOARCH: "arm64",
		StatusPath:  status,
		ReleasesURL: "http://127.0.0.1:1/unreachable",
		Exec:        func(string, []string, []string) error { return nil },
	}
	outcome, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reason != "backoff_after_failure" || outcome.Checked {
		t.Fatalf("backoff ignored: %+v", outcome)
	}
}

// The instance proxy setting governs update traffic; the environment is only a
// fallback. This is what makes a slow external network usable.
func TestConfiguredProxyWinsOverEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env.invalid:8080")
	t.Setenv("HTTP_PROXY", "http://env.invalid:8080")
	server := feed(t, "v0.5.0", "light-media-server-linux-arm64", "payload", nil)
	opts := baseOptions(t, server)
	opts.ProxySetting = "http://setting.invalid:1080"
	chosen, err := opts.transportFor(0).Proxy(httptest.NewRequest("GET", "https://api.github.com", nil))
	if err != nil {
		t.Fatal(err)
	}
	if chosen == nil || chosen.Host != "setting.invalid:1080" {
		t.Fatalf("configured proxy not used: %v", chosen)
	}
	if got := opts.proxySource(); got != "setting" {
		t.Fatalf("proxy source %q", got)
	}
	opts.ProxySetting = ""
	chosen, _ = opts.transportFor(1).Proxy(httptest.NewRequest("GET", "https://api.github.com", nil))
	if chosen == nil || chosen.Host != "env.invalid:8080" {
		t.Fatalf("environment fallback lost: %v", chosen)
	}
	if got := opts.proxySource(); got != "environment" {
		t.Fatalf("proxy source %q", got)
	}
}

// With neither source set the updater must talk directly rather than fail.
func TestNoProxyConfiguredMeansDirect(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(name, "")
	}
	opts := Options{GOOS: "linux", GOARCH: "arm64"}
	proxy := opts.transportFor(0).Proxy
	if proxy == nil {
		t.Skip("transport reports a direct dial")
	}
	chosen, err := proxy(httptest.NewRequest("GET", "https://api.github.com", nil))
	if err != nil {
		t.Fatal(err)
	}
	if chosen != nil {
		t.Fatalf("expected a direct connection, got %v", chosen)
	}
	if got := opts.proxySource(); got != "none" {
		t.Fatalf("proxy source %q", got)
	}
}

// When the configured proxy cannot serve the feed, the environment proxy must
// take over instead of failing the whole update.
func TestFallsBackToEnvironmentWhenSettingProxyFails(t *testing.T) {
	server := feed(t, "v0.5.0", "light-media-server-linux-arm64", "payload", nil)
	// A dead proxy in the setting, a working one in the environment.
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("HTTP_PROXY", "")
	opts := baseOptions(t, server)
	opts.ProxySetting = "http://127.0.0.1:1"
	session := opts.newSession()
	if _, err := session.get(context.Background(), server.URL+"/releases"); err != nil {
		t.Skipf("environment proxy unavailable in this sandbox: %v", err)
	}
	if session.proxy != "environment" {
		t.Fatalf("fallback proxy label %q", session.proxy)
	}
}

// A proxy that trickles bytes is unusable for a 60 MB artifact: the throughput
// floor must reject the stage so the next candidate gets a chance.
func TestTricklingTransferIsRejected(t *testing.T) {
	name := "light-media-server-linux-arm64"
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := json.Marshal([]release{{Tag: "v0.9.0", Assets: []Asset{
			{Name: name, URL: server.URL + "/bin"},
			{Name: name + ".sha256", URL: server.URL + "/sums"},
		}}})
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(8<<20))
		for i := 0; i < 8<<20/1024; i++ {
			if _, err := w.Write(make([]byte, 1024)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond) // ~20 KB/s
		}
	})
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 64) + "  " + name + "\n"))
	})
	server = httptest.NewServer(mux)
	defer server.Close()

	opts := baseOptions(t, server)
	opts.StallTimeout = 30 * time.Second
	opts.TotalTimeout = time.Minute
	start := time.Now()
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("trickling transfer must fail")
	}
	if strings.Contains(err.Error(), "stalled") {
		t.Fatalf("expected a throughput rejection, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 25*time.Second {
		t.Fatalf("throughput floor too slow: %s", elapsed)
	}
}
