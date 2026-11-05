package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Source: fixed _PluginBase.get_data_path:257-266, and installed
// TorrentTransfer:785/825/836/844/857 Path exists/read_bytes/write_bytes.
// These assertions concern platform file access, never transfer business logic.
// The backup fixture is synthetic; App/artifact/SDK/filesystem are real.
func TestOriginalHostLocalFilesActualSDKMountAndClone(t *testing.T) {
	binary := os.Getenv("OWNED_LOCAL_FILES_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("actual separately compiled local-files SDK worker required")
	}
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	m := installOwnedResourceProcessWorker(t, a, cookie, binary)
	ctx := context.Background()
	const hash = "0123456789abcdef0123456789abcdef01234567"
	backup := t.TempDir()
	torrent, resume := []byte("d4:infod4:name5:ownede8:announce20:https://invalid.testee"), []byte("d8:trackersll20:https://invalid.testeee")
	for name, content := range map[string][]byte{hash + ".torrent": torrent, hash + ".fastresume": resume} {
		if err := os.WriteFile(filepath.Join(backup, name), content, 0400); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(backup, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(backup, 0700) })
	configure := func(id, path string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"backup_path": path, "hash": hash, "run_once": true})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	type snapshot struct {
		PID         int    `json:"pid"`
		Version     string `json:"version"`
		DataPath    string `json:"data_path"`
		Observation struct {
			Instance   string `json:"instance"`
			TorrentSHA string `json:"torrent_sha"`
			ResumeSHA  string `json:"resume_sha"`
			TempPath   string `json:"temp_path"`
			Error      string `json:"error"`
		} `json:"observation"`
		Config struct {
			RunOnce bool `json:"run_once"`
		} `json:"config"`
	}
	read := func(id string) snapshot {
		t.Helper()
		r := callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+id+"/snapshot", "", cookie, "")
		if r.Code != 200 {
			t.Fatalf("actual API %d %s", r.Code, r.Body.String())
		}
		var o snapshot
		if err := json.Unmarshal(r.Body.Bytes(), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	sha := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	configure(m.PluginID, backup)
	source := read(m.PluginID)
	if source.Observation.Error != "" || source.Observation.Instance != m.PluginID || source.Observation.TorrentSHA != sha(torrent) || source.Observation.ResumeSHA != sha(resume) || source.Config.RunOnce {
		t.Fatalf("actual source file observation %+v", source)
	}
	if got, err := os.ReadFile(source.Observation.TempPath); err != nil || string(got) != string(torrent) {
		t.Fatal("temporary torrent bytes differ", err)
	}
	cloneID := m.PluginID + "files"
	r := callTest(t, a.Handler(), "POST", "/api/plugins/"+m.PluginID+"/clone", `{"suffix":"Files"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	configure(cloneID, backup)
	clone := read(cloneID)
	if clone.PID == source.PID || clone.Observation.Instance != cloneID || clone.Observation.TempPath == source.Observation.TempPath || clone.DataPath != filepath.Join(a.Dir, "plugin-data", cloneID) {
		t.Fatal("clone failed actual code/data-path isolation")
	}
	// A missing read is surfaced by the plugin, while the already saved config
	// and source files remain. No success is inferred from settings HTTP 200.
	configure(cloneID, filepath.Join(backup, "missing"))
	failed := read(cloneID)
	if failed.Observation.Error == "" || failed.Config.RunOnce {
		t.Fatal("missing backup silently passed or single-run flag replays")
	}
	r = callTest(t, a.Handler(), "POST", "/api/v1/plugin/"+cloneID+"/run", "", cookie, "")
	if r.Code != 422 {
		t.Fatalf("expected actual failure API422, got%d", r.Code)
	}
	configure(cloneID, backup)
	r = callTest(t, a.Handler(), "DELETE", "/api/v1/plugin/"+cloneID+"/cleanup", "", cookie, "")
	if r.Code != http.StatusOK {
		t.Fatal("actual clone cleanup failed", r.Code)
	}
	if _, err := os.Stat(clone.Observation.TempPath); !os.IsNotExist(err) {
		t.Fatal("clone temp survived explicit cleanup", err)
	}
	if got, err := os.ReadFile(source.Observation.TempPath); err != nil || string(got) != string(torrent) {
		t.Fatal("clone cleanup touched source temp", err)
	}
	// Close and recover sessions from the same encrypted Store, keeping the
	// same App for deterministic DB inspection. Full CLI restart is separate.
	a.closeOriginalSessions()
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	cold := read(m.PluginID)
	if cold.PID == source.PID || cold.Observation.TorrentSHA != source.Observation.TorrentSHA || cold.Config.RunOnce {
		t.Fatal("worker restart lost persisted result or replayed one-shot")
	}
	for _, id := range []string{cloneID, m.PluginID} {
		r = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+id, "", cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		r = callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+id+"/snapshot", "", cookie, "")
		if r.Code != 404 {
			t.Fatal("uninstalled API remains", id, r.Code)
		}
	}
	for name, want := range map[string][]byte{hash + ".torrent": torrent, hash + ".fastresume": resume} {
		if got, err := os.ReadFile(filepath.Join(backup, name)); err != nil || string(got) != string(want) {
			t.Fatal("backup was modified", name, err)
		}
	}
}
