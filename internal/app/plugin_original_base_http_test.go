package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestOriginalBaseConfigHTTPUsesCurrentCacheWithoutInitialization(t *testing.T) {
	a, cookie, object := originalBaseSDKFixture(t)
	applyOriginalBaseSDKPlan(t, a, cookie, ownedBaseFixtureID, map[string]any{"config": map[string]any{}})
	before := readOriginalBaseSDKSnapshot(t, object)
	for _, id := range []string{ownedBaseFixtureID, "OwnedUnknownConfigNamespace"} {
		response := callTest(t, a.Handler(), "GET", "/api/plugins/"+id, "", cookie, "")
		if response.Code != http.StatusOK {
			t.Fatalf("config GET %d %s", response.Code, response.Body.String())
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		want := "null"
		if id == ownedBaseFixtureID {
			want = "{}"
		}
		assertOriginalBaseJSON(t, envelope.Data, want)
	}
	anonymous := callTest(t, a.Handler(), "GET", "/api/plugins/"+ownedBaseFixtureID, "", nil, "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("config auth %d", anonymous.Code)
	}
	after := readOriginalBaseSDKSnapshot(t, object)
	if after.InitCount != before.InitCount {
		t.Fatal("config GET initialized plugin")
	}
}
