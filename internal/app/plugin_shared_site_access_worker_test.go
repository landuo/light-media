package app

// Actual installed ordinary worker -> SDK -> host -> owned SQLite. The
// coordinator provides a prebuilt fixture. No build, environment rewriting,
// replacement HostCaller, native factory, or public statistics getter is used.
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

const (
	ownedAccessWorkerID = "OwnedSiteAccess"
	ownedAccessDomain   = "owned-site-access.invalid"
	ownedAccessSecond   = "owned-site-access-second.invalid"
	ownedAccessSpaced   = " Owned-Site-Access.invalid "
)

type ownedAccessWorkerCalls struct {
	Total, Succeeded, Failed int
	InputJSONBytes           int `json:"input_json_bytes"`
	OutputJSONBytes          int `json:"output_json_bytes"`
}

type ownedAccessWorkerAttempt struct {
	Index      int
	Op, Domain string
	OK         bool
	SourceNone bool                   `json:"source_none"`
	ErrorCode  string                 `json:"error_code"`
	HostCalls  ownedAccessWorkerCalls `json:"host_calls"`
}

type ownedAccessWorkerResult struct {
	PID, Invocation, Requested, Completed, Succeeded, Failed int
	InitCount                                                int `json:"init_count"`
	OK                                                       bool
	Interrupted                                              string
	Results                                                  []ownedAccessWorkerAttempt
	PreviousProbe                                            *struct {
		Label     string
		Available bool
		Denied    *bool
		ErrorCode string                 `json:"error_code"`
		HostCalls ownedAccessWorkerCalls `json:"host_calls"`
	} `json:"previous_probe"`
}

// Keep exact persisted nullable/raw values, including the note JSON text, when
// checking fail preservation and cold reopen. Only this test's owned DB is read.
type ownedAccessWorkerRow struct {
	ID            int64
	Domain        string
	Success, Fail int64
	Seconds       sql.NullInt64
	State         int64
	Modified      string
	Note          sql.NullString
}

