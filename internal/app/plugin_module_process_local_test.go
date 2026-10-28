package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"light-media/internal/plugins/artifacts"
)

// Production acceptance uses the real App.Start ticker, never calls scan itself.
// Pinned local-source watcher changes the installed primary only; clone code and
// independent data remain owned by the plugin manager. Go uses immutable index
// and executable publication in place of executing changed Python source.
func verifyOwnedProductionLocalWatcher(t *testing.T, dir string, index artifacts.Index, v1, v2 []byte, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func(), setDeveloper func(bool)) {
	t.Helper()
	root := filepath.Join(dir, "owned-local-publications")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	repository, err := artifacts.LocalRepositoryURL(ownedStandardFixtureID, root)
	if err != nil {
		t.Fatal(err)
	}
	var systemRange json.RawMessage
	publish := func(version string, corrupt bool) {
		t.Helper()
		raw, _ := json.Marshal(index)
		var publication artifacts.Index
		if json.Unmarshal(raw, &publication) != nil {
			t.Fatal("cannot copy actual local declaration")
		}
		selected := 0
		payload := v1
		if version == "2.0" {
			selected, payload = 1, v2
		}
		publication.CurrentVersion = version
		publication.SystemVersion = systemRange
		release := publication.Releases[selected]
		name := "worker-" + version
		if corrupt {
			name += "-invalid"
			payload = []byte("owned invalid executable publication")
		}
		release.Artifacts[0].URL = name
		publication.Releases = []artifacts.Release{release}
		if err := os.WriteFile(filepath.Join(root, name+".pending"), payload, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(root, name+".pending"), filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(publication)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, artifacts.IndexName+".pending"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Rename(filepath.Join(root, artifacts.IndexName+".pending"), filepath.Join(root, artifacts.IndexName)); err != nil {
			t.Fatal(err)
		}
	}
	configure := func(enabled bool) {
		success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": []string{}, "plugin_local_repo_paths": []string{root}, "plugin_auto_reload": enabled})
	}
	type snapshot struct {
		Nonce         int
		InitCount     int `json:"init_count"`
		Version       string
		Config, Value json.RawMessage
	}
	read := func(id string) snapshot {
		t.Helper()
		var s snapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/echo", nil, 200), &s) != nil || s.Nonce <= 0 {
			t.Fatal("actual local SDK unavailable")
		}
		return s
	}
	check := func(id, version, key string) snapshot {
		t.Helper()
		s := read(id)
		var config map[string]any
		if s.Version != version || json.Unmarshal(s.Config, &config) != nil || config["key"] != key || !bytes.Equal(s.Value, []byte(`"`+key+`"`)) {
			t.Fatal("local publication lost actual code/config/KV", id, version)
		}
		return s
	}
	wait := func(version string, oldPID int) snapshot {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for {
			var s snapshot
			// reload stops the old SDK before creating the new object. A real
			// transient 502 is observable and must not be confused with success.
			_ = json.Unmarshal(request("GET", "/api/v1/plugin/"+ownedStandardFixtureID+"/echo", nil, 0), &s)
			if s.Nonce > 0 && s.Version == version && s.Nonce != oldPID {
				return check(ownedStandardFixtureID, version, "local-primary")
			}
			if time.Now().After(deadline) {
				t.Fatal("real one-second watcher did not publish expected actual SDK", version)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	publish("1.0", false)
	configure(false)
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": repository, "force": true})
	success("PUT", "/api/plugins/"+ownedStandardFixtureID, map[string]any{"enabled": true, "key": "local-primary"})
	clone := ownedStandardFixtureID + "hotreload"
	success("POST", "/api/plugins/"+ownedStandardFixtureID+"/clone", map[string]string{"suffix": "HotReload"})
	success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "key": "local-clone"})
	before := check(ownedStandardFixtureID, "1.0", "local-primary")
	cloneBefore := check(clone, "1.0", "local-clone")
	publish("2.0", false)
	time.Sleep(1250 * time.Millisecond)
	if check(ownedStandardFixtureID, "1.0", "local-primary").Nonce != before.Nonce {
		t.Fatal("disabled actual watcher replaced worker")
	}
	publish("1.0", false)
	configure(true)
	time.Sleep(1250 * time.Millisecond) // allow the real ticker to establish baseline
	publish("2.0", false)
	updated := wait("2.0", before.Nonce)
	t.Log("actual ticker published v2; source replacement and clone isolation next")
	if check(clone, "1.0", "local-clone").Nonce != cloneBefore.Nonce {
		t.Fatal("primary reload replaced clone code or process")
	}
	publish("1.0", true)
	deadline := time.Now().Add(8 * time.Second)
	for !bytes.Contains(request("GET", "/api/logs?module=plugins&limit=100", nil, 200), []byte("local_reload_failed")) {
		if time.Now().After(deadline) {
			t.Fatal("failed local publication not reported through actual host logs")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if check(ownedStandardFixtureID, "2.0", "local-primary").Nonce != updated.Nonce {
		t.Fatal("bad publication replaced running SDK")
	}
	t.Log("bad local publication reported; prior v2 worker retained")
	publish("1.0", false)
	recovered := wait("1.0", updated.Nonce)
	t.Log("valid local v1 publication recovered before cold restart")
	// Freeze the publication, cold restart, then let the actual ticker establish
	// its own baseline again. Persistent settings and clone isolation must survive.
	stop()
	start()
	cold := wait("1.0", recovered.Nonce)
	if cold.Nonce == recovered.Nonce || cold.InitCount != 1 {
		t.Fatal("cold local SDK process did not initialize once")
	}
	var controls struct {
		Data struct {
			Paths   []string `json:"plugin_local_repo_paths"`
			Enabled bool     `json:"plugin_auto_reload"`
		}
	}
	if json.Unmarshal(request("GET", "/api/plugins/markets", nil, 200), &controls) != nil || len(controls.Data.Paths) != 1 || controls.Data.Paths[0] != root || !controls.Data.Enabled {
		t.Fatal("local developer settings did not survive actual restart")
	}
	var cloneCold snapshot
	deadline = time.Now().Add(8 * time.Second)
	for {
		_ = json.Unmarshal(request("GET", "/api/v1/plugin/"+clone+"/echo", nil, 0), &cloneCold)
		if cloneCold.Nonce > 0 && cloneCold.Nonce != cloneBefore.Nonce && cloneCold.Version == "1.0" {
			cloneCold = check(clone, "1.0", "local-clone")
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual cold clone did not become ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cloneCold.Nonce == cloneBefore.Nonce || cloneCold.Nonce == cold.Nonce {
		t.Fatal("cold clone did not recover independently")
	}
	time.Sleep(1250 * time.Millisecond)
	success("DELETE", "/api/plugins/"+ownedStandardFixtureID, nil)
	publish("2.0", false)
	time.Sleep(1250 * time.Millisecond)
	var installed struct {
		Data []struct {
			ID         string `json:"id"`
			InstanceID string `json:"instance_id"`
		}
	}
	if json.Unmarshal(request("GET", "/api/plugins?state=installed", nil, 200), &installed) != nil {
		t.Fatal("actual installed registry unavailable")
	}
	for _, item := range installed.Data {
		if item.ID == ownedStandardFixtureID || item.InstanceID == ownedStandardFixtureID {
			t.Fatal("watcher reinstalled uninstalled primary")
		}
	}
	if check(clone, "1.0", "local-clone").Nonce != cloneCold.Nonce {
		t.Fatal("uninstalled primary publication changed clone")
	}
	if os.Getenv("PLUGIN_PROCESS_DEV_WATCH_FIXTURE") == "true" {
		configure(false)
		success("POST", "/api/plugins/install", map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": repository, "force": true})
		success("PUT", "/api/plugins/"+ownedStandardFixtureID, map[string]any{"enabled": true, "key": "local-primary"})
		beforeDev := check(ownedStandardFixtureID, "2.0", "local-primary")
		stop()
		setDeveloper(true)
		start()
		devCold := wait("2.0", beforeDev.Nonce)
		if json.Unmarshal(request("GET", "/api/plugins/markets", nil, 200), &controls) != nil || controls.Data.Enabled {
			t.Fatal("DEV trigger was persisted as or depended on auto reload switch")
		}
		devClone := check(clone, "1.0", "local-clone")
		time.Sleep(1250 * time.Millisecond)
		systemRange = json.RawMessage(`">=3"`)
		publish("1.0", false)
		devUpdated := wait("1.0", devCold.Nonce)
		if check(clone, "1.0", "local-clone").Nonce != devClone.Nonce {
			t.Fatal("DEV mismatch synchronization changed clone")
		}
		var manual struct{ Data struct{ Success bool } }
		if json.Unmarshal(request("POST", "/api/plugins/install", map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": repository, "force": true}, 200), &manual) != nil || manual.Data.Success {
			t.Fatal("DEV monitor bypass leaked into real manual install")
		}
		logFailures := func() int {
			t.Helper()
			var page struct {
				Data struct{ Entries []struct{ Event string } }
			}
			if json.Unmarshal(request("GET", "/api/logs?module=plugins&limit=100", nil, 200), &page) != nil {
				t.Fatal("actual DEV logs unavailable")
			}
			count := 0
			for _, entry := range page.Data.Entries {
				if entry.Event == "local_reload_failed" {
					count++
				}
			}
			return count
		}
		failuresBefore := logFailures()
		publish("2.0", true)
		deadline = time.Now().Add(8 * time.Second)
		for logFailures() <= failuresBefore {
			if time.Now().After(deadline) {
				t.Fatal("DEV corrupt SHA publication was not reported")
			}
			time.Sleep(50 * time.Millisecond)
		}
		if check(ownedStandardFixtureID, "1.0", "local-primary").Nonce != devUpdated.Nonce {
			t.Fatal("DEV mode bypassed actual SHA integrity")
		}
		// A cold process with DEV off and auto switch off must stay quiescent;
		// incompatible metadata also prevents startup repair from changing v1.
		stop()
		setDeveloper(false)
		start()
		normalCold := wait("1.0", devUpdated.Nonce)
		request("GET", "/api/plugins/market?details=true", nil, 200)
		systemRange = nil
		publish("2.0", false)
		time.Sleep(1250 * time.Millisecond)
		if check(ownedStandardFixtureID, "1.0", "local-primary").Nonce != normalCold.Nonce {
			t.Fatal("DEV off plus auto off still watched source")
		}
		success("DELETE", "/api/plugins/"+ownedStandardFixtureID, nil)
		t.Log("real DEV=true CLI restart with auto=false triggers local monitor; incompatible system range synchronized only through DEV watcher; manual install still rejects; corrupt SHA retained live PID; clone code/config/KV isolated; DEV=false cold restart plus auto=false stops watching")
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_local_repo_paths": []string{}, "plugin_auto_reload": false})
	t.Log("real Linux App.Start ticker verified: disabled watcher preserves PID; v1→v2 code replacement retains config/KV; bad SHA publication logs failure and preserves live worker; valid downgrade retries; actual cold restart restores local controls/independent clone; uninstalled primary is never reinstalled; owned plugins and local settings cleared")
}
