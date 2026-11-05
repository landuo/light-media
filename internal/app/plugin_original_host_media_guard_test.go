package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestOriginalHostMediaPartialRefreshGuardsAfterLoginBeforeWriteActualSDK(t *testing.T) {
	for _, mode := range []string{"configuration_changed", "scope_revoked"} {
		t.Run(mode, func(t *testing.T) {
			a, _, object := ownedHostServicesFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			var scans atomic.Int32
			const guid = "11111111111111111111111111111111"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v/api/v1/sys/version":
					fmt.Fprint(w, `{"code":0,"data":{"version":"1.0.0","mediasrvVersion":"1.0.0"}}`)
				case "/v/api/v1/login":
					fmt.Fprint(w, `{"code":0,"data":{"token":"owned-session"}}`)
				case "/v/api/v1/user/info":
					fmt.Fprint(w, `{"code":0,"data":{"is_admin":1}}`)
				case "/v/api/v1/mdb/list":
					fmt.Fprint(w, `{"code":0,"data":[{"guid":"`+guid+`","name":"owned","category":"Mix","dir_list":["/owned/library/"]}]}`)
				case "/v/api/v1/task/running":
					once.Do(func() { close(entered) })
					<-release
					fmt.Fprint(w, `{"code":0,"data":true}`)
				case "/v/api/v1/mdb/scan/" + guid:
					scans.Add(1)
					fmt.Fprint(w, `{"code":0,"data":true}`)
				default:
					t.Error("unexpected media request", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			settings := a.Settings()
			settings.FNOS = FNConfig{Host: server.URL, Username: "owned-user", Password: "owned-only-password", Enabled: true}
			if err := a.saveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			completed := make(chan error, 1)
			go func() {
				_, err := ownedHostServicesCall(t, object, plugins.MediaServersRefreshCapability, plugins.MediaRefreshRequest{Name: "fnos", Paths: []string{"/owned/library/New"}})
				completed <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				close(release)
				t.Fatal("actual login/read-library barrier not reached")
			}
			if mode == "configuration_changed" {
				settings.FNOS.Password = "changed-owned-only-password"
				if err := a.saveSettings(context.Background(), settings); err != nil {
					close(release)
					t.Fatal(err)
				}
			} else {
				object.stopping.Store(true)
			}
			close(release)
			select {
			case err := <-completed:
				if err == nil {
					t.Fatal("revoked invocation returned success")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("revoked invocation failed to return")
			}
			object.stopping.Store(false)
			if scans.Load() != 0 {
				t.Fatal("write occurred after login despite revoked current scope", scans.Load())
			}
			want := "media_host_MEDIA_SERVER_CHANGED"
			if mode == "scope_revoked" {
				want = "media_host_PLUGIN_CALL_CLOSED"
			}
			found := false
			page, err := a.queryLogs(context.Background(), map[string]string{"plugin_instance_id": "OwnedHostServices", "from": "0001-01-01T00:00:00Z", "limit": "500"})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range page.Entries {
				if row["event"] == want {
					found = true
				}
			}
			if !found {
				t.Fatal("failure was not the injected live guard", mode, want)
			}
		})
	}
}
