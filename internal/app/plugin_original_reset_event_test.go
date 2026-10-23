package app

import (
	"context"
	"errors"
	"light-media/internal/plugins/builtin"
	"reflect"
	"testing"
)

func TestPluginOriginalResetChainRunsBeforeStopAndContinuesAfterHandlerError(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	if _, err := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, map[string]any{"enable": true}); err != nil {
		t.Fatal(err)
	}
	object := a.startOriginalPlugin(ctx, "SystemProcess")
	if object == nil {
		t.Fatal("init")
	}
	calls := []string{}
	laterEnabled := true
	a.registerOriginalPluginDataResetHandler(originalPluginDataResetHandler{ID: "later-disabled", Priority: 20, Enabled: func() bool { return laterEnabled }, Invoke: func(context.Context, *originalPluginDataResetEvent) error {
		t.Error("enabled flag not rechecked")
		return nil
	}})
	a.registerOriginalPluginDataResetHandler(originalPluginDataResetHandler{ID: "first", Priority: 1, Invoke: func(c context.Context, e *originalPluginDataResetEvent) error {
		if e.PluginID != "SystemProcess" || !e.ResetConfig || !e.ResetData {
			t.Fatal("original payload")
		}
		loaded, _ := a.pluginRunningInstances.Load(e.PluginID)
		if loaded != object {
			t.Fatal("handler ran after stop")
		}
		if _, err := a.pluginStore.Config(c, e.PluginID); err != nil {
			t.Fatal("handler ran after config deletion")
		}
		calls = append(calls, "first")
		laterEnabled = false
		return errors.New("owned original handler error")
	}})
	a.registerOriginalPluginDataResetHandler(originalPluginDataResetHandler{ID: "second", Priority: 10, Invoke: func(context.Context, *originalPluginDataResetEvent) error {
		calls = append(calls, "second")
		return nil
	}})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/SystemProcess/reset", `{}`, cookie, "")
	if response.Code != 200 {
		t.Fatalf("caught handler exception interrupted reset: %d %s", response.Code, response.Body.String())
	}
	a.wg.Wait()
	if !reflect.DeepEqual(calls, []string{"first", "second"}) {
		t.Fatalf("chain order %v", calls)
	}
	loaded, ok := a.pluginRunningInstances.Load("SystemProcess")
	if !ok || loaded == object {
		t.Fatal("reset did not proceed to reload")
	}
}
