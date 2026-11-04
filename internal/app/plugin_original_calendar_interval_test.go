package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"light-media/internal/plugins/schedule"
)

// New owned peers use only tcp4. A denied/failed bind is fatal, not permission
// to probe another address family, proxy, upstream, or listening route.
func calendarOwnedPeer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	t.Cleanup(server.Close)
	return server
}

type calendarOwnedArtifactCounts struct{ index, worker atomic.Int64 }

// Standard artifact installation, copied narrowly from installOwnedInputFixture
// to avoid its implicit IPv6 fallback. The ordinary worker is not modified.
func calendarOwnedInstall(t *testing.T) (*App, *http.Cookie, *calendarOwnedArtifactCounts) {
	t.Helper()
	path := os.Getenv("OWNED_INPUT_FIXTURE_BINARY")
	if path == "" {
		t.Fatal("OWNED_INPUT_FIXTURE_BINARY must name the precompiled actual SDK worker")
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	raw, err := exec.CommandContext(ctx, path, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedInput" {
		t.Fatal("wrong owned worker manifest")
	}
	sum := sha256.Sum256(binary)
	digest := hex.EncodeToString(sum[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(binary)), HostContract: pluginResourcesSDKContract}}}}}
	counts := &calendarOwnedArtifactCounts{}
	peer := calendarOwnedPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected owned artifact method", r.Method)
			http.Error(w, "method", 405)
			return
		}
		switch r.URL.Path {
		case "/index":
			counts.index.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			counts.worker.Add(1)
			_, _ = w.Write(binary)
		default:
			t.Error("unexpected owned artifact path", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	index.Releases[0].Artifacts[0].URL = peer.URL + "/worker"
	a := newTestApp(t)
	// Existing isolation disables plugin statistics/center, not environment proxy
	// routing or another business capability. No full App.Start/background sweep.
	disableResetFixtureStatistics(t, a)
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	settings.APIToken = "owned-calendar-synthetic-api-key"
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"plugin_id": "OwnedInput", "repo_url": peer.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	return a, cookie, counts
}

func calendarOwnedPID(t *testing.T, a *App) int {
	t.Helper()
	response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/OwnedInput/events", "")
	var result struct {
		PID int `json:"pid"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.PID <= 0 || result.PID == os.Getpid() {
		t.Fatal("actual worker PID", response.Code, response.Body.String())
	}
	return result.PID
}
func calendarOwnedConfig(t *testing.T, a *App, cookie *http.Cookie, defs ...map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"enabled": true, "services": defs})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(raw), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
}
func calendarOwnedDefinition(id string, trigger map[string]any, gate string) map[string]any {
	return map[string]any{"id": id, "name": "Owned calendar " + id, "handler": "run_service", "trigger": trigger, "kwargs": map[string]any{"misfire_grace_time": nil}, "input": map[string]any{"mode": "list-fail", "gate": gate, "progress": 37, "text": "owned calendar callback"}}
}
func calendarOwnedSpec(at time.Time) map[string]any {
	at = at.UTC()
	return map[string]any{"kind": "calendarinterval", "timezone": "UTC", "calendar": map[string]any{"days": 1, "start_date": at.Format("2006-01-02"), "hour": at.Hour(), "minute": at.Minute(), "second": at.Second()}}
}
func calendarOwnedProgress(t *testing.T, a *App, cookie *http.Cookie, id string) map[string]json.RawMessage {
	t.Helper()
	response := callTest(t, a.Handler(), "GET", "/api/v1/dashboard/schedule/OwnedInput_"+id+"/progress", "", cookie, "")
	var result struct {
		Success bool
		Data    map[string]json.RawMessage
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.Success {
		t.Fatal("actual progress API", response.Code, response.Body.String())
	}
	return result.Data
}
func calendarOwnedList(t *testing.T, a *App, cookie *http.Cookie) map[string]PluginServiceInfo {
	t.Helper()
	response := callTest(t, a.Handler(), "GET", "/api/v1/dashboard/schedule", "", cookie, "")
	var wire []originalServiceWireInfo
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &wire) != nil {
		t.Fatal("actual source-wire schedule API", response.Code, response.Body.String())
	}
	// The delivered source v1 schema deliberately has neither service_id nor
	// can_run. Native identity/button admission belongs to the existing native
	// envelope; cross-check only the common source-visible fields between them.
	native := callTest(t, a.Handler(), "GET", "/api/plugins/services", "", cookie, "")
	var envelope struct{ Data []PluginServiceInfo }
	if native.Code != 200 || json.Unmarshal(native.Body.Bytes(), &envelope) != nil || len(envelope.Data) != len(wire) {
		t.Fatal("actual native service API", native.Code, native.Body.String())
	}
	out := map[string]PluginServiceInfo{}
	for i, row := range envelope.Data {
		source := wire[i]
		if row.InstanceID != "OwnedInput" || row.ID != "OwnedInput_"+row.ServiceID || source.ID != row.ID || source.Status != row.Status || (source.NextRun == nil) != (row.NextRun == nil) || source.NextRun != nil && *source.NextRun != *row.NextRun {
			t.Fatal("source/native schedule field mismatch", source, row)
		}
		if _, exists := out[row.ServiceID]; exists {
			t.Fatal("duplicate actual service ID", row.ServiceID)
		}
		out[row.ServiceID] = row
	}
	return out
}
func calendarOwnedWaitSuccess(t *testing.T, a *App, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		result := a.originalServiceProgressSnapshot("OwnedInput_" + id)
		status, _ := json.Marshal(result["status"])
		if string(status) == `"success"` {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("actual worker callback did not finish", id, result)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func calendarOwnedTickUntil(t *testing.T, a *App, id string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.runOriginalPluginScheduler(ctx) }()
	defer func() { cancel(); <-done; a.wg.Wait() }()
	calendarOwnedWaitSuccess(t, a, id)
}
func calendarOwnedLogin(t *testing.T, a *App) *http.Cookie {
	t.Helper()
	response := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	for _, cookie := range response.Result().Cookies() {
		if response.Code == 200 && cookie.Name == "lm_session" {
			return cookie
		}
	}
	t.Fatal("cold login", response.Code, response.Body.String())
	return nil
}

func TestOriginalCalendarActualWorkerClockAndCold(t *testing.T) {
	var calls atomic.Int64
	peer := calendarOwnedPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/ordinary" {
			t.Error("unexpected callback request", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	a, cookie, artifacts := calendarOwnedInstall(t)
	at := time.Now().UTC().Add(-time.Second).Truncate(time.Second)
	definition := calendarOwnedDefinition("ordinary", calendarOwnedSpec(at), peer.URL+"/ordinary")
	calendarOwnedConfig(t, a, cookie, definition)
	before := calendarOwnedPID(t, a)
	a.pluginOriginalDirectJobs.mu.Lock()
	initial := a.pluginOriginalDirectJobs.jobs[originalServiceKey{"OwnedInput", "ordinary"}]
	due := initial.due
	_, typed := initial.trigger.(*schedule.CalendarTrigger)
	a.pluginOriginalDirectJobs.mu.Unlock()
	if !typed || !due.Equal(at) {
		t.Fatal("ordinary wire did not retain source past First", due, at, typed)
	}
	calendarOwnedTickUntil(t, a, "ordinary")
	if calls.Load() != 1 {
		t.Fatal("warm source coalesced callback count", calls.Load())
	}
	progress := calendarOwnedProgress(t, a, cookie, "ordinary")
	if string(progress["success"]) != "true" || string(progress["error"]) != "null" || progress["clock_error"] != nil {
		t.Fatal("ordinary callback altered", progress)
	}
	row := calendarOwnedList(t, a, cookie)["ordinary"]
	if row.Status != "等待" || !row.CanRun || row.NextRun == nil || *row.NextRun == "" {
		t.Fatal("ordinary public clock", row)
	}
	// No durable Next is persisted: cold plugin init re-declares the original
	// past start. Its one coalesced callback is valid source behavior, not replay.
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	coldCookie := calendarOwnedLogin(t, cold)
	after := calendarOwnedPID(t, cold)
	if after == before {
		t.Fatal("cold did not start a new actual worker")
	}
	cold.pluginOriginalDirectJobs.mu.Lock()
	coldDue := cold.pluginOriginalDirectJobs.jobs[originalServiceKey{"OwnedInput", "ordinary"}].due
	cold.pluginOriginalDirectJobs.mu.Unlock()
	if !coldDue.Equal(at) {
		t.Fatal("cold invented durable due", coldDue, at)
	}
	calendarOwnedTickUntil(t, cold, "ordinary")
	if calls.Load() != 2 || artifacts.index.Load() != 1 || artifacts.worker.Load() != 1 {
		t.Fatal("cold redeclaration/refetch counts", calls.Load(), artifacts.index.Load(), artifacts.worker.Load())
	}
	if got := calendarOwnedProgress(t, cold, coldCookie, "ordinary"); string(got["success"]) != "true" || got["clock_error"] != nil {
		t.Fatal("cold callback", got)
	}
	t.Logf("actual installed Calendar: PID %d -> %d, warm/cold callback 1+1, unchanged artifact GETs 2", before, after)
}

// This port is used ONLY to isolate host submit-before-second-Next ordering.
// It delegates the first Next to the real Calendar trigger, then injects the
// exact typed calculation fault. This is not additional source algorithm proof
// or evidence that the wall clock reached year 9999; pure19 covers the latter
// finite days overflow algorithm independently.
type calendarOwnedSecondFault struct {
	actual originalJobTrigger
	calls  int
}

func (c *calendarOwnedSecondFault) FirstChecked(at time.Time) (time.Time, error) {
	return c.actual.FirstChecked(at)
}
func (c *calendarOwnedSecondFault) NextChecked(at time.Time) (time.Time, error) {
	c.calls++
	if c.calls == 2 {
		return time.Time{}, &schedule.CalendarIntervalSourceError{Class: "OverflowError", Message: "date value out of range"}
	}
	return c.actual.NextChecked(at)
}

func TestOriginalCalendarActualWorkerFaultIsolationAndResave(t *testing.T) {
	var other, blocked atomic.Int64
	reached, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	peer := calendarOwnedPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected callback method", r.Method)
			http.Error(w, "method", 405)
			return
		}
		switch r.URL.Path {
		case "/other":
			other.Add(1)
		case "/blocked":
			if blocked.Add(1) == 1 {
				close(reached)
			}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		default:
			t.Error("unexpected callback path", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	a, cookie, _ := calendarOwnedInstall(t)
	defer releaseOnce.Do(func() { close(release) })
	// Positive, finite date addition overflows on its first advance; no source
	// year/month retry loop, system-clock alteration or enormous catch-up loop.
	overflow := calendarOwnedDefinition("fault", map[string]any{"kind": "calendarinterval", "timezone": "UTC", "calendar": map[string]any{"days": 3652058, "start_date": "0002-01-01"}}, peer.URL+"/blocked")
	ordinary := calendarOwnedDefinition("other", map[string]any{"kind": "date", "timezone": "UTC", "run_at": time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)}, peer.URL+"/other")
	calendarOwnedConfig(t, a, cookie, overflow, ordinary)
	a.dispatchOriginalPluginJobs(context.Background(), time.Now())
	a.wg.Wait()
	if blocked.Load() != 0 || other.Load() != 1 {
		t.Fatal("collection fault submitted callback or stopped another clock", blocked.Load(), other.Load())
	}
	const fault = "OverflowError: date value out of range"
	row := calendarOwnedList(t, a, cookie)["fault"]
	if row.ID != "OwnedInput_fault" || row.Status != "停止" || !row.CanRun || row.NextRun == nil || *row.NextRun != "时钟已停止："+fault {
		t.Fatal("fault not visible", row)
	}
	firstProgress := calendarOwnedProgress(t, a, cookie, "fault")
	if string(firstProgress["clock_error"]) != `"`+fault+`"` || string(firstProgress["success"]) != "null" || string(firstProgress["error"]) != "null" {
		t.Fatal("clock fault fabricated callback failure", firstProgress)
	}
	// Distinct error log is persisted with logical job attribution.
	flushTestLogs(t, a)
	logs := callTest(t, a.Handler(), "GET", "/api/logs?job_id=OwnedInput_fault&limit=100", "", cookie, "")
	var page struct{ Data logPage }
	if logs.Code != 200 || json.Unmarshal(logs.Body.Bytes(), &page) != nil {
		t.Fatal("clock log API", logs.Code, logs.Body.String())
	}
	found := 0
	for _, entry := range page.Data.Entries {
		if entry["event"] == "calendar_trigger_error" && entry["job_id"] == "OwnedInput_fault" && entry["level"] == "ERROR" && entry["message"] == fault {
			found++
		}
	}
	if found != 1 {
		t.Fatal("clock error log missing or repeated", found)
	}
	// Ordinary save re-declares the same ID. It must clear clockError without a
	// new repair endpoint, even though the old progress object may still exist.
	repaired := calendarOwnedDefinition("fault", calendarOwnedSpec(time.Now().Add(-time.Second)), peer.URL+"/other")
	calendarOwnedConfig(t, a, cookie, repaired)
	if got := calendarOwnedProgress(t, a, cookie, "fault"); got["clock_error"] != nil {
		t.Fatal("save retained old fault", got)
	}
	if got := calendarOwnedList(t, a, cookie)["fault"]; got.Status != "等待" {
		t.Fatal("save did not restore clock", got)
	}
	a.dispatchOriginalPluginJobs(context.Background(), time.Now())
	a.wg.Wait()
	if other.Load() != 2 {
		t.Fatal("re-saved actual worker did not execute", other.Load())
	}
	// Second Next occurs after batch admission. Hold the actual callback in an
	// owned peer so running status and the independent clock error coexist.
	pending := calendarOwnedDefinition("fault", calendarOwnedSpec(time.Now().Add(-time.Second)), peer.URL+"/blocked")
	calendarOwnedConfig(t, a, cookie, pending)
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	job := state.jobs[originalServiceKey{"OwnedInput", "fault"}]
	injected := &calendarOwnedSecondFault{actual: job.trigger}
	job.trigger = injected
	state.mu.Unlock()
	a.dispatchOriginalPluginJobs(context.Background(), time.Now())
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("submitted callback was discarded on second Next error")
	}
	running := calendarOwnedList(t, a, cookie)["fault"]
	if running.Status != "正在运行" || running.CanRun || running.NextRun == nil || *running.NextRun != "时钟已停止："+fault {
		t.Fatal("draining callback UI admission altered", running)
	}
	active := calendarOwnedProgress(t, a, cookie, "fault")
	if string(active["status"]) != `"running"` || string(active["clock_error"]) != `"`+fault+`"` {
		t.Fatal("running progress lost clock error", active)
	}
	releaseOnce.Do(func() { close(release) })
	a.wg.Wait()
	finished := calendarOwnedProgress(t, a, cookie, "fault")
	if string(finished["status"]) != `"success"` || string(finished["success"]) != "true" || string(finished["error"]) != "null" || string(finished["clock_error"]) != `"`+fault+`"` {
		t.Fatal("successful callback erased/fabricated clock failure", finished)
	}
	stopped := calendarOwnedList(t, a, cookie)["fault"]
	if stopped.Status != "停止" || !stopped.CanRun {
		t.Fatal("completed fault row disappeared", stopped)
	}
	a.dispatchOriginalPluginJobs(context.Background(), time.Now())
	a.wg.Wait()
	if blocked.Load() != 1 || injected.calls != 2 {
		t.Fatal("stopped clock retried", blocked.Load(), injected.calls)
	}
	// A fault must not act as a second paused timer. The source's real multiple-
	// paused error remains unchanged for two ordinary paused clocks.
	paused := calendarOwnedDefinition("paused", map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 3600000}, "")
	paused["kwargs"] = map[string]any{"next_run_time": nil}
	// Register through the real worker, then re-establish the same finite fault.
	calendarOwnedConfig(t, a, cookie, overflow, paused)
	a.dispatchOriginalPluginJobs(context.Background(), time.Now())
	a.wg.Wait()
	if rows := calendarOwnedList(t, a, cookie); len(rows) != 2 || rows["fault"].Status != "停止" {
		t.Fatal("fault counted as legacy paused timer", rows)
	}
	paused2 := calendarOwnedDefinition("paused2", map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 3600000}, "")
	paused2["kwargs"] = map[string]any{"next_run_time": nil}
	calendarOwnedConfig(t, a, cookie, paused, paused2)
	response := callTest(t, a.Handler(), "GET", "/api/v1/dashboard/schedule", "", cookie, "")
	if response.Code != 500 || response.Body.String() != "Internal Server Error" {
		t.Fatal("legacy multiple paused source wire error changed", response.Code, response.Body.String())
	}
	nativePaused := callTest(t, a.Handler(), "GET", "/api/plugins/services", "", cookie, "")
	if nativePaused.Code != 500 || !strings.Contains(nativePaused.Body.String(), "PLUGIN_SERVICE_LIST_FAILED") {
		t.Fatal("legacy native paused error envelope changed", nativePaused.Code, nativePaused.Body.String())
	}
	t.Log("actual first-collection overflow isolates one clock; actual worker callback survives controlled second-Next fault; logs/API/progress preserve separate clock error; ordinary re-save and legacy paused rules verified")
}
