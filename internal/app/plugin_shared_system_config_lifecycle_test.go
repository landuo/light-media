package app

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins"
)

// The real installer changes the installation generation before the old
// worker's exact close closure runs. That legitimate cleanup is not a stale
// ordinary callback and must retain its declared shared-config authority.
func TestSharedSystemConfigRealVersionUpgradeClose(t *testing.T) {
	a, cookie, p := sharedSystemConfigFixture(t, sharedSystemConfigAll)
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedSystemConfig", `{"close_value":"[\"A => ClosedV1\"]"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	v, ok := a.pluginRunningInstances.Load("OwnedSystemConfig")
	if !ok {
		t.Fatal("configured real worker missing")
	}
	p = v.(*originalPluginInstance)
	before := sharedSystemConfigWorkerSnapshot(t, p)
	if _, e := systemConfigSet(t, p, `["A => BeforeUpgrade"]`); e != nil {
		t.Fatal(e)
	}
	var generationBefore int64
	if e := a.Store.DB.QueryRow(`SELECT generation FROM plugin_installations WHERE instance_id='OwnedSystemConfig'`).Scan(&generationBefore); e != nil {
		t.Fatal(e)
	}
	replacement := installSharedSystemConfigFixtureVersion(t, a, cookie, sharedSystemConfigAll, "2.0")
	after := sharedSystemConfigWorkerSnapshot(t, replacement)
	if string(before["pid"]) == string(after["pid"]) {
		t.Fatal("upgrade did not replace actual process")
	}
	var generationAfter int64
	if e := a.Store.DB.QueryRow(`SELECT generation FROM plugin_installations WHERE instance_id='OwnedSystemConfig'`).Scan(&generationAfter); e != nil {
		t.Fatal(e)
	}
	if generationAfter <= generationBefore {
		t.Fatal("installation generation unchanged")
	}
	assertSharedOracleJSON(t, systemConfigGet(t, replacement), json.RawMessage(`["A => ClosedV1"]`))
	if got, e := a.prepareRecognitionTitleChecked(context.Background(), "A"); e != nil || got != "ClosedV1" {
		t.Fatal(got, e)
	}
	if _, e := sharedSystemConfigCall(t, p, plugins.SharedSystemConfigSet, plugins.SharedSystemConfigSetV1{Key: plugins.SystemConfigCustomIdentifiers, Value: json.RawMessage(`["stale"]`)}); e == nil {
		t.Fatal("stopped ordinary worker retained write authority")
	}
	assertSystemConfigStored(t, a, []json.RawMessage{json.RawMessage(`["A => ClosedV1"]`)})
	t.Logf("actual install v1->v2 generation %d->%d, old close shared set persisted; stopped ordinary callback denied", generationBefore, generationAfter)
}
