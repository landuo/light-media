package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins/artifacts"
)

// Runs only in an actual disposable Docker container. Never creates a fake
// /.dockerenv on a host or reaches a user's runtime/storage directory.
func verifyOwnedProductionDockerCodeRecovery(t *testing.T, dir, repository string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	if !originalPluginDockerMode() {
		t.Fatal("Docker lifecycle acceptance requires an actual Docker container")
	}
	const id = ownedStandardFixtureID
	clone := id + "dockerbackup"
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": []string{}, "plugin_local_repo_paths": []string{}, "plugin_auto_reload": false})
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": id, "repo_url": repository + "/version-one/index", "force": true})
	success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "key": "backup-source"})
	success("POST", "/api/plugins/"+id+"/clone", map[string]string{"suffix": "DockerBackup"})
	success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "key": "backup-clone"})
	runtimeRoot, backupRoot := filepath.Join(dir, "plugin-packages"), filepath.Join(dir, "plugin-code-backup")
	installed, err := artifacts.ReadInstalled(backupRoot, id, validatePluginArtifactContract)
	if err != nil || installed.Manifest.Version != "1.0" {
		t.Fatal("Docker install did not immediately refresh persistent code backup", err)
	}
	type snapshot struct {
		Nonce         int
		Version       string
		InitCount     int `json:"init_count"`
		Config, Value json.RawMessage
	}
	read := func(identity, marker string) snapshot {
		t.Helper()
		var value snapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+identity+"/echo", nil, 200), &value) != nil || value.Nonce <= 0 || value.Version != "1.0" || !bytes.Contains(value.Config, []byte(marker)) || !bytes.Equal(value.Value, []byte(`"`+marker+`"`)) {
			t.Fatal("Docker actual SDK/config/KV mismatch", identity)
		}
		return value
	}
	sourceBefore, cloneBefore := read(id, "backup-source"), read(clone, "backup-clone")
	stop()
	backupFiles := map[string]os.FileInfo{}
	for _, identity := range []string{id, clone} {
		code, err := artifacts.ReadInstalled(backupRoot, identity, validatePluginArtifactContract)
		if err != nil || code.Manifest.Version != "1.0" {
			t.Fatal("Docker shutdown did not back up actual code", identity, err)
		}
		backupFiles[identity], _ = os.Stat(code.ExecutablePath)
		if err = os.RemoveAll(filepath.Join(runtimeRoot, identity)); err != nil {
			t.Fatal(err)
		}
	}
	start()
	for identity, marker := range map[string]string{id: "backup-source", clone: "backup-clone"} {
		value := read(identity, marker)
		if value.InitCount != 1 || value.Nonce == sourceBefore.Nonce || value.Nonce == cloneBefore.Nonce {
			t.Fatal("Docker restored object did not start new independent worker", identity)
		}
		code, err := artifacts.ReadInstalled(runtimeRoot, identity, validatePluginArtifactContract)
		if err != nil {
			t.Fatal("Docker did not restore code before manager initialization", err)
		}
		liveFile, _ := os.Stat(code.ExecutablePath)
		if os.SameFile(liveFile, backupFiles[identity]) {
			t.Fatal("restored live executable shares backup inode")
		}
	}
	if _, err := os.Stat(backupRoot); !os.IsNotExist(err) {
		t.Fatal("Docker startup did not consume persistent backup", err)
	}
	stop()
	corrupt, err := artifacts.ReadInstalled(backupRoot, clone, validatePluginArtifactContract)
	if err != nil {
		t.Fatal("fresh shutdown snapshot missing", err)
	}
	if os.WriteFile(corrupt.ExecutablePath, []byte("owned corrupt backup"), 0700) != nil || os.RemoveAll(filepath.Join(runtimeRoot, clone)) != nil {
		t.Fatal("prepare owned corrupt snapshot")
	}
	start()
	read(id, "backup-source")
	request("GET", "/api/plugins/"+clone+"/api/echo", nil, 404)
	if !bytes.Contains(request("GET", "/api/logs?module=plugins&limit=100", nil, 200), []byte("code_restore_failed")) {
		t.Fatal("invalid Docker backup failure not reported")
	}
	if _, err := os.Stat(backupRoot); !os.IsNotExist(err) {
		t.Fatal("failed Docker restore did not consume snapshot", err)
	}
	success("DELETE", "/api/plugins/"+id, nil)
	success("DELETE", "/api/plugins/"+clone, nil)
	t.Log("actual Docker: immediate install backup, shutdown source/clone backup, missing runtime code restored before init; independent SDKs/config/KV retained; startup consumes snapshots, corrupt clone rejected without hiding valid source")
}
