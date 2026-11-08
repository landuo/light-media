package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// The source initializer invokes manager helpers synchronously on the same
// call stack. An actual reverse SDK call must keep that operation's ownership;
// an unrelated concurrent request must still fail rather than enter its lock.
func TestOriginalManagerSourceOperationLeaseActualSDK(t *testing.T) {
	a, cookie, object := ownedHostServicesFixture(t)
	saved := callTest(t, a.Handler(), "PUT", "/api/plugins/"+object.installation.InstanceID, `{"enabled":false,"marker":"source-owned"}`, cookie, "")
	assertOriginalRegistrySuccess(t, saved.Code, saved.Body.Bytes())
	rows := func() int {
		var count int
		if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_values WHERE instance_id=? AND kind='config'`, object.installation.InstanceID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if rows() != 1 {
		t.Fatal("owned configuration was not persisted")
	}
	call := func(ctx context.Context) (plugins.PluginControlResult, error) {
		input, err := json.Marshal(map[string]any{"method": plugins.PluginManagerControlCapability, "input": plugins.PluginManagerRequest{InstanceID: object.installation.InstanceID, Action: "config.delete"}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := object.callOriginalMethod(ctx, "resource", input)
		var output plugins.PluginControlResult
		if err == nil {
			err = json.Unmarshal(result.Result, &output)
		}
		return output, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.pluginOriginalOperationMu.Lock()
	defer a.pluginOriginalOperationMu.Unlock()
	if _, err := call(ctx); err == nil || rows() != 1 {
		t.Fatal("unrelated callback entered an active operation", err)
	}
	owned, release := a.bindOriginalOperationOwner(ctx)
	result, err := call(owned)
	if err != nil || !result.Accepted || !result.Completed || string(result.Result) != "true" || rows() != 0 {
		t.Fatal("source same-stack callback did not actually delete its configuration", result, err)
	}
	release()
	if _, err = call(owned); err == nil {
		t.Fatal("closed owner lease re-entered the still-held operation")
	}
}

func TestOriginalManagerOperationLeaseCannotCrossAppOrCancellation(t *testing.T) {
	first, second := &App{}, &App{}
	ctx, end := first.bindOriginalOperationOwner(context.Background())
	defer end()
	second.pluginOriginalOperationMu.Lock()
	defer second.pluginOriginalOperationMu.Unlock()
	if _, _, err := second.enterOriginalManagerOperation(ctx); err == nil {
		t.Fatal("foreign host borrowed the lease")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := first.enterOriginalManagerOperation(cancelled); err == nil {
		t.Fatal("cancelled original operation remained admitted")
	}
}
