package app

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"light-media/internal/plugins/artifacts"
)

const autoBackupFixtureID = "AutoBackup"

func TestAutoBackupInstalledAMD64Package(t *testing.T) {
	bundle := os.Getenv("SOURCE_AUTO_BACKUP_PACKAGE_DIR")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the AutoBackup candidate ZIP in the owned Linux amd64 fixture")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, cookie := retainedPluginAuthorityFixture(t)
	custom := filepath.Join(t.TempDir(), "new", "backups")
	settings := a.Settings()
	settings.AllowedRoots = nil
	if err := a.saveSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	const category = "movie:\n  OwnedCategory:\n"
	if err := os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte(category), 0600); err != nil {
		t.Fatal(err)
	}
	const snapshotMarker = "owned SQLite transaction included"
	if err := a.Store.Put(t.Context(), "backup-fixture", "marker", snapshotMarker); err != nil {
		t.Fatal(err)
	}
	a.Start(t.Context())
	installAutoBackupCandidate(t, a, cookie, bundle)
	const path = "/api/plugins/" + autoBackupFixtureID
	handler := a.Handler()
	form := callTest(t, handler, http.MethodGet, path+"/form", "", cookie, "")
	if form.Code != http.StatusOK || !bytes.Contains(form.Body.Bytes(), []byte(`"component":"VCronField"`)) ||
		!bytes.Contains(form.Body.Bytes(), []byte(`"model":"onlyonce"`)) || bytes.Contains(form.Body.Bytes(), []byte(`webdav_enabled`)) {
		t.Fatal("installed native form lost the source cron or local-only controls")
	}

	services := func(enabled bool) {
		t.Helper()
		response := callTest(t, handler, http.MethodGet, "/api/plugins/services", "", cookie, "")
		var view struct{ Data []PluginServiceInfo }
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil {
			t.Fatal("could not inspect registered scheduler entries", response.Code)
		}
		found := false
		for _, item := range view.Data {
			if item.InstanceID == autoBackupFixtureID && item.ServiceID == autoBackupFixtureID && item.Name == "自动备份定时服务" {
				found = item.NextRun != nil && item.CanRun
			}
		}
		if found != enabled {
			t.Fatal("source cron registration did not follow enabled and cron")
		}
	}
	save := func(config map[string]any) {
		t.Helper()
		response := callTest(t, handler, http.MethodPut, path, string(mustCleanSeedJSON(config)), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}

	defaultDir, err := a.originalBaseDataPath(t.Context(), autoBackupFixtureID)
	if err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(defaultDir, "bk_owned_old.zip")
	if err := os.WriteFile(older, []byte("owned previous archive"), 0600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(defaultDir, "owned-keep.txt")
	if err := os.WriteFile(unrelated, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	save(map[string]any{"enabled": false, "onlyonce": true, "cron": "17 5 * * *", "cnt": "1"})
	services(false)
	config, present, err := a.readOriginalBaseConfigForInit(t.Context(), autoBackupFixtureID)
	if err != nil || !present || !bytes.Contains(config, []byte(`"onlyonce":false`)) {
		t.Fatal("one-shot was not cleared from saved config at initialization", err)
	}
	var original string
	privateAwait(t, "disabled plugin one-shot produced a ZIP and pruned the oldest bk entry", func() bool {
		archives, _ := filepath.Glob(filepath.Join(defaultDir, "bk_*.zip"))
		if len(archives) == 1 && archives[0] != older {
			original = archives[0]
		}
		return original != ""
	})
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("retention removed an entry outside the source bk prefix", err)
	}
	assertAutoBackupArchive(t, a, original, category, snapshotMarker)
	t.Log("disabled plugin one-shot cleared its stored flag, created a consistent owned ZIP, and retained only the newest bk entry")

	save(map[string]any{"enabled": true, "onlyonce": false, "cron": "17 5 * * *", "cnt": "", "back_path": custom})
	services(true)
	callAPI := func(key, header string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path+"/api/backup?apikey="+url.QueryEscape(key), nil)
		if header != "" {
			request.Header.Set("X-API-KEY", header)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := callAPI("invalid-owned-key", ""); response.Code != http.StatusUnauthorized {
		t.Fatal("plugin API accepted an invalid dependency key", response.Code)
	}
	if response := callAPI("invalid-owned-key", syntheticPluginAPIKey); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"success":false`) || !strings.Contains(response.Body.String(), "API密钥错误") {
		t.Fatal("plugin API lost its handler-level credential failure", response.Code)
	}
	if _, err := os.Stat(custom); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("authentication failure created the backup directory", err)
	}
	if response := callAPI(syntheticPluginAPIKey, ""); response.Code != http.StatusBadGateway {
		t.Fatal("valid API key granted an unapproved local path", response.Code)
	}
	if _, err := os.Stat(custom); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unapproved destination was created", err)
	}
	settings.AllowedRoots = []string{filepath.Dir(filepath.Dir(custom))}
	if err := a.saveSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	response := callAPI(syntheticPluginAPIKey, "")
	var tuple []json.RawMessage
	var message string
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &tuple) != nil || len(tuple) != 2 ||
		string(tuple[0]) != "true" || json.Unmarshal(tuple[1], &message) != nil || !strings.HasPrefix(message, "备份完成 备份文件 ") {
		t.Fatalf("authorized plugin API did not complete the original success tuple: %d %s", response.Code, response.Body)
	}
	archive := strings.TrimPrefix(message, "备份完成 备份文件 ")
	if filepath.Dir(archive) != custom {
		t.Fatal("backup did not use the configured, authorized local directory")
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatal("configured directory backup affected the default directory", err)
	}
	apiArchive, err := os.Stat(archive)
	if err != nil || apiArchive.Mode().Perm() != 0600 {
		t.Fatal("configured destination archive was not created privately", err)
	}
	t.Log("preserved both API authentication layers and wrote the valid API backup only after the configured directory was granted")

	service := callTest(t, handler, http.MethodPost, "/api/plugins/services/run",
		`{"instance_id":"AutoBackup","service_id":"AutoBackup"}`, cookie, "")
	assertOriginalRegistrySuccess(t, service.Code, service.Body.Bytes())
	privateAwait(t, "registered public cron service published its backup", func() bool {
		archives, _ := filepath.Glob(filepath.Join(custom, "bk_*.zip"))
		for _, name := range archives {
			if info, err := os.Stat(name); err == nil && !os.SameFile(apiArchive, info) {
				return true
			}
		}
		return false
	})
	t.Log("public cron service registered and produced an archive through the host's manual scheduler entry")
}

func assertAutoBackupArchive(t *testing.T, a *App, name, category, marker string) {
	t.Helper()
	if !regexp.MustCompile(`^bk_[0-9]{14}\.zip$`).MatchString(filepath.Base(name)) {
		t.Fatal("archive name does not follow the installed source")
	}
	if info, err := os.Stat(name); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("archive permissions are not private", err)
	}
	reader, err := zip.OpenReader(name)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	members := map[string][]byte{}
	for _, file := range reader.File {
		entry, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(entry)
		entry.Close()
		if err != nil {
			t.Fatal(err)
		}
		members[file.Name] = contents
	}
	key, err := os.ReadFile(filepath.Join(a.Dir, "secret.key"))
	if err != nil || len(reader.File) != 3 || string(members["category.yaml"]) != category || !bytes.Equal(members["secret.key"], key) {
		t.Fatal("archive lacks the native equivalent of the SQLite/config payload", err)
	}
	name = filepath.Join(t.TempDir(), "snapshot.db")
	if err := os.WriteFile(name, members["light-media.db"], 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: name, RawQuery: "mode=ro"}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var check string
	if err := snapshot.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatal("archive SQLite snapshot is not consistent", err)
	}
	var value []byte
	if err := snapshot.QueryRow(`SELECT payload FROM records WHERE kind='backup-fixture' AND id='marker'`).Scan(&value); err != nil || string(value) != string(mustCleanSeedJSON(marker)) {
		t.Fatal("archive did not include the committed fixture data", err)
	}
}

func installAutoBackupCandidate(t *testing.T, a *App, cookie *http.Cookie, bundle string) {
	t.Helper()
	const archiveName = "AutoBackup-2.1.4-linux-amd64.zip"
	raw, err := os.ReadFile(filepath.Join(bundle, artifacts.IndexName))
	if err != nil {
		t.Fatal(err)
	}
	var index artifacts.RepositoryIndex
	if json.Unmarshal(raw, &index) != nil || index.SchemaVersion != 1 || len(index.Plugins) != 1 ||
		index.Plugins[0].PluginID != autoBackupFixtureID || index.Plugins[0].CurrentVersion != "2.1.4" ||
		len(index.Plugins[0].Releases) != 1 || len(index.Plugins[0].Releases[0].Artifacts) != 1 {
		t.Fatal("unexpected AutoBackup candidate index")
	}
	release := &index.Plugins[0].Releases[0]
	artifact := &release.Artifacts[0]
	archive, err := os.ReadFile(filepath.Join(bundle, archiveName))
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if release.Version != "2.1.4" || release.Manifest.PluginID != autoBackupFixtureID || release.Manifest.HasPage ||
		artifact.Format != "zip" || artifact.Platform != "linux-amd64" || artifact.HostContract != pluginResourcesSDKContract ||
		artifact.Size != int64(len(archive)) || artifact.SHA256 != digest {
		t.Fatal("expected the indexed Linux amd64 AutoBackup ZIP")
	}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + artifacts.IndexName:
			_ = json.NewEncoder(w).Encode(index)
		case "/" + archiveName:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repository.Close)
	artifact.URL = repository.URL + "/" + archiveName
	body := mustCleanSeedJSON(map[string]string{"plugin_id": autoBackupFixtureID, "repo_url": repository.URL + "/" + artifacts.IndexName})
	response := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), autoBackupFixtureID, validatePluginArtifactContract)
	if err != nil || installed.Manifest.Version != "2.1.4" || installed.SHA256 != artifact.ExecutableSHA256 {
		t.Fatal("host did not install the indexed AutoBackup ZIP", err)
	}
	originalBaseSDKLoaded(t, a, autoBackupFixtureID)
	t.Logf("installed candidate: index sha256 %x, ZIP sha256 %s, worker sha256 %s", sha256.Sum256(raw), digest, installed.SHA256)
}
