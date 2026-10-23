package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This first executable slice covers actual init calls and copied config only.
// It is NOT the complete stop/event/register original-sequence acceptance.
func TestPluginOriginalCloneInitializerCalls(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config any
		save   bool
		calls  int
	}{
		{"absent", nil, false, 2}, {"empty", map[string]any{}, true, 2},
		{"null", nil, true, 2}, {"nonempty", map[string]any{"onlyonce": true, "future": 0}, true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
			if tc.name == "null" {
				// Original SystemConfig can contain None. Seed only this owned
				// stored configuration, not any execution or completion proof.
				sealed, e := a.pluginStore.seal("config", "SystemProcess", "main", []byte(`null`))
				if e != nil {
					t.Fatal(e)
				}
				_, e = a.Store.DB.Exec(`INSERT INTO plugin_values(instance_id,kind,item_key,revision,sealed,updated_at) VALUES('SystemProcess','config','main',1,?,?)`, sealed, now())
				if e != nil {
					t.Fatal(e)
				}
			} else if tc.save {
				if _, e := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, tc.config); e != nil {
					t.Fatal(e)
				}
			}
			if a.startOriginalPlugin(ctx, "SystemProcess") == nil {
				t.Fatal("original source class must first be loaded")
			}
			// Original initialization is synchronous and does not require a server worker.
			response := callTest(t, a.Handler(), "POST", "/api/plugins/SystemProcess/clone", `{"suffix":"Original"}`, cookie, "")
			if response.Code != 200 {
				t.Fatalf("clone: %d %s", response.Code, response.Body.String())
			}
			if _, loaded := a.pluginRunningInstances.Load("SystemProcessoriginal"); !loaded {
				t.Fatal("successful initializer did not install running object")
			}
			var total, succeeded int
			if e := a.Store.DB.QueryRow(`SELECT COUNT(*),COALESCE(SUM(state='succeeded'),0) FROM plugin_runs WHERE instance_id='SystemProcessoriginal'`).Scan(&total, &succeeded); e != nil {
				t.Fatal(e)
			}
			if total != tc.calls || succeeded != tc.calls {
				t.Fatalf("initializer calls total=%d succeeded=%d want=%d", total, succeeded, tc.calls)
			}
			config, e := a.pluginStore.Config(ctx, "SystemProcessoriginal")
			if tc.name != "nonempty" {
				if !errors.Is(e, sql.ErrNoRows) {
					t.Fatalf("original absence must stay absent: %v", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			var got map[string]any
			if e = json.Unmarshal(config.Value, &got); e != nil {
				t.Fatal(e)
			}
			if got["enable"] != false || got["enabled"] != false || got["onlyonce"] != true || got["future"] != float64(0) {
				t.Fatalf("copy changed original config: %s", config.Value)
			}
		})
	}
}

func TestPluginOriginalCloneInitExceptionSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, fail      string
		calls           int
		success, loaded bool
	}{
		{"first-start-caught", "1", 2, true, true},
		{"both-starts-caught", "1,2", 2, true, false},
		{"direct-init-propagates", "2", 2, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
			if _, e := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, map[string]any{"future": 1}); e != nil {
				t.Fatal(e)
			}
			useBuiltInPluginProcess(t, a)
			if a.startOriginalPlugin(ctx, "SystemProcess") == nil {
				t.Fatal("source class load")
			}
			executable, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			counter := filepath.Join(t.TempDir(), "init-count")
			a.pluginCommand = func(string) (plugins.Command, error) {
				return plugins.Command{Path: executable, Args: []string{"-test.run=^TestOriginalCloneExceptionSDKFixture$", "--", counter, tc.fail}}, nil
			}
			done := make(chan struct{})
			go func() { defer close(done); a.pluginWorker(ctx) }()
			defer func() { cancel(); <-done }()
			response := callTest(t, a.Handler(), "POST", "/api/plugins/SystemProcess/clone", `{"suffix":"Exception"}`, cookie, "")
			var body struct {
				Data struct {
					Success bool `json:"success"`
				} `json:"data"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.Success != tc.success {
				t.Fatalf("original exception response %d %s", response.Code, response.Body.String())
			}
			_, loaded := a.pluginRunningInstances.Load("SystemProcessexception")
			if loaded != tc.loaded {
				t.Fatalf("running=%v want=%v", loaded, tc.loaded)
			}
			raw, e := os.ReadFile(counter)
			if e != nil || string(raw) != strconv.Itoa(tc.calls) {
				t.Fatalf("actual init count %s %v", raw, e)
			}
			in, e := a.pluginStore.Installation(ctx, "SystemProcessexception")
			if e != nil || !in.Installed {
				t.Fatal("original partial installation was rolled back", e)
			}
			if _, e := a.pluginStore.Config(ctx, in.InstanceID); e != nil {
				t.Fatal("original copied config was rolled back", e)
			}
		})
	}
}

func TestOriginalCloneExceptionSDKFixture(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--" && i+2 < len(os.Args) {
			path, fail := os.Args[i+1], ","+os.Args[i+2]+","
			manifest, handle, _ := builtin.Lookup("SystemProcess")
			wrapped := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
				if method == "invoke" {
					old, _ := os.ReadFile(path)
					n, _ := strconv.Atoi(string(old))
					n++
					if e := os.WriteFile(path, []byte(strconv.Itoa(n)), 0600); e != nil {
						return nil, e
					}
					if strings.Contains(fail, ","+strconv.Itoa(n)+",") {
						return nil, errors.New("synthetic original init exception")
					}
				}
				return handle(ctx, method, raw, host)
			}
			if plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, wrapped) != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
}
