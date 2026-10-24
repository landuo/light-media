package app

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPluginOriginalUnknownRegistryHTTPNoObject(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	// Truly unknown IDs cannot have plugin_values rows under the Go FK. Do not
	// disable constraints or manufacture installation/owner receipts to fake it.
	id := "OwnedUnknownOriginalRegistry"
	for _, action := range []string{"reload", "reset"} {
		r := callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/"+action, `{}`, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		if _, ok := a.pluginRunningInstances.Load(id); ok {
			t.Fatal("unknown ID acquired object")
		}
	}
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, `{"owned":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	var n int
	for _, table := range []string{"plugin_installations", "plugin_values", "plugin_runs"} {
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE instance_id=?`, id).Scan(&n); err != nil || n != 0 {
			t.Fatalf("unknown target wrote %s n=%d err=%v", table, n, err)
		}
	}
}

func assertOriginalRegistrySuccess(t *testing.T, status int, raw []byte) {
	t.Helper()
	var body struct {
		Data struct {
			Success bool `json:"success"`
		} `json:"data"`
	}
	if status != 200 || json.Unmarshal(raw, &body) != nil || !body.Data.Success {
		t.Fatalf("original endpoint success expected: %d %s", status, raw)
	}
}
