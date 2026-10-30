package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// Compare the same completed physical file facts through actual source
// MetaInfoPath -> FileItem/MediaInfo -> add_success -> SQLite -> get_by_src.
// This proves history producer/read semantics, not source organizer naming or
// a third-party TMDB deployment. The Go side has already run the native copy.
func sharedHistoryCompareActualSourceProducer(t *testing.T, path, target string, got *plugins.TransferHistoryRecord) {
	t.Helper()
	python, driver, source, evidence := os.Getenv("HISTORY_SOURCE_PYTHON"), os.Getenv("HISTORY_SOURCE_DRIVER"), os.Getenv("HISTORY_SOURCE_ROOT"), os.Getenv("HISTORY_PRODUCER_EVIDENCE")
	if python == "" || driver == "" || source == "" || evidence == "" {
		t.Fatal("fresh real source Python/driver/root/evidence bindings required")
	}
	run, err := os.MkdirTemp(evidence, "native-source-")
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(run, "config")
	if err = os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	fileitem := func(path string) map[string]any {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(path)
		ext := strings.TrimPrefix(filepath.Ext(name), ".")
		return map[string]any{"path": path, "storage": "local", "type": "file", "name": name, "basename": strings.TrimSuffix(name, "."+ext), "extension": ext, "size": info.Size(), "modify_time": float64(info.ModTime().UnixNano()) / 1e9}
	}
	input := map[string]any{"operations": []any{
		map[string]any{"case": "native-completed-physical-facts", "family": "transfer", "method": "add_success", "arguments": map[string]any{"mode": "copy"}, "producer": map[string]any{
			"meta_path": path, "meta": map[string]any{"type": "电影"}, "fileitem": fileitem(path),
			"media":        map[string]any{"source": "themoviedb", "type": "电影", "title": "测试影片", "year": "2024", "tmdb_id": 123, "imdb_id": "tt1234567", "tvdb_id": 456, "category": ""},
			"transferinfo": map[string]any{"target_item": fileitem(target), "file_list": []string{path}},
		}},
		map[string]any{"case": "source-followup-get", "family": "transfer", "method": "get_by_src", "arguments": map[string]any{"src": path}},
	}}
	raw, _ := json.MarshalIndent(input, "", "  ")
	inputPath, outputPath := filepath.Join(run, "inputs.json"), filepath.Join(run, "source.json")
	if err = os.WriteFile(inputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, driver, inputPath, outputPath)
	cmd.Env = []string{"HOME=" + config, "CONFIG_DIR=" + config, "PATH=/usr/bin:/bin", "PYTHONPATH=" + source + ":" + filepath.Dir(driver), "PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1", "DB_TYPE=sqlite", "LANG=C.UTF-8", "TZ=Asia/Shanghai"}
	log, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(run, "source.log"), log, 0600)
	if err != nil {
		t.Fatalf("real source producer failed: %v %s", err, log)
	}
	observed, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Observations []struct {
			Returned map[string]any
			Error    any
			Before   string `json:"before_local_date"`
			After    string `json:"after_local_date"`
		}
		NetworkAttempts []any `json:"network_attempts"`
	}
	if err = json.Unmarshal(observed, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.NetworkAttempts) != 0 || len(result.Observations) != 2 || result.Observations[0].Error != nil || result.Observations[1].Error != nil {
		t.Fatalf("unexpected source observation %s", observed)
	}
	raw, _ = json.Marshal(got)
	var actual map[string]any
	_ = json.Unmarshal(raw, &actual)
	for index, observation := range result.Observations {
		expected := observation.Returned
		if date, ok := expected["date"].(string); index == 0 && (!ok || date < observation.Before || date > observation.After) {
			t.Fatal("source did not freeze local producer date", expected["date"])
		}
		// Approved representation and nondeterministic wall-clock normalization,
		// not dropped business fields: all other source ORM columns are compared.
		expected["id"] = actual["id"]
		expected["date"] = actual["date"]
		if !reflect.DeepEqual(expected, actual) {
			t.Fatalf("real native/source producer mismatch\nsource %s\ntarget %s", mustHistoryJSON(expected), mustHistoryJSON(actual))
		}
	}
	if err = os.WriteFile(filepath.Join(run, "target-worker.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("real source producer + get_by_src all-column differential retained: %s", run)
}
