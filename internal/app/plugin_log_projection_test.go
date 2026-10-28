package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type pluginLogSourceFiles struct {
	Defaults struct {
		MaxMiB  int    `json:"LOG_MAX_FILE_SIZE"`
		Backups int    `json:"LOG_BACKUP_COUNT"`
		Format  string `json:"LOG_FILE_FORMAT"`
	}
	Limit    int              `json:"download_limit"`
	MTimes   map[string]int64 `json:"files_mtimes"`
	Selected []string         `json:"selected_names"`
	Plain    string           `json:"plain_current"`
	ZIP      []struct {
		Name        string
		Mode        uint32
		Compression uint16
		Epoch       int64 `json:"epoch_seconds"`
	} `json:"zip_metadata"`
	ZIPInvalid []struct {
		Year  int
		Error string
	} `json:"zip_invalid_years"`
	Rotation []struct {
		Name              string
		Size              int64
		Characters, Bytes int
		Rotates           bool
	} `json:"rotation"`
}

func pluginProjectionSource(t *testing.T) pluginLogSourceFiles {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/standard-plugin-logs/file-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref pluginLogSourceFiles
	if json.Unmarshal(raw, &ref) != nil || ref.Limit != 10 || ref.Defaults.MaxMiB != 5 || ref.Defaults.Backups != 10 {
		t.Fatal("fixed logging oracle changed")
	}
	return ref
}

