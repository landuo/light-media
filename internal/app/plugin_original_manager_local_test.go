package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalManagerLocalMetadataActualSDKIncludesUninstalledAndClone(t *testing.T) {
	a, cookie, object := ownedHostServicesFixture(t)
	id := object.installation.InstanceID
	saved := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, `{"enabled":false}`, cookie, "")
	assertOriginalRegistrySuccess(t, saved.Code, saved.Body.Bytes())
	clone := callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/clone", `{"suffix":"localproof","name":"独立本地元数据","version":"9.4"}`, cookie, "")
	assertOriginalRegistrySuccess(t, clone.Code, clone.Body.Bytes())
	// Source manager traversal reads states before filtering installed values,
	// catches a getter failure, and still includes the imported class metadata.
	broken := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, `{"enabled":false,"fail_state":true}`, cookie, "")
	if broken.Code == http.StatusOK {
		t.Fatal("owned source getter failure did not happen", broken.Body.String())
	}
	if err := a.setOriginalPluginInstalled(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	raw, err := ownedHostServicesCall(t, object, plugins.PluginManagerReadCapability, plugins.PluginManagerRequest{View: "local"})
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		ID        string `json:"id"`
		Name      string `json:"plugin_name"`
		Version   string `json:"plugin_version"`
		Installed bool   `json:"installed"`
		State     bool   `json:"state"`
		Order     int    `json:"plugin_order"`
	}
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != id || rows[0].Installed || rows[0].State || rows[0].Version != "1.0" || rows[1].ID != id+"localproof" || !rows[1].Installed || rows[1].Name != "独立本地元数据" || rows[1].Version != "9.4" || rows[0].Order != rows[1].Order {
		t.Fatalf("source local metadata/stable order/clone version mismatch: %s", raw)
	}
	for _, request := range []plugins.PluginManagerRequest{{View: "local", IncludeState: true}, {View: "local", InstanceID: id}} {
		if _, err = ownedHostServicesCall(t, object, plugins.PluginManagerReadCapability, request); err == nil {
			t.Fatal("source full traversal accepted a redefined selector", request)
		}
	}
}
