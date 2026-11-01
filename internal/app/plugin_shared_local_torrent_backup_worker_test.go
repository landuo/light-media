package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

type ownedLocalBackupWorkerResult struct {
	PID, Invocation, Requested, Completed, Succeeded, Failed int
	InitCount                                                int   `json:"init_count"`
	HadPrevious                                              bool  `json:"had_previous"`
	StaleDenied                                              *bool `json:"stale_denied"`
	PriorReleasedHandleDenied                                *bool `json:"prior_released_handle_denied"`
	OK                                                       bool
	ReassembledBytes                                         int64 `json:"reassembled_bytes"`
	Results                                                  []struct {
		Index     int
		OK        bool
		Error     string
		ErrorCode string `json:"error_code"`
		Result    *struct {
			Skipped, Temporary bool
			Length             *int
			SHA256             *string
			SmallBase64        *string `json:"small_base64"`
		}
		HostCalls struct {
			Total, Prepare, Read, Release int
			MaterialPrepare               int `json:"material_prepare"`
		} `json:"host_calls"`
		Grant *struct {
			Contract, Capability, Direction, Kind, SHA256 string
			Total                                         int64
			ChunkBytes                                    int `json:"chunk_bytes"`
		} `json:"material_grant"`
	}
}

func ownedLocalBackupWorkerInstall(t *testing.T, a *App, cookie *http.Cookie) string {
	t.Helper()
	environment, id := "OWNED_LOCAL_TORRENT_BACKUP_WORKER", "OwnedLocalTorrentBackup"
	index, payload := originalMarketOwnedIndex(t, environment)
	manifest := index.Releases[0].Manifest
	if manifest.Validate() != nil || manifest.PluginID != id || len(manifest.APIs) != 1 || manifest.APIs[0].Path != "/backup" || !reflect.DeepEqual(manifest.APIs[0].Methods, []string{"POST"}) {
		t.Fatal("actual backup fixture manifest differs")
	}
	if !reflect.DeepEqual(manifest.Capabilities, []string{plugins.LocalTorrentBackupCapability}) {
		t.Fatal("backup capability manifest differs")
	}
	index.Releases[0].Artifacts[0].HostContract = pluginResourcesSDKContract
	var indexHits, artifactHits, unexpected atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			unexpected.Add(1)
			http.Error(w, "owned read-only artifact", 405)
			return
		}
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	})
	// Fix the owned address family before starting; a bind refusal is fatal.
	// No IPv6 retry, proxy change, forwarding, CONNECT or external upstream.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	index.Releases[0].Artifacts[0].URL = "http://" + server.Listener.Addr().String() + "/worker"
	server.Start()
	t.Cleanup(func() {
		server.Close()
		if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
			t.Errorf("owned artifact acquisition count %d/%d/%d", indexHits.Load(), artifactHits.Load(), unexpected.Load())
		}
	})
	body, _ := json.Marshal(map[string]string{"plugin_id": id, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	loaded, ok := a.pluginRunningInstances.Load(id)
	if !ok {
		t.Fatal("installed backup worker absent")
	}
	object := loaded.(*originalPluginInstance)
	if !object.external || object.hostContract != pluginResourcesSDKContract {
		t.Fatal("not an actual resources-contract worker")
	}
	installation, err := a.pluginStore.Installation(context.Background(), id)
	if err != nil || !installation.Installed {
		t.Fatal("real SQLite installation absent", err)
	}
	return id
}

func ownedLocalBackupWorkerFixture(t *testing.T) (*App, *http.Cookie, string, string) {
	t.Helper()
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	root := t.TempDir()
	cfg := a.Settings()
	cfg.PluginMarkets = []string{}
	cfg.PluginLocalRepoPaths = []string{}
	cfg.PluginAutoReload = false
	cfg.AllowedRoots = []string{root}
	cfg.Roots = []Root{}
	disabled := false
	// Disabled selections have no endpoint/credentials. Preparing bytes must
	// inspect host kind only, never instantiate/contact a real downloader.
	cfg.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-backup-qb", Kind: "qb"}, Enabled: &disabled}, {Config: download.Config{ID: "owned-backup-tr", Kind: "transmission"}, Enabled: &disabled}}
	if err := a.saveSettings(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	id := ownedLocalBackupWorkerInstall(t, a, cookie)
	return a, cookie, id, root
}

func ownedLocalBackupWorkerWrite(t *testing.T, root, name, hash string, torrent, resume []byte) plugins.LocalTorrentBackupRequest {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, hash+".torrent"), torrent, 0600); err != nil {
		t.Fatal(err)
	}
	if resume != nil {
		if err := os.WriteFile(filepath.Join(directory, hash+".fastresume"), resume, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return plugins.LocalTorrentBackupRequest{DownloaderID: "owned-backup-qb", Directory: directory, Hash: hash}
}

func ownedLocalBackupWorkerQuery(t *testing.T, a *App, cookie *http.Cookie, id string, requests ...plugins.LocalTorrentBackupRequest) ownedLocalBackupWorkerResult {
	t.Helper()
	body, err := json.Marshal(map[string]any{"requests": requests, "continue_on_error": true, "probe_previous": true})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/"+id+"/backup", string(body), cookie, "")
	if response.Code != 200 {
		t.Fatalf("actual backup HTTP %d: %s", response.Code, response.Body)
	}
	var result ownedLocalBackupWorkerResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.PID <= 0 || result.PID == os.Getpid() || result.InitCount < 1 || result.Requested != len(requests) || result.Completed != len(requests) || len(result.Results) != len(requests) {
		t.Fatalf("actual worker observation absent: %+v %v", result, err)
	}
	if result.HadPrevious && (result.StaleDenied == nil || !*result.StaleDenied) {
		t.Fatal("known-successful old callback was not denied", result)
	}
	// Current fixture negotiates material/v1: no legacy handle was allocated
	// or released. Do not manufacture a released-handle security observation.
	// Explicit unnegotiated SDK roots retain the legacy read/release checks.
	if result.PriorReleasedHandleDenied != nil {
		t.Fatal("material invocation unexpectedly reported a legacy released handle", result)
	}
	entries, err := os.ReadDir(filepath.Join(a.Dir, "temp", "plugin-material-spool"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("completed invocation retained owner transport spools", len(entries))
	}
	return result
}

func ownedLocalBackupWorkerBytes(t *testing.T, result ownedLocalBackupWorkerResult, index int, want []byte, temporary bool) {
	t.Helper()
	attempt := result.Results[index]
	digest := sha256.Sum256(want)
	if !attempt.OK || attempt.Result == nil || attempt.Result.Skipped || attempt.Result.Temporary != temporary || attempt.Result.Length == nil || *attempt.Result.Length != len(want) || attempt.Result.SHA256 == nil || *attempt.Result.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("actual full SDK bytes differ: %+v", attempt)
	}
	if len(want) <= 4096 {
		if attempt.Result.SmallBase64 == nil || *attempt.Result.SmallBase64 != base64.StdEncoding.EncodeToString(want) {
			t.Fatal("actual worker small bytes differ", attempt)
		}
	} else if attempt.Result.SmallBase64 != nil {
		t.Fatal("large content unexpectedly exposed as fixture base64")
	}
	if attempt.HostCalls.Prepare != 1 || attempt.HostCalls.MaterialPrepare != 1 || attempt.HostCalls.Read != 0 || attempt.HostCalls.Release != 0 || attempt.HostCalls.Total != 1 {
		t.Fatalf("actual material prepare versus ordinary callback count differs: %+v", attempt.HostCalls)
	}
	g := attempt.Grant
	if g == nil || g.Contract != plugins.MaterialContract || g.Capability != plugins.LocalTorrentBackupCapability || g.Direction != string(plugins.MaterialToWorker) || g.Kind != "bytes" || g.Total != int64(len(want)) || g.SHA256 != hex.EncodeToString(digest[:]) || g.ChunkBytes < 1 || g.ChunkBytes > plugins.MaterialFrameBytes {
		t.Fatalf("actual material grant summary differs: %+v", g)
	}
}

func ownedLocalBackupWorkerSkip(t *testing.T, result ownedLocalBackupWorkerResult) {
	t.Helper()
	attempt := result.Results[0]
	if !result.OK || !attempt.OK || attempt.Result == nil || !attempt.Result.Skipped || attempt.Result.Temporary || attempt.Result.Length != nil || attempt.Result.SHA256 != nil || attempt.Result.SmallBase64 != nil || attempt.HostCalls.Prepare != 1 || attempt.HostCalls.MaterialPrepare != 1 || attempt.HostCalls.Read != 0 || attempt.HostCalls.Release != 0 || attempt.HostCalls.Total != 1 || attempt.Grant != nil {
		t.Fatalf("source failed-item skip became bytes or host error: %+v", result)
	}
}

func ownedLocalBackupWorkerWarm(t *testing.T, before, after ownedLocalBackupWorkerResult) {
	t.Helper()
	if before.PID != after.PID || before.InitCount != after.InitCount || after.Invocation != before.Invocation+1 || !after.HadPrevious || after.StaleDenied == nil || !*after.StaleDenied {
		t.Fatalf("warm identity/init/scope changed: %+v -> %+v", before, after)
	}
}

func TestSharedLocalTorrentBackupActualWorkerRecoveryAndScopes(t *testing.T) {
	a, cookie, id, root := ownedLocalBackupWorkerFixture(t)
	original := ownedLocalBackupSource(t, "qbittorrent-qbittorrent-autoFalse-skipFalse")
	rebuilt := ownedLocalBackupSource(t, "restore-trackers")
	direct := ownedLocalBackupWorkerWrite(t, root, "original", ownedLocalBackupHash, original.torrent, nil)
	restore := ownedLocalBackupWorkerWrite(t, root, "rebuilt", ownedLocalBackupHash, rebuilt.torrent, rebuilt.resume)
	first := ownedLocalBackupWorkerQuery(t, a, cookie, id, direct, restore)
	if !first.OK || first.HadPrevious || first.Invocation != 1 || first.ReassembledBytes != int64(len(original.want)+len(rebuilt.want)) {
		t.Fatal("fresh real source materials", first)
	}
	ownedLocalBackupWorkerBytes(t, first, 0, original.want, false)
	ownedLocalBackupWorkerBytes(t, first, 1, rebuilt.want, true)
	missing := ownedLocalBackupWorkerWrite(t, root, "missing-resume", ownedLocalBackupHash, rebuilt.torrent, nil)
	skipped := ownedLocalBackupWorkerQuery(t, a, cookie, id, missing)
	ownedLocalBackupWorkerSkip(t, skipped)
	ownedLocalBackupWorkerWarm(t, first, skipped)
	// Repair only our own file; no permissions, inode, path-escape or symlink probe.
	if err := os.WriteFile(filepath.Join(missing.Directory, missing.Hash+".fastresume"), rebuilt.resume, 0600); err != nil {
		t.Fatal(err)
	}
	recovered := ownedLocalBackupWorkerQuery(t, a, cookie, id, missing)
	ownedLocalBackupWorkerBytes(t, recovered, 0, rebuilt.want, true)
	ownedLocalBackupWorkerWarm(t, skipped, recovered)
	// Exact invalid-torrent bytes from the historical source case, not an IO
	// denial. The source handles this decoder failure as WARN/failed-item skip.
	invalid := ownedLocalBackupWorkerWrite(t, root, "invalid-torrent", ownedLocalBackupHash, []byte("garbage"), nil)
	bad := ownedLocalBackupWorkerQuery(t, a, cookie, id, invalid)
	ownedLocalBackupWorkerSkip(t, bad)
	ownedLocalBackupWorkerWarm(t, recovered, bad)
	if err := os.WriteFile(filepath.Join(invalid.Directory, invalid.Hash+".torrent"), original.torrent, 0600); err != nil {
		t.Fatal(err)
	}
	repaired := ownedLocalBackupWorkerQuery(t, a, cookie, id, invalid)
	ownedLocalBackupWorkerBytes(t, repaired, 0, original.want, false)
	ownedLocalBackupWorkerWarm(t, bad, repaired)
	unknown := direct
	unknown.DownloaderID = "owned-not-configured"
	failure := ownedLocalBackupWorkerQuery(t, a, cookie, id, unknown, direct)
	if failure.OK || failure.Failed != 1 || failure.Succeeded != 1 || failure.Results[0].OK || failure.Results[0].ErrorCode != "host_operation_failed" || failure.Results[0].Result != nil {
		t.Fatalf("config refusal became source skip/success: %+v", failure)
	}
	ownedLocalBackupWorkerBytes(t, failure, 1, original.want, false)
	ownedLocalBackupWorkerWarm(t, repaired, failure)
	warm := ownedLocalBackupWorkerQuery(t, a, cookie, id, restore)
	ownedLocalBackupWorkerBytes(t, warm, 0, rebuilt.want, true)
	ownedLocalBackupWorkerWarm(t, failure, warm)
	ownedLocalBackupAssertBytes(t, filepath.Join(direct.Directory, direct.Hash+".torrent"), original.torrent)
	ownedLocalBackupAssertBytes(t, filepath.Join(restore.Directory, restore.Hash+".torrent"), rebuilt.torrent)
	ownedLocalBackupAssertBytes(t, filepath.Join(restore.Directory, restore.Hash+".fastresume"), rebuilt.resume)
	ownedLocalBackupAssertBytes(t, filepath.Join(a.Dir, "temp", "plugin-torrent-backup", ownedLocalBackupHash+".torrent"), rebuilt.want)
	logs, err := a.queryLogs(context.Background(), map[string]string{"module": "plugins", "limit": "500"})
	if err != nil || logs.Gap {
		t.Fatal("actual source diagnostics unavailable", err)
	}
	missingLog, badLog := false, false
	for _, entry := range logs.Entries {
		if entry["event"] == "local_torrent_backup" && entry["level"] == "WARN" {
			text, _ := entry["message"].(string)
			missingLog = missingLog || strings.Contains(text, "fastresume文件不存在")
			badLog = badLog || strings.Contains(text, "解析种子文件")
		}
	}
	if !missingLog || !badLog {
		t.Fatal("source skip diagnostics missing", missingLog, badLog)
	}
}

func TestSharedLocalTorrentBackupActualWorkerPagedAndCold(t *testing.T) {
	a, cookie, id, root := ownedLocalBackupWorkerFixture(t)
	padding := bytes.Repeat([]byte{'P'}, plugins.TorrentMaterialChunkBytes+31)
	large := append([]byte("d8:announce32:https://tracker.invalid/announce7:comment"+strconv.Itoa(len(padding))+":"), padding...)
	large = append(large, []byte("4:infod6:lengthi1e4:name5:ownedee")...)
	// Synthetic paging vector only; source-byte fidelity is tested separately.
	paged := ownedLocalBackupWorkerWrite(t, root, "paged", "MixedCase-Large", large, nil)
	rebuilt := ownedLocalBackupSource(t, "restore-trackers")
	restore := ownedLocalBackupWorkerWrite(t, root, "rebuilt", ownedLocalBackupHash, rebuilt.torrent, rebuilt.resume)
	first := ownedLocalBackupWorkerQuery(t, a, cookie, id, paged, restore)
	if !first.OK || first.ReassembledBytes != int64(len(large)+len(rebuilt.want)) {
		t.Fatal("actual paged reconstruction failed", first)
	}
	ownedLocalBackupWorkerBytes(t, first, 0, large, false)
	ownedLocalBackupWorkerBytes(t, first, 1, rebuilt.want, true)
	empty := ownedLocalBackupWorkerWrite(t, root, "tr-empty", "EmptyCase", []byte{}, nil)
	empty.DownloaderID = "owned-backup-tr"
	emptyResult := ownedLocalBackupWorkerQuery(t, a, cookie, id, empty)
	ownedLocalBackupWorkerBytes(t, emptyResult, 0, []byte{}, false)
	ownedLocalBackupWorkerWarm(t, first, emptyResult)
	temp := filepath.Join(a.Dir, "temp", "plugin-torrent-backup", ownedLocalBackupHash+".torrent")
	ownedLocalBackupAssertBytes(t, temp, rebuilt.want)
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// Close/release does not delete retained source-style temporary material.
	ownedLocalBackupAssertBytes(t, temp, rebuilt.want)
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	login := callTest(t, cold.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	var coldCookie *http.Cookie
	for _, value := range login.Result().Cookies() {
		if value.Name == "lm_session" {
			coldCookie = value
		}
	}
	if login.Code != 200 || coldCookie == nil {
		t.Fatal("cold normal login failed")
	}
	reopened := ownedLocalBackupWorkerQuery(t, cold, coldCookie, id, paged, restore)
	if !reopened.OK || reopened.PID == first.PID || reopened.HadPrevious || reopened.Invocation != 1 {
		t.Fatal("cold new worker/configuration not recovered", reopened)
	}
	ownedLocalBackupWorkerBytes(t, reopened, 0, large, false)
	ownedLocalBackupWorkerBytes(t, reopened, 1, rebuilt.want, true)
	ownedLocalBackupAssertBytes(t, temp, rebuilt.want)
	ownedLocalBackupAssertBytes(t, filepath.Join(paged.Directory, paged.Hash+".torrent"), large)
	ownedLocalBackupAssertBytes(t, filepath.Join(restore.Directory, restore.Hash+".torrent"), rebuilt.torrent)
	ownedLocalBackupAssertBytes(t, filepath.Join(restore.Directory, restore.Hash+".fastresume"), rebuilt.resume)
}