func ownedAccessWorkerRead(t *testing.T, a *App, domain string) ownedAccessWorkerRow {
	t.Helper()
	var out ownedAccessWorkerRow
	err := a.Store.DB.QueryRow(`SELECT id,domain,success,fail,seconds,lst_state,lst_mod_date,note FROM source_site_access_statistic WHERE domain=?`, domain).Scan(&out.ID, &out.Domain, &out.Success, &out.Fail, &out.Seconds, &out.State, &out.Modified, &out.Note)
	if err != nil || out.ID <= 0 || out.Domain != domain {
		t.Fatalf("owned persisted access row absent or mismatched: %+v %v", out, err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM source_site_access_statistic WHERE domain=?`, domain).Scan(&count); err != nil || count != 1 {
		t.Fatal("owned domain was duplicated", count, err)
	}
	return out
}

func ownedAccessWorkerMissing(t *testing.T, a *App, domain string) {
	t.Helper()
	var id int64
	if err := a.Store.DB.QueryRow(`SELECT id FROM source_site_access_statistic WHERE domain=?`, domain).Scan(&id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("owned access row unexpectedly present", id, err)
	}
}

func ownedAccessWorkerStamp(t *testing.T, stamp string, before, after time.Time) {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", stamp, time.Local)
	if err != nil || parsed.Format("2006-01-02 15:04:05") != stamp || parsed.Before(before.Truncate(time.Second)) || parsed.After(after.Truncate(time.Second)) {
		t.Fatal("persisted real timestamp outside its actual call window", stamp, before, after, err)
	}
}

func ownedAccessWorkerNote(t *testing.T, row ownedAccessWorkerRow, want map[string]int64) {
	t.Helper()
	if !row.Note.Valid {
		t.Fatal("owned note unexpectedly SQL NULL")
	}
	var got map[string]int64
	if err := json.Unmarshal([]byte(row.Note.String), &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("owned source note differs", got, want, err)
	}
}

func ownedAccessWorkerInstall(t *testing.T, a *App, cookie *http.Cookie) {
	t.Helper()
	index, payload := originalMarketOwnedIndex(t, "OWNED_SITE_ACCESS_WORKER")
	manifest := index.Releases[0].Manifest
	if manifest.Validate() != nil || manifest.PluginID != ownedAccessWorkerID || manifest.Version != "1.0" || len(manifest.APIs) != 1 || manifest.APIs[0].Key != "access" || manifest.APIs[0].Path != "/access" || manifest.APIs[0].Auth != "bear" || manifest.APIs[0].Body != "json" || !manifest.APIs[0].BodyRequired || !reflect.DeepEqual(manifest.APIs[0].Methods, []string{"POST"}) || !reflect.DeepEqual(manifest.Capabilities, []string{plugins.SiteAccessSuccessCapability, plugins.SiteAccessFailCapability}) {
		t.Fatal("actual owned access manifest differs")
	}
	index.Releases[0].Artifacts[0].HostContract = pluginResourcesSDKContract
	var indexHits, artifactHits, unexpected atomic.Int32
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			unexpected.Add(1)
			http.Error(w, "owned read-only artifact", 405)
			return
		}
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	index.Releases[0].Artifacts[0].URL = "http://" + peer.Listener.Addr().String() + "/worker"
	peer.Start()
	t.Cleanup(func() {
		peer.Close()
		if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
			t.Errorf("owned access artifact requests %d/%d/%d", indexHits.Load(), artifactHits.Load(), unexpected.Load())
		}
	})
	body, _ := json.Marshal(map[string]string{"plugin_id": ownedAccessWorkerID, "repo_url": peer.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	loaded, ok := a.pluginRunningInstances.Load(ownedAccessWorkerID)
	if !ok {
		t.Fatal("installed access worker absent")
	}
	object, ok := loaded.(*originalPluginInstance)
	if !ok || !object.external || object.hostContract != pluginResourcesSDKContract {
		t.Fatal("not an actual resources-contract access worker")
	}
	installation, err := a.pluginStore.Installation(context.Background(), ownedAccessWorkerID)
	if err != nil || !installation.Installed {
		t.Fatal("real access installation missing", err)
	}
}

func ownedAccessWorkerFixture(t *testing.T) (*App, *http.Cookie) {
	t.Helper()
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	cfg := a.Settings()
	cfg.PluginMarkets, cfg.PluginLocalRepoPaths = []string{}, []string{}
	cfg.PluginAutoReload = false
	cfg.Sites, cfg.Downloaders = []SiteConfig{}, []DownloaderConfig{}
	if err := a.saveSettings(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	ownedAccessWorkerInstall(t, a, cookie)
	return a, cookie
}

func ownedAccessWorkerOperation(op, domain string, seconds any) map[string]any {
	value := map[string]any{"op": op, "domain": domain}
	if op == "success" {
		value["seconds"] = seconds
	}
	return value
}

func ownedAccessWorkerQuery(t *testing.T, a *App, cookie *http.Cookie, probe bool, operations ...map[string]any) (ownedAccessWorkerResult, time.Time, time.Time) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"operations": operations, "continue_on_error": true, "probe_previous": probe})
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/"+ownedAccessWorkerID+"/access", string(body), cookie, "")
	after := time.Now()
	if response.Code != http.StatusOK {
		t.Fatalf("actual access API HTTP %d", response.Code)
	}
	var out ownedAccessWorkerResult
	if json.Unmarshal(response.Body.Bytes(), &out) != nil || out.PID <= 0 || out.PID == os.Getpid() || out.InitCount < 1 || out.Requested != len(operations) || out.Completed != len(operations) || len(out.Results) != len(operations) || out.Interrupted != "" {
		t.Fatalf("actual access worker observation incomplete: %+v", out)
	}
	for i, attempt := range out.Results {
		wantDomain := operations[i]["domain"]
		if operations[i]["op"] == "invalid_wire" {
			wantDomain = ownedAccessDomain
		}
		if attempt.Index != i || attempt.Op != operations[i]["op"] || attempt.Domain != wantDomain || attempt.HostCalls.Total != 1 || attempt.HostCalls.Succeeded+attempt.HostCalls.Failed != 1 || attempt.HostCalls.InputJSONBytes <= 0 {
			t.Fatal("actual access callback attempt differs", attempt)
		}
	}
	return out, before, after
}

func ownedAccessWorkerSuccess(t *testing.T, out ownedAccessWorkerResult) {
	t.Helper()
	if !out.OK || out.Failed != 0 || out.Succeeded != len(out.Results) {
		t.Fatal("actual access SDK batch failed", out)
	}
	for _, attempt := range out.Results {
		if !attempt.OK || !attempt.SourceNone || attempt.ErrorCode != "" || attempt.HostCalls.Succeeded != 1 || attempt.HostCalls.Failed != 0 || attempt.HostCalls.OutputJSONBytes != 4 {
			t.Fatal("actual SDK did not return source None exactly once", attempt)
		}
	}
}

func ownedAccessWorkerWarm(t *testing.T, before, after ownedAccessWorkerResult) {
	t.Helper()
	if before.PID != after.PID || before.InitCount != after.InitCount || after.Invocation != before.Invocation+1 {
		t.Fatal("owned worker identity/init continuity differs", before, after)
	}
	p := after.PreviousProbe
	if p == nil || p.Label != "previous_invocation_fixed_frame" || !p.Available || p.Denied == nil || !*p.Denied || p.ErrorCode != "invalid_host_call" || p.HostCalls.Total != 1 || p.HostCalls.Failed != 1 || p.HostCalls.Succeeded != 0 || p.HostCalls.InputJSONBytes <= 0 || p.HostCalls.OutputJSONBytes != 0 {
		t.Fatal("expired actual worker callback not rejected exactly once", p)
	}
}

func TestSharedSiteAccessActualWorkerRecoveryAndScopes(t *testing.T) {
	a, cookie := ownedAccessWorkerFixture(t)
	ownedAccessWorkerMissing(t, a, ownedAccessDomain)
	first, before, after := ownedAccessWorkerQuery(t, a, cookie, false, ownedAccessWorkerOperation("success", ownedAccessDomain, nil))
	ownedAccessWorkerSuccess(t, first)
	if first.Invocation != 1 || first.PreviousProbe != nil {
		t.Fatal("fresh worker invocation differs", first)
	}
	main := ownedAccessWorkerRead(t, a, ownedAccessDomain)
	if main.Success != 1 || main.Fail != 0 || main.State != 0 || main.Seconds != (sql.NullInt64{Int64: 1, Valid: true}) {
		t.Fatal("first source None statistics differ", main)
	}
	ownedAccessWorkerNote(t, main, map[string]int64{})
	ownedAccessWorkerStamp(t, main.Modified, before, after)

	second, before, after := ownedAccessWorkerQuery(t, a, cookie, true, ownedAccessWorkerOperation("success", ownedAccessSecond, 7))
	ownedAccessWorkerSuccess(t, second)
	ownedAccessWorkerWarm(t, first, second)
	if got := ownedAccessWorkerRead(t, a, ownedAccessDomain); got != main {
		t.Fatal("stale caller probe changed prior owned row", got, main)
	}
	timed := ownedAccessWorkerRead(t, a, ownedAccessSecond)
	if timed.Success != 1 || timed.Fail != 0 || timed.State != 0 || timed.Seconds != (sql.NullInt64{Int64: 7, Valid: true}) {
		t.Fatal("first nonzero source timing differs", timed)
	}
	ownedAccessWorkerNote(t, timed, map[string]int64{timed.Modified: 7})
	ownedAccessWorkerStamp(t, timed.Modified, before, after)

	failed, before, after := ownedAccessWorkerQuery(t, a, cookie, true, ownedAccessWorkerOperation("fail", ownedAccessSecond, nil))
	ownedAccessWorkerSuccess(t, failed)
	ownedAccessWorkerWarm(t, second, failed)
	kept := ownedAccessWorkerRead(t, a, ownedAccessSecond)
	if kept.ID != timed.ID || kept.Success != timed.Success || kept.Fail != 1 || kept.State != 1 || kept.Seconds != timed.Seconds || kept.Note != timed.Note {
		t.Fatal("source fail did not retain raw timing/note", timed, kept)
	}
	ownedAccessWorkerStamp(t, kept.Modified, before, after)
	if got := ownedAccessWorkerRead(t, a, ownedAccessDomain); got != main {
		t.Fatal("old caller unexpectedly incremented main row", got, main)
	}

	recovered, before, after := ownedAccessWorkerQuery(t, a, cookie, true, map[string]any{"op": "invalid_wire"}, ownedAccessWorkerOperation("success", ownedAccessDomain, nil))
	ownedAccessWorkerWarm(t, failed, recovered)
	bad, good := recovered.Results[0], recovered.Results[1]
	if recovered.OK || recovered.Failed != 1 || recovered.Succeeded != 1 || bad.OK || bad.SourceNone || bad.ErrorCode != "host_operation_failed" || bad.HostCalls.Failed != 1 || bad.HostCalls.Succeeded != 0 || bad.HostCalls.OutputJSONBytes != 0 || !good.OK || !good.SourceNone || good.ErrorCode != "" || good.HostCalls.Succeeded != 1 || good.HostCalls.Failed != 0 || good.HostCalls.OutputJSONBytes != 4 {
		t.Fatal("invalid_wire boundary or same-worker typed recovery differs", recovered)
	}
	mainAfter := ownedAccessWorkerRead(t, a, ownedAccessDomain)
	if mainAfter.ID != main.ID || mainAfter.Success != 2 || mainAfter.Fail != 0 || mainAfter.State != 0 || mainAfter.Seconds != main.Seconds || mainAfter.Note != main.Note {
		t.Fatal("invalid/stale frame mutated row or None changed timing", mainAfter, main)
	}
	ownedAccessWorkerStamp(t, mainAfter.Modified, before, after)
	if got := ownedAccessWorkerRead(t, a, ownedAccessSecond); got != kept {
		t.Fatal("unrelated owned row changed", got, kept)
	}

	labels, before, after := ownedAccessWorkerQuery(t, a, cookie, true, ownedAccessWorkerOperation("success", "", 0), ownedAccessWorkerOperation("success", ownedAccessSpaced, -3))
	ownedAccessWorkerSuccess(t, labels)
	ownedAccessWorkerWarm(t, recovered, labels)
	for domain, seconds := range map[string]int64{"": 1, ownedAccessSpaced: -3} {
		row := ownedAccessWorkerRead(t, a, domain)
		if row.Success != 1 || row.Fail != 0 || row.State != 0 || row.Seconds != (sql.NullInt64{Int64: seconds, Valid: true}) {
			t.Fatal("exact domain/zero/negative source semantics differ", row)
		}
		ownedAccessWorkerNote(t, row, map[string]int64{row.Modified: seconds})
		ownedAccessWorkerStamp(t, row.Modified, before, after)
	}
	if got := ownedAccessWorkerRead(t, a, ownedAccessDomain); got != mainAfter {
		t.Fatal("final stale caller added an extra mutation", got, mainAfter)
	}
}

func TestSharedSiteAccessActualWorkerColdPersistence(t *testing.T) {
	a, cookie := ownedAccessWorkerFixture(t)
	first, before, after := ownedAccessWorkerQuery(t, a, cookie, false, ownedAccessWorkerOperation("success", ownedAccessDomain, 7), ownedAccessWorkerOperation("fail", ownedAccessDomain, nil), ownedAccessWorkerOperation("fail", "", nil))
	ownedAccessWorkerSuccess(t, first)
	main, empty := ownedAccessWorkerRead(t, a, ownedAccessDomain), ownedAccessWorkerRead(t, a, "")
	if main.Success != 1 || main.Fail != 1 || main.State != 1 || main.Seconds != (sql.NullInt64{Int64: 7, Valid: true}) || !main.Note.Valid {
		t.Fatal("warm timing/fail row differs", main)
	}
	var note map[string]int64
	if json.Unmarshal([]byte(main.Note.String), &note) != nil || len(note) != 1 {
		t.Fatal("warm source note differs", main)
	}
	for stamp, value := range note {
		if value != 7 {
			t.Fatal("source timing changed", value)
		}
		ownedAccessWorkerStamp(t, stamp, before, after)
	}
	if empty.Success != 0 || empty.Fail != 1 || empty.State != 1 || empty.Seconds.Valid || empty.Note.Valid {
		t.Fatal("first fail SQL NULL fields differ", empty)
	}
	ownedAccessWorkerStamp(t, main.Modified, before, after)
	ownedAccessWorkerStamp(t, empty.Modified, before, after)
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if got := ownedAccessWorkerRead(t, cold, ownedAccessDomain); got != main {
		t.Fatal("cold eight-column/raw-note row changed", got, main)
	}
	if got := ownedAccessWorkerRead(t, cold, ""); got != empty {
		t.Fatal("cold SQL NULL row changed", got, empty)
	}
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	login := callTest(t, cold.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	var coldCookie *http.Cookie
	for _, value := range login.Result().Cookies() {
		if value.Name == "lm_session" {
			coldCookie = value
		}
	}
	if login.Code != 200 || coldCookie == nil {
		t.Fatal("cold normal login failed")
	}
	reopened, before, after := ownedAccessWorkerQuery(t, cold, coldCookie, false, ownedAccessWorkerOperation("success", ownedAccessSecond, nil))
	ownedAccessWorkerSuccess(t, reopened)
	if reopened.PID == first.PID || reopened.Invocation != 1 || reopened.PreviousProbe != nil {
		t.Fatal("cold API did not use a new actual worker", first, reopened)
	}
	if got := ownedAccessWorkerRead(t, cold, ownedAccessDomain); got != main {
		t.Fatal("cold new worker changed prior owned row", got, main)
	}
	if got := ownedAccessWorkerRead(t, cold, ""); got != empty {
		t.Fatal("cold new worker changed prior NULL row", got, empty)
	}
	row := ownedAccessWorkerRead(t, cold, ownedAccessSecond)
	if row.Success != 1 || row.Fail != 0 || row.State != 0 || row.Seconds != (sql.NullInt64{Int64: 1, Valid: true}) || row.ID == main.ID || row.ID == empty.ID {
		t.Fatal("cold source None/real new row ID differs", row)
	}
	ownedAccessWorkerNote(t, row, map[string]int64{})
	ownedAccessWorkerStamp(t, row.Modified, before, after)
}
