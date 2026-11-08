package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/plugins/schedule"
)

// This is App + real SQLite + an independently compiled standard SDK worker,
// with an owned HTTP callback peer. It does not claim cmd/server/main execution.
func TestOriginalServiceActualSDKSystemLocal(t *testing.T) {
	child := os.Getenv("PLUGIN_APP_LOCAL_CHILD")
	if child == "" {
		raw, err := os.ReadFile("../../testdata/plugin-local-timezone-20261105/New_York.tzif")
		if err != nil {
			t.Fatal(err)
		}
		zone := filepath.Join(t.TempDir(), "owned-anonymous-local-zone")
		if err := os.WriteFile(zone, raw, 0600); err != nil {
			t.Fatal(err)
		}
		for _, branch := range []string{"owned-TZ-file", "empty-TZ-canonical-files"} {
			t.Run(branch, func(t *testing.T) {
				value := zone
				if branch == "empty-TZ-canonical-files" {
					value = ""
				}
				command := exec.Command(os.Args[0], "-test.run=^TestOriginalServiceActualSDKSystemLocal$", "-test.v")
				for _, value := range os.Environ() {
					if !strings.HasPrefix(value, "TZ=") {
						command.Env = append(command.Env, value)
					}
				}
				command.Env = append(command.Env, "TZ="+value, "PLUGIN_APP_LOCAL_CHILD="+branch)
				output, err := command.CombinedOutput()
				t.Log(string(output))
				if err != nil {
					t.Fatal("independent App local-zone process", err)
				}
			})
		}
		return
	}
	location := time.Local
	if child == "empty-TZ-canonical-files" {
		// Independent expected location is decoded from read-only canonical
		// metadata, not from the implementation's Location/New resolver.
		location = time.UTC
		for _, path := range []string{"/etc/localtime", "/usr/local/etc/localtime"} {
			raw, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			location, err = time.LoadLocationFromTZData("expected-system-local", raw)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		_, standardOffset := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).In(time.Local).Zone()
		if standardOffset != 0 {
			t.Fatal("independent empty-TZ Go Local must be UTC")
		}
	} else {
		_, offset := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).In(location).Zone()
		if offset != -18000 {
			t.Fatal("real owned TZif not active", offset)
		}
	}
	a, cookie := installOwnedInputFixture(t)
	var mu sync.Mutex
	calls := map[string]int{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer peer.Close()
	configure := func(app *App, id string, defs []map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"enabled": true, "services": defs})
		r := callTest(t, app.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		app.wg.Wait()
	}
	definition := func(id string, trigger map[string]any, path string) map[string]any {
		return map[string]any{"id": id, "name": id, "handler": "run_service", "trigger": trigger, "input": map[string]any{"mode": "list-fail", "progress": 37, "gate": peer.URL + path}}
	}
	pid := func(app *App, id string) int {
		t.Helper()
		r := ownedInputAPI(t, app, "GET", "/api/v1/plugin/"+id+"/events", "")
		var out struct{ PID int }
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.PID == 0 || out.PID == os.Getpid() {
			t.Fatal("independent SDK worker unavailable", r.Code, r.Body.String())
		}
		return out.PID
	}
	localDaily := definition("daily", map[string]any{"kind": "cron", "local_timezone": true, "cron": "30 1 * * *"}, "/daily")
	ianaDaily := definition("iana", map[string]any{"kind": "cron", "timezone": "Asia/Shanghai", "cron": "30 1 * * *"}, "/iana")
	conflict := definition("conflict", map[string]any{"kind": "cron", "local_timezone": true, "timezone": "UTC", "cron": "* * * * *"}, "/invalid")
	oldLocal := definition("old-local", map[string]any{"kind": "cron", "timezone": "Local", "cron": "* * * * *"}, "/invalid")
	// Use the source option conversion too: a naive next_run_time must resolve
	// the declaration's local zone, rather than silently selecting UTC.
	naive := definition("naive", map[string]any{"kind": "cron", "local_timezone": true, "cron": "30 1 * * *"}, "/naive")
	naive["kwargs"] = map[string]any{"next_run_time": "2999-01-02 03:04:05"}
	configure(a, "OwnedInput", []map[string]any{localDaily, ianaDaily, conflict, oldLocal, naive})
	inspect := func(app *App, id string) {
		t.Helper()
		state := &app.pluginOriginalDirectJobs
		state.mu.Lock()
		defer state.mu.Unlock()
		local := state.jobs[originalServiceKey{id, "daily"}]
		iana := state.jobs[originalServiceKey{id, "iana"}]
		if local == nil || local.trigger == nil || !local.clockRegistered || !local.definition.Trigger.LocalTimezone || local.definition.Trigger.Timezone != "" {
			t.Fatal("worker local declaration not registered")
		}
		anchor := time.Now()
		localNow := anchor.In(location)
		want := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 1, 30, 0, 0, location)
		if want.Before(anchor) {
			want = want.AddDate(0, 0, 1)
		}
		if !local.due.Equal(want) {
			t.Fatal("actual local due differs from independent wall calendar", local.due, want)
		}
		named, err := schedule.New(schedule.Spec{Kind: "cron", Timezone: "Asia/Shanghai", Cron: "30 1 * * *"}, anchor)
		if iana == nil || err != nil || !iana.due.Equal(named.First(anchor)) || iana.definition.Trigger.LocalTimezone {
			t.Fatal("existing IANA service changed")
		}
		for _, name := range []string{"conflict", "old-local"} {
			job := state.jobs[originalServiceKey{id, name}]
			if job == nil || job.clockRegistered || !job.due.IsZero() {
				t.Fatal("invalid local clock registered", name)
			}
		}
		job := state.jobs[originalServiceKey{id, "naive"}]
		wantNaive := time.Date(2999, 1, 2, 3, 4, 5, 0, location)
		if job == nil || !job.clockRegistered || !job.due.Equal(wantNaive) {
			t.Fatal("naive source option lost local zone")
		}
	}
	inspect(a, "OwnedInput")
	r := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedInput/clone", `{"suffix":"local","name":"Owned local clone"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	sourcePID, clonePID := pid(a, "OwnedInput"), pid(a, "OwnedInputlocal")
	if sourcePID == clonePID {
		t.Fatal("clone worker shared source process")
	}
	configure(a, "OwnedInputlocal", []map[string]any{localDaily, ianaDaily, conflict, oldLocal, naive})
	inspect(a, "OwnedInputlocal")
	clockDefinition := func(at time.Time, path string) map[string]any {
		local := at.In(location)
		fields := map[string]string{"year": strconv.Itoa(local.Year()), "month": strconv.Itoa(int(local.Month())), "day": strconv.Itoa(local.Day()), "hour": strconv.Itoa(local.Hour()), "minute": strconv.Itoa(local.Minute()), "second": strconv.Itoa(local.Second())}
		return definition("clock", map[string]any{"kind": "cron", "local_timezone": true, "fields": fields}, path)
	}
	fire := time.Now().Add(4 * time.Second).Truncate(time.Second)
	sourceDefs := []map[string]any{localDaily, ianaDaily, conflict, oldLocal, naive, clockDefinition(fire, "/source-clock")}
	cloneDefs := []map[string]any{clockDefinition(fire, "/clone-clock")}
	configure(a, "OwnedInput", sourceDefs)
	configure(a, "OwnedInputlocal", cloneDefs)
	runClock := func(app *App, want int) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); app.runOriginalPluginScheduler(ctx) }()
		defer func() { cancel(); <-done; app.wg.Wait() }()
		deadline := time.Now().Add(10 * time.Second)
		for {
			mu.Lock()
			s, c := calls["/source-clock"], calls["/clone-clock"]
			mu.Unlock()
			if s == want && c == want {
				break
			}
			if s > want || c > want || time.Now().After(deadline) {
				t.Fatal("actual local clock side-effect counts", s, c, "want", want)
			}
			time.Sleep(10 * time.Millisecond)
		}
		app.wg.Wait()
		for _, id := range []string{"OwnedInput_clock", "OwnedInputlocal_clock"} {
			p := app.originalServiceProgressSnapshot(id)
			status, _ := json.Marshal(p["status"])
			if string(status) != `"success"` {
				t.Fatal("clock did not consume actual worker completion", id, p)
			}
		}
	}
	runClock(a, 1)
	sourcePID, clonePID = pid(a, "OwnedInput"), pid(a, "OwnedInputlocal")
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err := cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	cold.wg.Wait()
	if pid(cold, "OwnedInput") == sourcePID || pid(cold, "OwnedInputlocal") == clonePID {
		t.Fatal("cold recovered old process")
	}
	inspect(cold, "OwnedInput")
	// The clone keeps its independent clock-only config, and past finite cron
	// occurrences do not replay when the existing registry is rebuilt cold.
	cold.pluginOriginalDirectJobs.mu.Lock()
	cloneDaily := cold.pluginOriginalDirectJobs.jobs[originalServiceKey{"OwnedInputlocal", "daily"}]
	cold.pluginOriginalDirectJobs.mu.Unlock()
	if cloneDaily != nil {
		t.Fatal("clone config merged with source")
	}
	cold.dispatchOriginalPluginJobs(context.Background(), time.Now())
	cold.wg.Wait()
	mu.Lock()
	s, c, invalid := calls["/source-clock"], calls["/clone-clock"], calls["/invalid"]
	mu.Unlock()
	if s != 1 || c != 1 || invalid != 0 {
		t.Fatal("cold replayed clock or invoked rejected declaration", s, c, invalid)
	}
	fire = time.Now().Add(3 * time.Second).Truncate(time.Second)
	configure(cold, "OwnedInput", []map[string]any{clockDefinition(fire, "/source-clock")})
	configure(cold, "OwnedInputlocal", []map[string]any{clockDefinition(fire, "/clone-clock")})
	runClock(cold, 2)
	t.Logf("App/independent SDK local calendar sourcePID=%d clonePID=%d coldSourcePID=%d coldClonePID=%d callbacks=2+2; rejected clocks=0", sourcePID, clonePID, pid(cold, "OwnedInput"), pid(cold, "OwnedInputlocal"))
}
