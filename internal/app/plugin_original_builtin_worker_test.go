package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins/schedule"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Uses the separately frozen Calendar02 standard installer/explicit-tcp4 peers.
// Those helper bodies are compiled unchanged; its old roots are not replayed.
func builtinOwnedManual(t *testing.T, a *App, cookie *http.Cookie, id string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"instance_id": "OwnedInput", "service_id": id})
	r := callTest(t, a.Handler(), "POST", "/api/plugins/services/run", string(raw), cookie, "")
	if r.Code != 200 {
		t.Fatal("manual route", id, r.Code, r.Body.String())
	}
	a.wg.Wait()
	if p := calendarOwnedProgress(t, a, cookie, id); string(p["success"]) != "true" {
		t.Fatal("manual did not execute actual worker", id, p)
	}
}
func builtinOwnedDate(at time.Time) map[string]any {
	return map[string]any{"kind": "date", "timezone": "UTC", "run_at": at.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)}
}

func TestOriginalBuiltinActualWorkerCompositionPresenceAndCold(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	peer := calendarOwnedPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected owned callback method", r.Method)
			http.Error(w, "method", 405)
			return
		}
		switch r.URL.Path {
		case "/nested", "/null", "/missing", "/empty", "/zero", "/invalid":
		default:
			t.Error("unexpected owned callback path", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(204)
	}))
	count := func(path string) int { mu.Lock(); defer mu.Unlock(); return calls[path] }
	a, cookie, artifacts := calendarOwnedInstall(t)
	at := time.Now().UTC().Add(-time.Second).Truncate(time.Second)
	// Ordered OR(AND(Calendar,Calendar),Date). Both original normal combinators
	// are exercised through the actual worker service declaration, not a mock job.
	cal := calendarOwnedSpec(at)
	nested := calendarOwnedDefinition("nested", map[string]any{"kind": "or", "children": []any{map[string]any{"kind": "and", "children": []any{cal, cal}}, builtinOwnedDate(at.Add(48 * time.Hour))}}, peer.URL+"/nested")
	null := calendarOwnedDefinition("null", nil, peer.URL+"/null")
	missing := calendarOwnedDefinition("missing", nil, peer.URL+"/missing")
	delete(missing, "trigger")
	empty := calendarOwnedDefinition("empty", map[string]any{}, peer.URL+"/empty")
	zero := calendarOwnedDefinition("zero", builtinOwnedDate(time.Time{}), peer.URL+"/zero")
	// Original constructed objects ignore constructor extras; only null consumes them.
	nested["kwargs"].(map[string]any)["owned_extra"] = "ignored-for-constructed"
	future := calendarOwnedDefinition("future", nil, peer.URL+"/unexpected-future")
	future["kwargs"] = map[string]any{"run_date": "2037-05-06 07:08:09", "timezone": "UTC"}
	invalid := calendarOwnedDefinition("invalid", nil, peer.URL+"/invalid")
	invalid["kwargs"] = map[string]any{"run_date": "2037-05-06 07:08:09", "owned_extra": "owned-only"}
	beforeSave := time.Now()
	calendarOwnedConfig(t, a, cookie, nested, null, missing, empty, zero, future, invalid)
	afterSave := time.Now()
	pid := calendarOwnedPID(t, a)
	checkState := func(app *App, start, end time.Time) {
		t.Helper()
		s := &app.pluginOriginalDirectJobs
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, id := range []string{"nested", "null", "missing", "empty", "zero", "future", "invalid"} {
			j := s.jobs[originalServiceKey{"OwnedInput", id}]
			if j == nil || !j.boundedClock || !j.definition.builtinDecoded {
				t.Fatal("raw declaration not retained", id)
			}
			switch id {
			case "missing":
				if !j.definition.triggerMissing || originalJobHasDue(j) || j.clockRegistered || j.clockError != "" {
					t.Fatal("missing trigger became None/clock", j.definition)
				}
			case "future":
				if !originalJobHasDue(j) || !j.clockRegistered || j.due.Format(time.RFC3339) != "2037-05-06T07:08:09Z" {
					t.Fatal("null ctor kwargs lost", j.due)
				}
			case "invalid":
				if originalJobHasDue(j) || j.clockRegistered || j.clockError != "" || j.definition.builtinDescriptor != nil {
					t.Fatal("unknown Date argument registered clock", j.clockError)
				}
			case "empty":
				if j.definition.triggerMissing || j.definition.builtinDescriptor == nil || originalJobHasDue(j) || j.clockRegistered {
					t.Fatal("empty trigger became None")
				}
			case "null":
				if j.definition.triggerMissing || j.definition.builtinDescriptor != nil || !originalJobHasDue(j) || j.due.Before(start.Add(-time.Microsecond)) || j.due.After(end) {
					t.Fatal("null default constructor", j.due, start, end)
				}
			case "zero":
				if !originalJobHasDue(j) || !j.due.IsZero() {
					t.Fatal("year one lost", j.due, j.duePresent)
				}
			case "nested":
				if !originalJobHasDue(j) || !j.due.Equal(at) {
					t.Fatal("original past Calendar First fast-forwarded", j.due, at)
				}
			}
		}
	}
	checkState(a, beforeSave, afterSave)
	if rows := calendarOwnedList(t, a, cookie); len(rows) != 4 || rows["nested"].ID != "OwnedInput_nested" || rows["zero"].ID != "OwnedInput_zero" {
		t.Fatal("optional zero/list registration", rows)
	}
	a.dispatchOriginalPluginJobs(context.Background(), time.Now())
	a.wg.Wait()
	for _, id := range []string{"nested", "null", "zero"} {
		if count("/"+id) != 1 {
			t.Fatal("warm actual callback", id, count("/"+id))
		}
		if p := calendarOwnedProgress(t, a, cookie, id); string(p["success"]) != "true" || p["clock_error"] != nil {
			t.Fatal(id, p)
		}
	}
	if count("/missing") != 0 || count("/empty") != 0 || count("/invalid") != 0 {
		t.Fatal("invalid registration auto-called method")
	}
	builtinOwnedManual(t, a, cookie, "missing")
	builtinOwnedManual(t, a, cookie, "empty")
	builtinOwnedManual(t, a, cookie, "invalid")
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	coldBefore := time.Now()
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	coldAfter := time.Now()
	coldCookie := calendarOwnedLogin(t, cold)
	coldPID := calendarOwnedPID(t, cold)
	if coldPID == pid {
		t.Fatal("cold did not start new worker")
	}
	checkState(cold, coldBefore, coldAfter)
	cold.dispatchOriginalPluginJobs(context.Background(), time.Now())
	cold.wg.Wait()
	for _, id := range []string{"nested", "null", "zero"} {
		if count("/"+id) != 2 {
			t.Fatal("cold actual redeclaration", id, count("/"+id))
		}
	}
	if count("/missing") != 1 || count("/empty") != 1 || count("/invalid") != 1 || artifacts.index.Load() != 1 || artifacts.worker.Load() != 1 {
		t.Fatal("cold invalid/default/artifact counts", calls)
	}
	builtinOwnedManual(t, cold, coldCookie, "missing")
	t.Logf("actual worker PID %d -> %d; same stored missing/null/{} and null kwargs reconstructed; nested and valid zero clock; original cold redeclaration, two artifact GETs", pid, coldPID)
}

