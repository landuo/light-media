package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/artifacts"
)

const remoteIdentifiersFixtureID = "RemoteIdentifiers"

func TestRemoteIdentifiersInstalledAMD64Package(t *testing.T) {
	bundle := os.Getenv("SOURCE_REMOTE_IDENTIFIERS_PACKAGE_DIR")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the RemoteIdentifiers candidate ZIP in the owned Linux amd64 fixture")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("TZ", "UTC")
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	t.Setenv("no_proxy", "127.0.0.1,localhost")

	const (
		indexPath    = "/owned-index.JSON"
		etherpadPath = "/etherpad/p/owned"
		retryPath    = "/retry"
		publicPath   = "/public"
		userRule     = "OwnedInput => OwnedPrefix"
		privateRule  = "OwnedPrefix => OwnedStage"
		finishRule   = "OwnedStage => OwnedFinal"
		publicRule   = "OwnedPrefix => OwnedPublic"
		namedCount   = 25
	)
	var namedPaths, namedRules []string
	for i := 1; i <= namedCount; i++ {
		namedPaths = append(namedPaths, fmt.Sprintf("/words/%02d", i))
		namedRules = append(namedRules, fmt.Sprintf("# owned list %02d\r", i))
	}
	type visit struct {
		path string
		at   time.Time
	}
	var requestMu sync.Mutex
	var requests []visit
	var retryCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMu.Lock()
		requests = append(requests, visit{path: r.URL.Path, at: time.Now()})
		requestMu.Unlock()
		if r.Method != http.MethodGet {
			t.Error("remote words must use GET")
		}
		authorization := r.Header.Get("Authorization")
		if r.URL.Path == indexPath {
			want := "Basic " + base64.StdEncoding.EncodeToString([]byte("own\xe9d:l\xe9af"))
			if authorization != want {
				t.Error("URL userinfo did not produce source Latin-1 Basic authentication")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		} else if authorization != "" {
			t.Error("index credentials leaked to a separate word list request")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		base := "http://" + r.Host
		switch r.URL.Path {
		case indexPath:
			// Source json.loads keeps the first insertion position of "first",
			// while its final value selects the Etherpad list.
			fmt.Fprintf(w, `{"first":%q,"retry":%q`, base+"/must-not-fetch", base+retryPath)
			for i, path := range namedPaths {
				fmt.Fprintf(w, `,"list%02d":%q`, i+1, base+path)
			}
			fmt.Fprintf(w, `,"first":%q}`, base+etherpadPath)
		case etherpadPath + "/export/txt":
			_, _ = io.WriteString(w, privateRule+"\n\n \n")
		case retryPath:
			if retryCalls.Add(1) == 1 {
				http.Error(w, "owned transient error", http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, finishRule+"\n")
		case publicPath:
			_, _ = io.WriteString(w, publicRule+"\n")
		default:
			i := slices.Index(namedPaths, r.URL.Path)
			if i < 0 {
				t.Error("unexpected owned words request", r.URL.Path)
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, namedRules[i]+"\n")
		}
	}))
	t.Cleanup(peer.Close)
	a, _, _ := importTestApp(t)
	ctx := t.Context()
	if err := a.saveRecognitionWords(ctx, 0, []string{userRule, remoteIdentifiersMarker, "OwnedStale => Removed"}); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	installRemoteIdentifiersCandidate(t, a, cookie, bundle)
	path := "/api/plugins/" + remoteIdentifiersFixtureID

	readForm := func() (string, json.RawMessage, map[string]json.RawMessage) {
		t.Helper()
		response := callTest(t, a.Handler(), http.MethodGet, path+"/form", "", cookie, "")
		var body struct {
			Data struct {
				RenderMode string `json:"render_mode"`
				Conf       json.RawMessage
				Model      map[string]json.RawMessage
			}
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatal("native form could not be read")
		}
		return body.Data.RenderMode, body.Data.Conf, body.Data.Model
	}
	renderMode, conf, defaults := readForm()
	const defaultURLs = "https://raw.githubusercontent.com/Putarku/MoviePilot-Help/main/Words/TV.txt\nhttps://raw.githubusercontent.com/Putarku/MoviePilot-Help/main/Words/anime.txt"
	if renderMode != "vuetify" || !bytes.Contains(conf, []byte(`"component":"VForm"`)) ||
		!bytes.Contains(conf, []byte(`"component":"VTextarea"`)) || !bytes.Contains(conf, []byte(`"model":"file_urls"`)) ||
		string(defaults["enable"]) != "false" || string(defaults["onlyonce"]) != "false" || string(defaults["flitter"]) != "true" ||
		string(defaults["cron"]) != `"30 4 * * *"` || string(defaults["file_urls"]) != string(mustCleanSeedJSON(defaultURLs)) {
		t.Fatal("native form did not expose the installed source defaults")
	}

	save := func(address, cron string, once, filter bool) {
		t.Helper()
		body := mustCleanSeedJSON(map[string]any{"enable": true, "onlyonce": once, "flitter": filter, "cron": cron, "file_urls": address})
		response := callTest(t, a.Handler(), http.MethodPut, path, string(body), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
	const serviceID = remoteIdentifiersFixtureID + "_" + remoteIdentifiersFixtureID
	service := func() *PluginServiceInfo {
		t.Helper()
		items, err := a.originalPublicServicesResult(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == serviceID {
				return &item
			}
		}
		return nil
	}
	awaitWords := func(revision int64, want []string, match string, timeout time.Duration) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			n, got, err := a.recognitionWords(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if n >= revision {
				if n != revision || !slices.Equal(got, want) {
					t.Fatal("remote refresh published a partial or out-of-order recognition snapshot")
				}
				// The matcher is swapped immediately after the database CAS.
				if a.prepareRecognitionTitle("OwnedInput") == match && a.prepareRecognitionTitle("OwnedStale") == "OwnedStale" {
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("remote refresh did not publish the expected snapshot and active matcher")
	}

	privateURL, err := url.Parse(peer.URL + indexPath)
	if err != nil {
		t.Fatal(err)
	}
	privateURL.User = url.UserPassword("ownéd", "léaf")
	started := time.Now()
	save(privateURL.String(), "invalid cron", true, true)
	a.pluginOriginalDirectJobs.mu.Lock()
	_, registered := a.pluginOriginalDirectJobs.jobs[originalServiceKey{remoteIdentifiersFixtureID, remoteIdentifiersFixtureID}]
	a.pluginOriginalDirectJobs.mu.Unlock()
	if registered || service() != nil {
		t.Fatal("invalid public crontab unexpectedly registered")
	}
	_, _, model := readForm()
	raw, _, err := a.readOriginalBaseConfigForInit(ctx, remoteIdentifiersFixtureID)
	var persisted struct {
		OnlyOnce *bool  `json:"onlyonce"`
		FileURLs string `json:"file_urls"`
	}
	if err != nil || json.Unmarshal(raw, &persisted) != nil || persisted.OnlyOnce == nil || *persisted.OnlyOnce ||
		persisted.FileURLs != privateURL.String() || string(model["onlyonce"]) != "false" ||
		string(model["file_urls"]) != string(mustCleanSeedJSON(privateIdentifiersURLs)) {
		t.Fatal("private init did not clear onlyonce or retain the masked source URL")
	}

	expected := append([]string{userRule, remoteIdentifiersMarker, privateRule, " ", finishRule}, namedRules...)
	awaitWords(2, expected, "OwnedFinal", 20*time.Second)
	requestMu.Lock()
	gotRequests := slices.Clone(requests)
	requestMu.Unlock()
	attempt := []string{indexPath, etherpadPath + "/export/txt", retryPath}
	wantPaths := append(append(slices.Clone(attempt), attempt...), namedPaths...)
	var gotPaths []string
	for _, request := range gotRequests {
		gotPaths = append(gotPaths, request.path)
	}
	if !slices.Equal(gotPaths, wantPaths) {
		t.Fatalf("expected source-wide replay then ordered continuation, got %v", gotPaths)
	}
	initialDelay := gotRequests[0].at.Sub(started)
	retryDelay := gotRequests[len(attempt)].at.Sub(gotRequests[len(attempt)-1].at)
	if initialDelay < 3*time.Second || retryDelay < 5*time.Second {
		t.Fatal("private scheduler did not preserve source initial and retry delays", initialDelay, retryDelay)
	}
	t.Logf("private onlyonce: %d owned GETs, initial wait %v, full retry wait %v, %d persisted rules",
		len(gotPaths), initialDelay, retryDelay, len(expected))

	save(peer.URL+publicPath, "30 4 * * *", false, false)
	if item := service(); item == nil || item.Name != "获取远端识别词" || item.NextRun == nil || !item.CanRun {
		t.Fatal("valid cron did not register the source public service")
	}
	run := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/services/run",
		`{"instance_id":"RemoteIdentifiers","service_id":"RemoteIdentifiers"}`, cookie, "")
	if run.Code != http.StatusOK || !strings.Contains(run.Body.String(), `"accepted":true`) {
		t.Fatal("public scheduler did not accept the real worker service")
	}
	awaitWords(3, []string{userRule, remoteIdentifiersMarker, publicRule, ""}, "OwnedPublic", 10*time.Second)
	requestMu.Lock()
	gotRequests = slices.Clone(requests)
	requestMu.Unlock()
	if len(gotRequests) != len(wantPaths)+1 || gotRequests[len(gotRequests)-1].path != publicPath {
		t.Fatal("public service did not perform exactly one owned GET")
	}
	t.Log("public refresh: source cron registered, one owned GET, trailing empty rule and live matcher verified")
}

func installRemoteIdentifiersCandidate(t *testing.T, a *App, cookie *http.Cookie, bundle string) {
	t.Helper()
	const (
		archiveName = "RemoteIdentifiers-2.4.1-linux-amd64.zip"
		indexSHA    = "464509c982648cd063eb3ec1d06ee77515a89e3f87129c22737dea56e1a7c7ba"
		archiveSHA  = "67373bed226f519678e0cbca0a02c24713538a63ab9b5f1897e5bfed9b4c62cb"
		workerSHA   = "28e4721a5cf98d026080a316f438ab0660c20fca807d7cedd254302446857ecb"
	)
	raw, err := os.ReadFile(filepath.Join(bundle, artifacts.IndexName))
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != indexSHA {
		t.Fatal("expected the exact owned RemoteIdentifiers index", err)
	}
	var index artifacts.RepositoryIndex
	if json.Unmarshal(raw, &index) != nil || index.SchemaVersion != 1 || len(index.Plugins) != 1 ||
		index.Plugins[0].PluginID != remoteIdentifiersFixtureID || index.Plugins[0].CurrentVersion != "2.4.1" ||
		len(index.Plugins[0].Releases) != 1 || len(index.Plugins[0].Releases[0].Artifacts) != 1 {
		t.Fatal("unexpected RemoteIdentifiers candidate index")
	}
	release := &index.Plugins[0].Releases[0]
	artifact := &release.Artifacts[0]
	archive, err := os.ReadFile(filepath.Join(bundle, archiveName))
	if err != nil {
		t.Fatal(err)
	}
	archiveDigest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if release.Version != "2.4.1" || release.Manifest.PluginID != remoteIdentifiersFixtureID ||
		!release.Manifest.ManagedPrivateScheduler || !release.Manifest.ManagedPublicServices ||
		artifact.Format != "zip" || artifact.Platform != "linux-amd64" || artifact.HostContract != pluginResourcesSDKContract ||
		artifact.Size != int64(len(archive)) || artifact.SHA256 != archiveSHA || archiveDigest != archiveSHA ||
		artifact.ExecutableSHA256 != workerSHA {
		t.Fatal("expected the indexed linux/amd64 RemoteIdentifiers ZIP")
	}
	var downloads atomic.Int32
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + artifacts.IndexName:
			_ = json.NewEncoder(w).Encode(index)
		case "/" + archiveName:
			downloads.Add(1)
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repository.Close)
	// Only the download location changes for the owned loopback repository.
	artifact.URL = repository.URL + "/" + archiveName
	body := mustCleanSeedJSON(map[string]string{"plugin_id": remoteIdentifiersFixtureID, "repo_url": repository.URL + "/" + artifacts.IndexName})
	response := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), remoteIdentifiersFixtureID, validatePluginArtifactContract)
	if err != nil || installed.Manifest.Version != "2.4.1" || installed.SHA256 != artifact.ExecutableSHA256 ||
		len(installed.Files) != len(artifact.Files) || downloads.Load() != 1 {
		t.Fatal("host did not install the indexed candidate ZIP", err)
	}
	originalBaseSDKLoaded(t, a, remoteIdentifiersFixtureID)
	t.Logf("installed candidate: index sha256 %s, ZIP sha256 %s, worker sha256 %s", indexSHA, archiveSHA, workerSHA)
}
