package app

import (
	"context"
	"encoding/json"
	bf "light-media/internal/plugins/brushflow"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPluginInstanceSettingsCancelReportsCommittedRunWithoutReplay(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	p := a.pluginStore
	ctx := context.Background()
	installCloneSource(t, p, bf.NativeManifest())
	if _, e := p.SaveConfig(ctx, bf.PluginID, 0, bf.DefaultConfig()); e != nil {
		t.Fatal(e)
	}
	in, e := p.Clone(ctx, bf.PluginID, PluginCloneRequest{Suffix: "CancelOwned"})
	if e != nil {
		t.Fatal(e)
	}
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req := httptest.NewRequest("POST", "/api/plugins/"+in.InstanceID+"/api/settings", strings.NewReader(`{"enabled":false,"show_sidebar_nav":false,"global_proxy_delete":false}`)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); a.Handler().ServeHTTP(response, req) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var queued PluginRun
wait:
	for {
		select {
		case <-deadline.C:
			t.Fatal("save did not commit")
		case <-done:
			t.Fatalf("returned before pending cancellation: %d %s", response.Code, response.Body.String())
		case <-tick.C:
			r, err := p.Runs(ctx, in.InstanceID, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(r) > 0 {
				queued = r[0]
				break wait
			}
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled HTTP did not return")
	}
	var body struct {
		Saved bool      `json:"configuration_saved"`
		Run   PluginRun `json:"run"`
	}
	if response.Code != 409 || json.Unmarshal(response.Body.Bytes(), &body) != nil || !body.Saved || body.Run.ID != queued.ID {
		t.Fatalf("ambiguous committed status: %d %s", response.Code, response.Body.String())
	}
	cfg, e := p.Config(ctx, in.InstanceID)
	if e != nil || !strings.Contains(string(cfg.Value), `"show_sidebar_nav":false`) {
		t.Fatal("committed config missing", e)
	}
	runs, e := p.Runs(ctx, in.InstanceID, 10)
	if e != nil || len(runs) != 1 || runs[0].State != "pending" {
		t.Fatal("cancellation replayed/executed run", runs, e)
	}
}
