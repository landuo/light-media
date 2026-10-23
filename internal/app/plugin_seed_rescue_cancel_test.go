package app

import (
	"context"
	"light-media/internal/plugins"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSeedRescueActualHTTPAbortDuringSDKReadIsTerminalNoEffect(t *testing.T) {
	a, in := rescueDefault(t)
	cookie := setupTestAdmin(t, a)
	entered := make(chan struct{})
	a.pluginSeedRescue.beforeRead = func(ctx context.Context) { close(entered); <-ctx.Done() }
	before := instanceReadTables(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("GET", "/api/plugins/SeedRescue/api/status", nil).WithContext(ctx)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { a.Handler().ServeHTTP(response, request); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual SDK never entered readonly host")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("aborted SDK read did not terminate")
	}
	if !reflect.DeepEqual(before, instanceReadTables(t, a)) {
		t.Fatal("aborted common read changed configuration, queue or native state")
	}
	// Preserve the real older SDK read path independently: it has authenticated
	// queued API metadata, unlike the current zero-queue HTTP projection.
	legacyEntered := make(chan struct{})
	a.pluginSeedRescue.beforeRead = func(c context.Context) { close(legacyEntered); <-c.Done() }
	input := plugins.HTTPRequest{Key: "status", Method: "GET", Path: "/status", RequestID: "owned-legacy-read-cancel"}
	queued, err := a.enqueuePluginHTTP(context.Background(), in, input, pluginAPIScope{Key: input.Key, Method: input.Method, RequestID: input.RequestID, Authority: pluginAPIAuthority{Kind: "session", Proof: tokenHash(cookie.Value)}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil || claimed.ID != queued.ID {
		t.Fatal("legacy read claim", err)
	}
	legacyCtx, legacyCancel := context.WithCancel(context.Background())
	defer legacyCancel()
	legacyDone := make(chan struct{})
	go func() { a.executePluginRun(legacyCtx, claimed); close(legacyDone) }()
	select {
	case <-legacyEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual legacy SDK did not enter read")
	}
	legacyCancel()
	select {
	case <-legacyDone:
	case <-time.After(10 * time.Second):
		t.Fatal("actual legacy SDK cancellation did not join")
	}
	var state, code, run string
	if e := a.Store.DB.QueryRow(`SELECT id,state,error_code FROM plugin_runs WHERE instance_id=? AND action='api' ORDER BY created_at DESC LIMIT 1`, in.InstanceID).Scan(&run, &state, &code); e != nil || state != "failed" || code != "execution_canceled" {
		t.Fatal("read cancellation became unknown", state, code, e)
	}
	var blocked int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_startup_blocks WHERE run_id=?`, run).Scan(&blocked)
	if blocked != 0 {
		t.Fatal("pure read blocked startup")
	}
	proof, _, e := a.pluginStore.seedRescueRead(context.Background(), a.Store.DB, in.InstanceID, in.Generation)
	if e != nil || seedRescueNoUnknown(proof) != nil {
		t.Fatal("read polluted mutation owner", e)
	}
	a.pluginSeedRescue.beforeRead = nil
	next := callTest(t, a.Handler(), "GET", "/api/plugins/SeedRescue/api/status", "", cookie, "")
	if next.Code != 200 || !strings.Contains(next.Body.String(), `"success":true`) {
		t.Fatal("next read blocked", next.Code, next.Body.String())
	}
	// Recover the same exact read metadata left by an older interrupted binary.
	if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending',error_code='execution_canceled' WHERE id=?`, run); e != nil {
		t.Fatal(e)
	}
	if e = a.pluginStore.recoverSeedRescueReads(context.Background()); e != nil {
		t.Fatal(e)
	}
	actual, _ := a.pluginStore.Run(context.Background(), run)
	if actual.State != "failed" || actual.ErrorCode != "SEED_RESCUE_READ_INTERRUPTED" {
		t.Fatal(actual.State, actual.ErrorCode)
	}
}