func TestOriginalBuiltinActualWorkerFaultsManualAndResave(t *testing.T) {
	var ordinary, manual, blocked atomic.Int64
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	peer := calendarOwnedPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected method", r.Method)
			http.Error(w, "method", 405)
			return
		}
		switch r.URL.Path {
		case "/ordinary":
			ordinary.Add(1)
		case "/manual":
			manual.Add(1)
		case "/blocked":
			if blocked.Add(1) == 1 {
				close(reached)
			}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		default:
			t.Error("unexpected callback", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(204)
	}))
	a, cookie, _ := calendarOwnedInstall(t)
	defer once.Do(func() { close(release) })
	at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	fault := calendarOwnedDefinition("fault", map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": -1000, "start_at": at.Format(time.RFC3339Nano)}, peer.URL+"/manual")
	fault["kwargs"] = map[string]any{"misfire_grace_time": nil, "next_run_time": at.Format(time.RFC3339Nano)}
	other := calendarOwnedDefinition("other", builtinOwnedDate(at), peer.URL+"/ordinary")
	calendarOwnedConfig(t, a, cookie, fault, other)
	a.dispatchOriginalPluginJobs(context.Background(), time.Now())
	a.wg.Wait()
	const dueFault = "builtin trigger boundary: due_collection_budget"
	if manual.Load() != 0 || ordinary.Load() != 1 {
		t.Fatal("collection fault submitted or suppressed another job", manual.Load(), ordinary.Load())
	}
	row := calendarOwnedList(t, a, cookie)["fault"]
	if row.Status != "停止" || !row.CanRun || row.NextRun == nil || *row.NextRun != "时钟已停止："+dueFault {
		t.Fatal("fault visible wire/native", row)
	}
	p := calendarOwnedProgress(t, a, cookie, "fault")
	if string(p["clock_error"]) != `"`+dueFault+`"` || string(p["success"]) != "null" || string(p["error"]) != "null" {
		t.Fatal("clock fault fabricated callback result", p)
	}
	builtinOwnedManual(t, a, cookie, "fault")
	p = calendarOwnedProgress(t, a, cookie, "fault")
	if manual.Load() != 1 || string(p["clock_error"]) != `"`+dueFault+`"` {
		t.Fatal("manual suppressed/erased independent clock error", p, manual.Load())
	}
	flushTestLogs(t, a)
	logs := callTest(t, a.Handler(), "GET", "/api/logs?job_id=OwnedInput_fault&limit=100", "", cookie, "")
	var page struct{ Data logPage }
	if logs.Code != 200 || json.Unmarshal(logs.Body.Bytes(), &page) != nil {
		t.Fatal("logs", logs.Body.String())
	}
	found := 0
	for _, e := range page.Data.Entries {
		if e["event"] == "builtin_trigger_error" && e["job_id"] == "OwnedInput_fault" && e["level"] == "ERROR" && e["message"] == dueFault {
			found++
		}
	}
	if found != 1 {
		t.Fatal("clock error persisted exactly once", found)
	}
	repaired := calendarOwnedDefinition("fault", builtinOwnedDate(time.Now().Add(-time.Second)), peer.URL+"/manual")
	calendarOwnedConfig(t, a, cookie, repaired)
	if p = calendarOwnedProgress(t, a, cookie, "fault"); p["clock_error"] != nil {
		t.Fatal("resave retained fault", p)
	}
	a.dispatchOriginalPluginJobs(context.Background(), time.Now())
	a.wg.Wait()
	if manual.Load() != 2 {
		t.Fatal("resave did not recover clock", manual.Load())
	}
	// Use the real evaluator with a deliberately one-call test budget to isolate
	// post-admission exhaustion without 65K computations or a wall-time race. The
	// separate policy root checks the exact production limits. No fake Next port.
	pending := calendarOwnedDefinition("fault", map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 3600000, "start_at": at.Format(time.RFC3339Nano)}, peer.URL+"/blocked")
	pending["kwargs"] = map[string]any{"misfire_grace_time": nil, "next_run_time": at.Format(time.RFC3339Nano)}
	calendarOwnedConfig(t, a, cookie, pending)
	limited, err := schedule.NewBuiltin(schedule.BuiltinDescriptor{Kind: "interval", Timezone: "UTC", IntervalMS: 3600000, StartAt: at.Format(time.RFC3339Nano)}, at, schedule.BuiltinLimits{MaxNodes: 1024, MaxDepth: 32, MaxEvaluations: 1})
	if err != nil {
		t.Fatal(err)
	}
	a.pluginOriginalDirectJobs.mu.Lock()
	a.pluginOriginalDirectJobs.jobs[originalServiceKey{"OwnedInput", "fault"}].builtinClock = limited
	a.pluginOriginalDirectJobs.mu.Unlock()
	a.dispatchOriginalPluginJobs(context.Background(), time.Now())
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("admitted callback lost on second Next fault")
	}
	const secondFault = "builtin trigger boundary: evaluation_budget"
	row = calendarOwnedList(t, a, cookie)["fault"]
	if row.Status != "正在运行" || row.CanRun || row.NextRun == nil || *row.NextRun != "时钟已停止："+secondFault {
		t.Fatal("running/fault UI semantics", row)
	}
	once.Do(func() { close(release) })
	a.wg.Wait()
	p = calendarOwnedProgress(t, a, cookie, "fault")
	if string(p["success"]) != "true" || string(p["error"]) != "null" || string(p["clock_error"]) != `"`+secondFault+`"` {
		t.Fatal("completion erased clock fault", p)
	}
	a.dispatchOriginalPluginJobs(context.Background(), time.Now())
	a.wg.Wait()
	if blocked.Load() != 1 {
		t.Fatal("fault replayed admitted callback", blocked.Load())
	}
	// Cancellation is a real context signal, not source None. A subsequent fresh
	// manual call to the retained method remains usable in this same worker.
	calendarOwnedConfig(t, a, cookie, repaired)
	pid := calendarOwnedPID(t, a)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	a.dispatchOriginalPluginJobs(canceled, time.Now())
	a.wg.Wait()
	p = calendarOwnedProgress(t, a, cookie, "fault")
	if string(p["clock_error"]) != `"context canceled"` || manual.Load() != 2 {
		t.Fatal("cancel became callback/None", p, manual.Load())
	}
	builtinOwnedManual(t, a, cookie, "fault")
	if calendarOwnedPID(t, a) != pid || manual.Load() != 3 {
		t.Fatal("cancel destroyed worker/manual method", manual.Load())
	}
	t.Log("owned real worker: due-bound isolation, other job and manual continue; re-save recovery; admitted callback survives shared-budget exhaustion; real cancellation retains same worker")
}
