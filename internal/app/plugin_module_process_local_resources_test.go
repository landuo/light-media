package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins/artifacts"
)

// Fixed plugin.py watcher: ready frontend -> sync only; backend source -> sync
// then reload. This gate never calls scan or replaces a successful SDK response.
func verifyOwnedProductionResourceWatcher(t *testing.T, dir string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	root := filepath.Join(dir, "owned-resource-publications")
	versions := filepath.Join(dir, "owned-resource-versions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, build := range []string{"one", "two", "three", "backend2"} {
		binary := os.Getenv("OWNED_FRONTEND_PROCESS_FIXTURE_BINARY")
		if build == "backend2" {
			binary = os.Getenv("OWNED_FRONTEND_PROCESS_FIXTURE_BINARY_V2")
		}
		index, payload := ownedProcessFrontendBuild(t, binary, build)
		index.Releases[0].Artifacts[0].URL = "package-" + index.Releases[0].Artifacts[0].SHA256 + ".zip"
		folder := filepath.Join(versions, build)
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(index)
		if os.WriteFile(filepath.Join(folder, index.Releases[0].Artifacts[0].URL), payload, 0600) != nil || os.WriteFile(filepath.Join(folder, artifacts.IndexName), raw, 0600) != nil {
			t.Fatal("prepare owned resource build")
		}
	}
	publish := func(build string, corrupt bool) {
		t.Helper()
		folder := filepath.Join(versions, build)
		raw, err := os.ReadFile(filepath.Join(folder, artifacts.IndexName))
		var index artifacts.Index
		if err != nil || json.Unmarshal(raw, &index) != nil {
			t.Fatal("owned resource declaration missing", err)
		}
		name := index.Releases[0].Artifacts[0].URL
		payload, err := os.ReadFile(filepath.Join(folder, name))
		if err != nil {
			t.Fatal(err)
		}
		if corrupt {
			payload = []byte("owned incomplete frontend build")
		}
		if os.WriteFile(filepath.Join(root, name+".pending"), payload, 0600) != nil || os.Rename(filepath.Join(root, name+".pending"), filepath.Join(root, name)) != nil || os.WriteFile(filepath.Join(root, artifacts.IndexName+".pending"), raw, 0600) != nil || os.Rename(filepath.Join(root, artifacts.IndexName+".pending"), filepath.Join(root, artifacts.IndexName)) != nil {
			t.Fatal("atomic owned resource publication failed")
		}
	}
	type observation struct {
		Nonce           int
		InitCount       int  `json:"init_count"`
		CwdOK           bool `json:"cwd_ok"`
		Version, Marker string
	}
	read := func(id string) observation {
		t.Helper()
		var value observation
		if json.Unmarshal(request("GET", "/api/plugins/"+id+"/api/status", nil, 200), &value) != nil || value.Nonce <= 0 || !value.CwdOK || value.InitCount < 1 {
			t.Fatal("actual frontend worker or cwd unavailable", id)
		}
		return value
	}
	descriptor := func(id string) originalFrontendDescriptor {
		t.Helper()
		var response struct{ Data originalFrontendDescriptor }
		if json.Unmarshal(request("GET", "/api/plugins/"+id+"/frontend", nil, 200), &response) != nil || response.Data.PackageSHA256 == "" {
			t.Fatal("actual frontend descriptor missing")
		}
		return response.Data
	}
	waitHash := func(id, previous string) originalFrontendDescriptor {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			var response struct{ Data originalFrontendDescriptor }
			_ = json.Unmarshal(request("GET", "/api/plugins/"+id+"/frontend", nil, 0), &response)
			if response.Data.PackageSHA256 != "" && response.Data.PackageSHA256 != previous {
				return response.Data
			}
			if time.Now().After(deadline) {
				t.Fatal("real ticker did not publish verified frontend resources")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	const id = originalFrontendFixtureID
	clone := id + "resources"
	repository, err := artifacts.LocalRepositoryURL(id, root)
	if err != nil {
		t.Fatal(err)
	}
	publish("one", false)
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": []string{}, "plugin_local_repo_paths": []string{root}, "plugin_auto_reload": os.Getenv("CLONE_FRONTEND_PROCESS_DEV") != "true"})
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": id, "repo_url": repository, "force": true})
	success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "marker": "resource-source"})
	success("POST", "/api/plugins/"+id+"/clone", map[string]string{"suffix": "Resources"})
	success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "marker": "resource-clone"})
	checkClass := func(instance string, d originalFrontendDescriptor, marker string) {
		t.Helper()
		assetRoot := "/api" + strings.TrimSuffix(d.EntryPath, "remoteEntry.js")
		js := string(request("GET", assetRoot+"chunk.js", nil, 200))
		if !strings.Contains(js, "plugin/"+instance+"/status") || !strings.Contains(js, "const id='OwnedFront'+'endFixture'") || !strings.Contains(js, "Owned Vue "+marker) {
			t.Fatal("published class must preserve literal/current identity and runtime source", instance, marker)
		}
		css := string(request("GET", assetRoot+strings.ToLower(instance)+"-theme.css", nil, 200))
		if !strings.Contains(css, "--"+strings.ToLower(instance)+"-class:17px") {
			t.Fatal("published class CSS/file identity absent", instance)
		}
	}
	before, copyBefore := read(id), read(clone)
	old, copyOld := descriptor(id), descriptor(clone)
	checkClass(id, old, "one")
	checkClass(clone, copyOld, "one")
	time.Sleep(1250 * time.Millisecond) // establish real watcher baseline
	publish("two", false)
	next := waitHash(id, old.PackageSHA256)
	checkClass(id, next, "two")
	checkClass(clone, copyOld, "one")
	if read(id) != before || read(clone) != copyBefore || descriptor(clone).PackageSHA256 != copyOld.PackageSHA256 {
		t.Fatal("frontend resource sync restarted worker, config or clone")
	}
	request("GET", "/api"+old.EntryPath, nil, 409)
	chunk := strings.TrimSuffix(next.EntryPath, "remoteEntry.js") + "chunk.js"
	if !bytes.Contains(request("GET", "/api"+chunk, nil, 200), []byte("Owned Vue two")) {
		t.Fatal("new resource bytes unavailable")
	}
	copyChunk := strings.TrimSuffix(copyOld.EntryPath, "remoteEntry.js") + "chunk.js"
	if !bytes.Contains(request("GET", "/api"+copyChunk, nil, 200), []byte("Owned Vue one")) {
		t.Fatal("clone resource bytes changed")
	}
	t.Logf("real frontend-only ticker: source pid=%d init=%d cwd=%t; clone pid=%d retained", before.Nonce, before.InitCount, before.CwdOK, copyBefore.Nonce)
	publish("three", true)
	deadline := time.Now().Add(8 * time.Second)
	for !bytes.Contains(request("GET", "/api/logs?module=plugins&limit=100", nil, 200), []byte("local_reload_failed")) {
		if time.Now().After(deadline) {
			t.Fatal("incomplete frontend publication not reported")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if descriptor(id).PackageSHA256 != next.PackageSHA256 || read(id) != before {
		t.Fatal("invalid frontend publication changed live package or process")
	}
	publish("three", false)
	third := waitHash(id, next.PackageSHA256)
	checkClass(id, third, "three")
	if read(id) != before || read(clone) != copyBefore {
		t.Fatal("valid resource recovery restarted worker")
	}
	publish("backend2", false)
	newCode := waitHash(id, third.PackageSHA256)
	checkClass(id, newCode, "backend2")
	updated := read(id)
	if updated.Nonce == before.Nonce || updated.Version != "2.0" || updated.InitCount != 1 || updated.Marker != before.Marker || read(clone) != copyBefore {
		t.Fatal("changed actual backend did not reload independently", updated)
	}
	stop()
	start()
	deadline = time.Now().Add(10 * time.Second)
	for {
		var cold observation
		_ = json.Unmarshal(request("GET", "/api/plugins/"+id+"/api/status", nil, 0), &cold)
		if cold.Nonce > 0 && cold.Nonce != updated.Nonce && cold.Version == "2.0" {
			if cold.InitCount != 1 || !cold.CwdOK || cold.Marker != before.Marker || descriptor(id).PackageSHA256 != newCode.PackageSHA256 {
				t.Fatal("cold frontend recovery lost package or config")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cold actual frontend SDK did not recover")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cold := read(clone); cold.Nonce == copyBefore.Nonce || cold.Version != "1.0" || cold.Marker != copyBefore.Marker || descriptor(clone).PackageSHA256 != copyOld.PackageSHA256 {
		t.Fatal("cold clone resource isolation failed")
	}
	checkClass(id, descriptor(id), "backend2")
	checkClass(clone, descriptor(clone), "one")
	// Source class updates may target an authenticated clone by its exact class
	// ID. A fresh exact-ID Go executable corresponds to that new Python class;
	// its frontend is already authored for the clone and must not get copycopy.
	if exactBinary := os.Getenv("OWNED_FRONTEND_EXACT_PROCESS_FIXTURE_BINARY"); exactBinary != "" {
		exactRoot := filepath.Join(dir, "owned-exact-clone-publications")
		if err := os.Mkdir(exactRoot, 0700); err != nil {
			t.Fatal(err)
		}
		exactRepository, err := artifacts.LocalRepositoryURL(clone, exactRoot)
		if err != nil {
			t.Fatal(err)
		}
		publishExact := func(marker string, corrupt bool) {
			t.Helper()
			index, payload := ownedProcessFrontendBuild(t, exactBinary, marker)
			if index.PluginID != clone || index.CurrentVersion != "2.0" {
				t.Fatal("exact frontend fixture does not match authenticated clone")
			}
			name := "package-" + index.Releases[0].Artifacts[0].SHA256 + ".zip"
			index.Releases[0].Artifacts[0].URL = name
			if corrupt {
				payload = []byte("owned incomplete exact clone publication")
			}
			raw, _ := json.Marshal(index)
			if os.WriteFile(filepath.Join(exactRoot, name+".pending"), payload, 0600) != nil || os.Rename(filepath.Join(exactRoot, name+".pending"), filepath.Join(exactRoot, name)) != nil || os.WriteFile(filepath.Join(exactRoot, artifacts.IndexName+".pending"), raw, 0600) != nil || os.Rename(filepath.Join(exactRoot, artifacts.IndexName+".pending"), filepath.Join(exactRoot, artifacts.IndexName)) != nil {
				t.Fatal("exact clone publication failed")
			}
		}
		publishExact("exact-one", false)
		success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": []string{}, "plugin_local_repo_paths": []string{root, exactRoot}, "plugin_auto_reload": os.Getenv("CLONE_FRONTEND_PROCESS_DEV") != "true"})
		priorSource, priorClone := read(id), read(clone)
		success("POST", "/api/plugins/install", map[string]any{"plugin_id": clone, "repo_url": exactRepository, "force": true})
		exact := read(clone)
		exactCode := descriptor(clone)
		if exact.Nonce == priorClone.Nonce || exact.Version != "2.0" || exact.InitCount != 1 || exact.Marker != priorClone.Marker || read(id) != priorSource {
			t.Fatal("exact clone update lost isolated code/config/source")
		}
		checkClass(clone, exactCode, "exact-one")
		request("GET", "/api"+copyOld.EntryPath, nil, 409)
		time.Sleep(1250 * time.Millisecond)
		publishExact("exact-two", false)
		exactTwo := waitHash(clone, exactCode.PackageSHA256)
		if read(clone) != exact || read(id) != priorSource {
			t.Fatal("exact clone frontend-only update restarted SDK or source")
		}
		checkClass(clone, exactTwo, "exact-two")
		// An invalid immutable publication must keep the running process/graph.
		failureCount := bytes.Count(request("GET", "/api/logs?module=plugins&limit=100", nil, 200), []byte("local_reload_failed"))
		publishExact("exact-bad", true)
		failureDeadline := time.Now().Add(8 * time.Second)
		for bytes.Count(request("GET", "/api/logs?module=plugins&limit=100", nil, 200), []byte("local_reload_failed")) <= failureCount {
			if time.Now().After(failureDeadline) {
				t.Fatal("exact clone invalid publication not reported by actual ticker")
			}
			time.Sleep(50 * time.Millisecond)
		}
		if read(clone) != exact || descriptor(clone).PackageSHA256 != exactTwo.PackageSHA256 {
			t.Fatal("invalid exact clone graph replaced valid runtime")
		}
		publishExact("exact-three", false)
		exactThree := waitHash(clone, exactTwo.PackageSHA256)
		checkClass(clone, exactThree, "exact-three")
		if read(clone) != exact || read(id) != priorSource {
			t.Fatal("exact clone resource recovery restarted object")
		}
		stop()
		start()
		coldExact := read(clone)
		if coldExact.Nonce == exact.Nonce || coldExact.InitCount != 1 || coldExact.Version != "2.0" || coldExact.Marker != exact.Marker || descriptor(clone).PackageSHA256 != exactThree.PackageSHA256 {
			t.Fatal("exact derived clone cold state lost")
		}
		checkClass(clone, descriptor(clone), "exact-three")
		t.Logf("actual exact clone update/frontend-only/failure/recovery/cold: pid%d→%d→%d, literal/CSS/file identity retained; DEV=%t", priorClone.Nonce, exact.Nonce, coldExact.Nonce, os.Getenv("CLONE_FRONTEND_PROCESS_DEV") == "true")
	}
	success("DELETE", "/api/plugins/"+id, nil)
	success("DELETE", "/api/plugins/"+clone, nil)
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": []string{}, "plugin_local_repo_paths": []string{}, "plugin_auto_reload": false})
	publish("one", false) // clean source for subsequent actual browser install
	t.Log("frontend resources, bad publication/recovery, backend change, cold restart and clone isolation verified in actual server")
}