func TestPluginLogsProjectionEmptyInstanceReturnsSource404(t *testing.T) {
	a, cookie := pluginLogsFixture(t, 0)
	for _, suffix := range []string{"", "/stream", "/file", "/download"} {
		r := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs"+suffix, "", cookie, "")
		if r.Code != 404 {
			t.Fatal("no log file became successful empty history/archive", suffix, r.Code)
		}
	}
}
func TestPluginLogsProjectionSourceSelectionKeepsIndependentZipEntries(t *testing.T) {
	ref := pluginProjectionSource(t)
	a, cookie := pluginLogsFixture(t, 0)
	dir := filepath.Join(a.Dir, "logs", "plugins")
	for original, modified := range ref.MTimes {
		name := strings.ReplaceAll(original, "owned.log", "one.log")
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		at := time.Unix(modified, 0)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	response := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs/download", "", cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for index, file := range archive.File {
		parts := strings.Split(file.Name, "/")
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "one-logs-") {
			t.Fatal("source ZIP layout changed", file.Name)
		}
		got = append(got, parts[1])
		if index >= len(ref.ZIP) {
			t.Fatal("fixed ZIP metadata missing")
		}
		metadata := ref.ZIP[index]
		if strings.ReplaceAll(metadata.Name, "owned.log", "one.log") != parts[1] || uint32(file.Mode().Perm()) != metadata.Mode || file.Method != metadata.Compression || file.Modified.Unix() != metadata.Epoch {
			t.Fatal("source ZIP metadata changed", parts[1], file.Mode().Perm(), file.Method, file.Modified, metadata)
		}
		f, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(f)
		f.Close()
		if err != nil || string(raw) != parts[1]+"\n" {
			t.Fatal("independent retained file content changed", file.Name)
		}
	}
	expected := make([]string, len(ref.Selected))
	for i, v := range ref.Selected {
		expected[i] = strings.ReplaceAll(v, "owned.log", "one.log")
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("source current/mtime/order/limit differs", got, expected)
	}
	plain := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs/file", "", cookie, "")
	if plain.Code != 200 || plain.Body.String() != strings.ReplaceAll(ref.Plain, "owned.log", "one.log") {
		t.Fatal("full text included backups or changed physical reversal", plain.Code, plain.Body.String())
	}
	if len(ref.ZIPInvalid) != 2 {
		t.Fatal("fixed invalid ZIP timestamp cases missing")
	}
	for _, sample := range ref.ZIPInvalid {
		stamp := time.Date(sample.Year, 1, 1, 0, 0, 0, 0, time.Local)
		if err := os.Chtimes(filepath.Join(dir, "one.log"), stamp, stamp); err != nil {
			t.Fatal(err)
		}
		response := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs/download", "", cookie, "")
		if response.Code == 200 {
			t.Fatal("source unsupported ZIP timestamp became a successful archive", sample.Year, sample.Error)
		}
	}
}
func TestPluginLogsProjectionRolloverSourceThresholdAndTenBackups(t *testing.T) {
	ref := pluginProjectionSource(t)
	a := newTestApp(t)
	dir := filepath.Join(a.Dir, "logs", "plugins")
	entry := func(id string) []byte {
		raw, _ := json.Marshal(map[string]any{"plugin_instance_id": id, "module": "plugins", "message": "中文界", "level": "INFO", "time": now()})
		return raw
	}
	for _, sample := range ref.Rotation {
		t.Run(sample.Name, func(t *testing.T) {
			id := "Boundary" + sample.Name
			path := filepath.Join(dir, strings.ToLower(id)+".log")
			f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.Truncate(sample.Size); err != nil {
				t.Fatal(err)
			}
			f.Close()
			a.logs.fileMu.Lock()
			err = a.logs.writePluginProjection(entry(id))
			a.logs.fileMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(path + ".1")
			if (err == nil) != sample.Rotates {
				t.Fatal("fixed stdlib rollover boundary changed", sample.Name, err)
			}
		})
	}
	path := filepath.Join(dir, "backups.log")
	for i := 0; i < 12; i++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Truncate(pluginProjectionMaxBytes - 1); err != nil {
			t.Fatal(err)
		}
		f.Close()
		a.logs.fileMu.Lock()
		err = a.logs.writePluginProjection(entry("Backups"))
		a.logs.fileMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i <= 10; i++ {
		name := path
		if i > 0 {
			name += fmt.Sprint(".", i)
		}
		if _, err := os.Stat(name); err != nil {
			t.Fatal("fixed backup retained file missing", name, err)
		}
	}
	if _, err := os.Stat(path + ".11"); !os.IsNotExist(err) {
		t.Fatal("source ten-backup bound exceeded", err)
	}
}
func TestPluginLogsProjectionLegacyReplayIsIdempotentAndRecoversInterruptedReplay(t *testing.T) {
	a, _ := pluginLogsFixture(t, 2)
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	one := filepath.Join(dir, "logs", "plugins", "one.log")
	before, err := os.ReadFile(one)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(filepath.Join(dir, "logs", "plugins")); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, "logs", ".plugin-projection-v1")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "logs", "plugins"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(one, []byte("owned interrupted partial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pending, _ := json.Marshal([]string{"one.log"})
	if err = os.WriteFile(filepath.Join(dir, "logs", ".plugin-projection-v1.pending"), pending, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		reopened, err := New(Options{DataDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(one)
		if err != nil || !bytes.Equal(after, before) {
			t.Fatal("legacy replay duplicated/lost source rows", i, err, string(after))
		}
		if _, err = os.Stat(filepath.Join(dir, "logs", ".plugin-projection-v1.pending")); !os.IsNotExist(err) {
			t.Fatal("replay receipt was not consumed")
		}
		if err = reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestPluginLogsProjectionSymlinkCannotWriteOrExportForeignFile(t *testing.T) {
	a, cookie := pluginLogsFixture(t, 0)
	outside := filepath.Join(t.TempDir(), "owned-foreign")
	if err := os.WriteFile(outside, []byte("owned sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.Dir, "logs", "plugins", "one.log")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	a.writeLog(map[string]any{"plugin_instance_id": "one", "level": "INFO", "module": "plugins", "message": "must not cross link"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !a.logs.flush(ctx) {
		t.Fatal("writer did not drain")
	}
	raw, err := os.ReadFile(outside)
	if err != nil || string(raw) != "owned sentinel" {
		t.Fatal("projection followed foreign link")
	}
	for _, suffix := range []string{"/file", "/download", "/stream"} {
		response := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs"+suffix, "", cookie, "")
		if response.Code != 404 {
			t.Fatal("unsafe projection exported", suffix, response.Code)
		}
	}
}

func TestPluginLogsProjectionFIFOFailureNeverBecomesSuccessfulLiveLine(t *testing.T) {
	a, cookie := pluginLogsFixture(t, 1)
	response := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs", "", cookie, "")
	if response.Code != 200 {
		t.Fatal("initial physical log unavailable")
	}
	after := a.logs.health().Sequence
	path := filepath.Join(a.Dir, "logs", "plugins", "one.log")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	a.writeLog(map[string]any{"plugin_instance_id": "one", "level": "INFO", "module": "plugins", "message": "owned failed physical write"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !a.logs.flush(ctx) || a.logs.health().WriteError == "" {
		t.Fatal("nonregular projection blocked the sole writer or hid its error")
	}
	for _, suffix := range []string{"", "/file", "/download", "/stream"} {
		if response = callTest(t, a.Handler(), "GET", "/api/plugins/one/logs"+suffix, "", cookie, ""); response.Code != 404 {
			t.Fatal("nonregular current file was readable", suffix, response.Code)
		}
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/logs/stream?plugin_instance_id=one", nil)
	request.Header.Set("Last-Event-ID", fmt.Sprintf("%s:%d", a.logs.boot, after))
	w := httptest.NewRecorder()
	a.streamLogs(w, request)
	if !strings.Contains(w.Body.String(), "plugin_file_write_failed") || strings.Contains(w.Body.String(), "owned failed physical write") {
		t.Fatal("failed disk write was advertised as a successful physical live line")
	}
}

func TestPluginLogsProjectionLegacyCorruptionPersistsExplicitGap(t *testing.T) {
	a, cookie := pluginLogsFixture(t, 1)
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "logs", "plugins")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "logs", ".plugin-projection-v1")); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "logs", "app.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("owned malformed historical row\n" + strings.Repeat("owned oversized historical row", logRecordLimit) + "\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		reopened, err := New(Options{DataDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		page, _, err := reopened.readPluginProjectionHistory(context.Background(), "one")
		if err != nil || !page.Gap || page.Complete || len(page.Entries) != 1 {
			t.Fatal("legacy parse loss was reported as complete", i, err, page.Gap, page.Complete, len(page.Entries))
		}
		response := callTest(t, reopened.Handler(), "GET", "/api/plugins/one/logs/download", "", cookie, "")
		if response.Code != 200 || response.Header().Get("X-Plugin-Log-Legacy-Gap") != "true" {
			t.Fatal("existing recoverable files hid their legacy migration gap", i, response.Code)
		}
		if err = reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
