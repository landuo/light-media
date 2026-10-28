package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestOriginalDashboardParametersActualWorker(t *testing.T) {
	for _, tt := range []struct {
		name, env string
		want      map[string]any
		status    int
	}{
		{"legacy", "OWNED_STANDARD_FIXTURE_BINARY", map[string]any{"key": "summary", "user_agent": "Owned dashboard browser"}, 200},
		{"zero", "OWNED_STANDARD_DASHBOARD_ZERO_FIXTURE_BINARY", map[string]any{}, 200},
		{"one", "OWNED_STANDARD_DASHBOARD_ONE_FIXTURE_BINARY", map[string]any{"user_agent": "Owned dashboard browser"}, 200},
		{"two", "OWNED_STANDARD_DASHBOARD_TWO_FIXTURE_BINARY", map[string]any{"key": "summary", "user_agent": "Owned dashboard browser"}, 200},
		{"key_only", "OWNED_STANDARD_DASHBOARD_INVALID_FIXTURE_BINARY", nil, 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			binary := os.Getenv(tt.env)
			if binary == "" {
				t.Fatalf("%s must name an actual owned SDK worker", tt.env)
			}
			t.Setenv("OWNED_STANDARD_FIXTURE_BINARY", binary)
			a, cookie, object := ownedStandardSDKFixture(t)
			before := ownedStandardSnapshotRead(t, object)
			request := httptest.NewRequest(http.MethodGet, "/api/plugins/"+ownedStandardFixtureID+"/dashboard?key=summary", nil)
			request.AddCookie(cookie)
			request.Header.Set("User-Agent", "Owned dashboard browser")
			response := httptest.NewRecorder()
			a.Handler().ServeHTTP(response, request)
			if response.Code != tt.status {
				t.Fatalf("dashboard status=%d body=%s", response.Code, response.Body.String())
			}
			after := ownedStandardSnapshotRead(t, object)
			if after.Nonce != before.Nonce || after.InitCount != before.InitCount {
				t.Fatal("view restarted or reinitialized plugin")
			}
			if tt.status == 500 {
				if after.Calls["get_dashboard"] != before.Calls["get_dashboard"] {
					t.Fatal("bad keyword call reached method body")
				}
				return
			}
			var got map[string]any
			if err := json.Unmarshal(after.LastPayload["get_dashboard"], &got); err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("real worker payload=%s want=%v err=%v", after.LastPayload["get_dashboard"], tt.want, err)
			}
			if after.Calls["get_dashboard"] != before.Calls["get_dashboard"]+1 {
				t.Fatal("dashboard method not invoked exactly once")
			}
		})
	}
}
