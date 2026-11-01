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
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

type localBackupMaterialAttempt struct {
	OK        bool
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
type localBackupMaterialObservation struct {
	PID, Invocation, Requested, Completed, Succeeded, Failed int
	InitCount                                                int   `json:"init_count"`
	HadPrevious                                              bool  `json:"had_previous"`
	StaleDenied                                              *bool `json:"stale_denied"`
	PriorReleasedHandleDenied                                *bool `json:"prior_released_handle_denied"`
	OK                                                       bool
	ReassembledBytes                                         int64 `json:"reassembled_bytes"`
	Results                                                  []localBackupMaterialAttempt
}

func localBackupMaterialInstall(t *testing.T, a *App, cookie *http.Cookie) string {
	t.Helper()
	const id = "OwnedLocalTorrentBackup"
	index, payload := originalMarketOwnedIndex(t, "OWNED_LOCAL_TORRENT_BACKUP_WORKER")
	manifest := index.Releases[0].Manifest
	if manifest.Validate() != nil || manifest.PluginID != id || len(manifest.APIs) != 1 || manifest.APIs[0].Path != "/backup" || !reflect.DeepEqual(manifest.Capabilities, []string{plugins.LocalTorrentBackupCapability}) {
		t.Fatal("actual material worker manifest mismatch")
	}
	index.Releases[0].Artifacts[0].HostContract = pluginResourcesSDKContract
	var indexHits, artifactHits, unexpected atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			unexpected.Add(1)
			http.Error(w, "owned artifact GET only", 405)
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
	// Explicit IPv4 before any start. A rejected bind is fatal, never an IPv6 or
	// alternate-route retry. No forwarder, CONNECT handler or external upstream.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	index.Releases[0].Artifacts[0].URL = "http://" + listener.Addr().String() + "/worker"
	server.Start()
	t.Cleanup(func() {
		server.Close()
		if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
			t.Errorf("actual owned artifact counts %d/%d/%d", indexHits.Load(), artifactHits.Load(), unexpected.Load())
		}
	})
	body, _ := json.Marshal(map[string]string{"plugin_id": id, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	loaded, ok := a.pluginRunningInstances.Load(id)
	if !ok {
		t.Fatal("installed material worker missing")
	}
	object := loaded.(*originalPluginInstance)
	if !object.external || object.hostContract != pluginResourcesSDKContract {
		t.Fatal("worker did not use actual resources host contract")
	}
	installation, err := a.pluginStore.Installation(context.Background(), id)
	if err != nil || !installation.Installed {
		t.Fatal("SQLite installation missing", err)
	}
	return id
}
func localBackupMaterialQuery(t *testing.T, a *App, cookie *http.Cookie, id string, requests ...plugins.LocalTorrentBackupRequest) localBackupMaterialObservation {
	t.Helper()
	body, err := json.Marshal(map[string]any{"requests": requests, "continue_on_error": true, "probe_previous": true})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/"+id+"/backup", string(body), cookie, "")
	if response.Code != 200 {
		t.Fatalf("material worker HTTP %d: %s", response.Code, response.Body)
	}
	var result localBackupMaterialObservation
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.PID <= 0 || result.PID == os.Getpid() || result.InitCount < 1 || result.Requested != len(requests) || result.Completed != len(requests) || len(result.Results) != len(requests) {
		t.Fatalf("missing actual SDK worker observation: %+v %v", result, err)
	}
	if result.HadPrevious && (result.StaleDenied == nil || !*result.StaleDenied) {
		t.Fatal("previous actual HostCaller admitted new material", result)
	}
	// This path never allocates a legacy byte handle; do not relabel it a legacy
	// release proof or assert old prepare/read/release counts for private frames.
	if result.PriorReleasedHandleDenied != nil {
		t.Fatal("negotiated path unexpectedly reported a legacy release")
	}
	entries, err := os.ReadDir(filepath.Join(a.Dir, "temp", "plugin-material-spool"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("invocation termination retained owner transport spools", len(entries))
	}
	return result
}
func localBackupMaterialBytes(t *testing.T, result localBackupMaterialObservation, index int, want []byte, temporary bool) {
	t.Helper()
	attempt := result.Results[index]
	sum := sha256.Sum256(want)
	sha := hex.EncodeToString(sum[:])
	if !attempt.OK || attempt.Result == nil || attempt.Result.Skipped || attempt.Result.Temporary != temporary || attempt.Result.Length == nil || *attempt.Result.Length != len(want) || attempt.Result.SHA256 == nil || *attempt.Result.SHA256 != sha {
		t.Fatalf("complete actual SDK bytes differ: %+v", attempt)
	}
	if len(want) <= 4096 && (attempt.Result.SmallBase64 == nil || *attempt.Result.SmallBase64 != base64.StdEncoding.EncodeToString(want)) {
		t.Fatal("actual nonnil small/empty SDK bytes differ")
	}
	if len(want) > 4096 && attempt.Result.SmallBase64 != nil {
		t.Fatal("fixture exposed large material")
	}
	c := attempt.HostCalls
	if c.Total != 1 || c.Prepare != 1 || c.MaterialPrepare != 1 || c.Read != 0 || c.Release != 0 {
		t.Fatalf("new prepare versus ordinary callback counts differ: %+v", c)
	}
	g := attempt.Grant
	if g == nil || g.Contract != plugins.MaterialContract || g.Capability != plugins.LocalTorrentBackupCapability || g.Direction != string(plugins.MaterialToWorker) || g.Kind != "bytes" || g.Total != int64(len(want)) || g.SHA256 != sha || g.ChunkBytes < 1 || g.ChunkBytes > plugins.MaterialFrameBytes {
		t.Fatalf("actual material grant metadata differs: %+v", g)
	}
}
func localBackupMaterialWarm(t *testing.T, before, after localBackupMaterialObservation) {
	t.Helper()
	if before.PID != after.PID || before.InitCount != after.InitCount || after.Invocation != before.Invocation+1 || !after.HadPrevious || after.StaleDenied == nil || !*after.StaleDenied {
		t.Fatal("same-worker current/expired invocation identity differs")
	}
}

func TestSharedLocalBackupMaterialLargeWorkerAndCold(t *testing.T) {
	a := newTestApp(t)
	// Existing helper isolates unrelated statistic-center traffic; it does not
	// change downloader/material roots, network proxy, CA or material authority.
	disableResetFixtureStatistics(t, a)
	root := t.TempDir()
	cfg := a.Settings()
	cfg.PluginMarkets = []string{}
	cfg.PluginLocalRepoPaths = []string{}
	cfg.PluginAutoReload = false
	cfg.AllowedRoots = []string{root}
	cfg.Roots = []Root{}
	disabled := false
	cfg.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-backup-qb", Kind: "qb"}, Enabled: &disabled}, {Config: download.Config{ID: "owned-backup-tr", Kind: "transmission"}, Enabled: &disabled}}
	if err := a.saveSettings(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	id := localBackupMaterialInstall(t, a, cookie)
	sample := localBackupLargeSource(t)
	restore := ownedLocalBackupWorkerWrite(t, root, "source-large", ownedLocalBackupHash, sample.torrent, sample.resume)
	paths := []string{filepath.Join(restore.Directory, restore.Hash+".torrent"), filepath.Join(restore.Directory, restore.Hash+".fastresume")}
	before := make([]os.FileInfo, len(paths))
	for i, path := range paths {
		var err error
		before[i], err = os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	first := localBackupMaterialQuery(t, a, cookie, id, restore)
	if !first.OK || first.HadPrevious || first.Invocation != 1 || first.ReassembledBytes != int64(len(sample.want)) {
		t.Fatal("first large negotiated SDK transfer failed", first)
	}
	localBackupMaterialBytes(t, first, 0, sample.want, true)
	rebuilt := filepath.Join(a.Dir, "temp", "plugin-torrent-backup", ownedLocalBackupHash+".torrent")
	ownedLocalBackupAssertBytes(t, rebuilt, sample.want)
	// The old exact source missing-resume branch remains a skip, not an empty
	// material grant. Only owned fixture files are created; no OS-failure probe.
	old := ownedLocalBackupSource(t, "restore-trackers")
	missing := ownedLocalBackupWorkerWrite(t, root, "missing-resume", ownedLocalBackupHash, old.torrent, nil)
	skipped := localBackupMaterialQuery(t, a, cookie, id, missing)
	localBackupMaterialWarm(t, first, skipped)
	attempt := skipped.Results[0]
	c := attempt.HostCalls
	if !skipped.OK || !attempt.OK || attempt.Result == nil || !attempt.Result.Skipped || attempt.Result.Temporary || attempt.Result.Length != nil || attempt.Grant != nil || c.Total != 1 || c.Prepare != 1 || c.MaterialPrepare != 1 || c.Read != 0 || c.Release != 0 {
		t.Fatal("source skip allocated material or changed new control shape", attempt)
	}
	unknown := restore
	unknown.DownloaderID = "owned-not-configured"
	empty := ownedLocalBackupWorkerWrite(t, root, "tr-empty", "EmptyCase", []byte{}, nil)
	empty.DownloaderID = "owned-backup-tr"
	recovered := localBackupMaterialQuery(t, a, cookie, id, unknown, restore, empty)
	localBackupMaterialWarm(t, skipped, recovered)
	denied := recovered.Results[0]
	if recovered.OK || recovered.Failed != 1 || recovered.Succeeded != 2 || denied.OK || denied.ErrorCode != "host_operation_failed" || denied.Result != nil || denied.Grant != nil || denied.HostCalls.Total != 1 || denied.HostCalls.MaterialPrepare != 1 {
		t.Fatal("current config denial became source skip or fake grant", recovered)
	}
	localBackupMaterialBytes(t, recovered, 1, sample.want, true)
	localBackupMaterialBytes(t, recovered, 2, []byte{}, false)
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	ownedLocalBackupAssertBytes(t, rebuilt, sample.want)
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
	reopened := localBackupMaterialQuery(t, cold, coldCookie, id, restore)
	if !reopened.OK || reopened.PID == first.PID || reopened.HadPrevious || reopened.Invocation != 1 {
		t.Fatal("cold installed worker identity not renewed", reopened)
	}
	localBackupMaterialBytes(t, reopened, 0, sample.want, true)
	ownedLocalBackupAssertBytes(t, rebuilt, sample.want)
	for i, path := range paths {
		after, err := os.Stat(path)
		if err != nil || after.Size() != before[i].Size() || !after.ModTime().Equal(before[i].ModTime()) {
			t.Fatal("backup bytes/mtime changed across calls or cold process", err)
		}
	}
	for i, data := range [][]byte{sample.torrent, sample.resume} {
		actual, err := os.ReadFile(paths[i])
		if err != nil || !bytes.Equal(actual, data) {
			t.Fatal("backup bytes changed", err)
		}
	}
}
