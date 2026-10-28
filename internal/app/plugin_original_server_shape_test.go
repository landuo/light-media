package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOriginalPluginServerRatingDefaultsAndExplicitNullMatchSource(t *testing.T) {
	var body atomic.Value
	body.Store(`{"plugin_id":"Owned"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	settings.PluginServerURL = originalServerURLPointer(server.URL)
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		path, response string
		status         int
	}{
		{"/api/plugins/rating/Owned", `{"plugin_id":"Owned"}`, http.StatusOK},
		{"/api/plugins/rating/Owned", `{"plugin_id":"Owned","average_rating":null,"rating_count":0,"user_rating":null}`, http.StatusBadGateway},
		{"/api/plugins/rating", `{"Owned":{"plugin_id":"Owned"}}`, http.StatusOK},
	} {
		body.Store(entry.response)
		r := callTest(t, a.Handler(), "GET", entry.path, "", cookie, "")
		if r.Code != entry.status {
			t.Fatalf("fixed rating schema defaults/null rejection differ: %d %s", r.Code, r.Body.String())
		}
		if entry.status == http.StatusOK && (!strings.Contains(r.Body.String(), `"average_rating":0`) || !strings.Contains(r.Body.String(), `"rating_count":0`) || !strings.Contains(r.Body.String(), `"user_rating":null`)) {
			t.Fatal("fixed Pydantic defaults missing", r.Body.String())
		}
	}
	body.Store(`{"plugin_id":"Owned","average_rating":0,"rating_count":0,"user_rating":null}`)
	if r := callTest(t, a.Handler(), "GET", "/api/plugins/rating/Owned", "", cookie, ""); r.Code != 200 {
		t.Fatal("explicit observed zero was rejected", r.Body.String())
	}
}

func TestOriginalPluginServerExplicitEmptySurvivesColdSettings(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/markets", `{"plugin_server_url":""}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if a.config.PluginServerURL == nil || *a.config.PluginServerURL != "" {
		t.Fatal("explicit empty center default was not represented")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if cold.config.PluginServerURL == nil || *cold.config.PluginServerURL != "" || *cold.Settings().PluginServerURL != "" {
		t.Fatal("cold settings lost explicit empty center override")
	}
}
