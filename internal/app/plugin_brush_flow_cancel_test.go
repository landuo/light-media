package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrushFlowActualHTTPAbortDuringSDKReadIsTerminalNoEffect(t *testing.T) {
	a, in := brushDefault(t)
	cookie := setupTestAdmin(t, a)
	entered := make(chan struct{})
	a.pluginBrushFlow.beforeRead = func(ctx context.Context) { close(entered); <-ctx.Done() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("GET", "/api/plugins/BrushFlowLanduo/api/status", nil).WithContext(ctx)
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
	var state, code, run string
	if e := a.Store.DB.QueryRow(`SELECT id,state,error_code FROM plugin_runs WHERE instance_id=? AND action='api' ORDER BY created_at DESC LIMIT 1`, in.InstanceID).Scan(&run, &state, &code); e != nil || state != "failed" || code != "execution_canceled" {
		t.Fatal("read cancellation became unknown", state, code, e)
	}
	var blocked int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_startup_blocks WHERE run_id=?`, run).Scan(&blocked)
	if blocked != 0 {
		t.Fatal("pure read blocked startup")
	}
	proof, _, e := a.pluginStore.brushFlowRead(context.Background(), a.Store.DB, in.InstanceID, in.Generation)
	if e != nil || brushFlowNoUnknown(proof) != nil {
		t.Fatal("read polluted mutation owner", e)
	}
	a.pluginBrushFlow.beforeRead = nil
	next := callTest(t, a.Handler(), "GET", "/api/plugins/BrushFlowLanduo/api/status", "", cookie, "")
	if next.Code != 200 || !strings.Contains(next.Body.String(), `"success":true`) {
		t.Fatal("next read blocked", next.Code, next.Body.String())
	}
	// Recover the same exact read metadata left by an older interrupted binary.
	if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending',error_code='execution_canceled' WHERE id=?`, run); e != nil {
		t.Fatal(e)
	}
	if e = a.pluginStore.recoverBrushFlowReads(context.Background()); e != nil {
		t.Fatal(e)
	}
	actual, _ := a.pluginStore.Run(context.Background(), run)
	if actual.State != "failed" || actual.ErrorCode != "BRUSH_FLOW_READ_INTERRUPTED" {
		t.Fatal(actual.State, actual.ErrorCode)
	}
}
