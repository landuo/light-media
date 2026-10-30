package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func ownedTransmissionQueryRead(t *testing.T, object *originalPluginInstance, raw json.RawMessage) ownedTransmissionQuerySDKSnapshot {
	t.Helper()
	result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 20 * time.Second}, "snapshot", raw, nil)
	if err != nil {
		t.Fatal("actual standard worker query failed", err)
	}
	var out ownedTransmissionQuerySDKSnapshot
	if err = json.Unmarshal(result.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.PID <= 0 || out.PID == os.Getpid() || !out.StaleDenied {
		t.Fatal("query was not a distinct active scoped worker")
	}
	return out
}

func TestSharedTransmissionQueryRealSourceWorker(t *testing.T) {
	oraclePath := os.Getenv("TRANSMISSION_QUERY_ORACLE")
	if oraclePath == "" {
		t.Skip("explicit fresh source oracle and owned same-exec daemon required")
	}
	body, err := os.ReadFile(oraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Name     string                        `json:"name"`
			Request  json.RawMessage               `json:"request"`
			Error    bool                          `json:"error"`
			Torrents []plugins.TransmissionTorrent `json:"torrents"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(body, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) < 20 {
		t.Fatal("incomplete fresh source oracle")
	}
	endpoint := os.Getenv("TRANSMISSION_URL")
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/transmission/rpc" || os.Getenv("DOWNLOADER_TEST_INSTANCE") == "" {
		t.Fatal("owned harness endpoint required")
	}
	a, cookie, object := ownedTransmissionQuerySDKFixture(t, true)
	config := a.Settings()
	yes := true
	config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-tr", Kind: "transmission", BaseURL: endpoint, Username: os.Getenv("TRANSMISSION_USERNAME"), Password: os.Getenv("TRANSMISSION_PASSWORD")}, Name: "Owned same-exec Transmission", Enabled: &yes}}
	ownedDownloaderSave(t, a, config)
	before := ownedDownloaderChanges(t, a)
	var baseline ownedTransmissionQuerySDKSnapshot
	for i, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			got := ownedTransmissionQueryRead(t, object, row.Request)
			if i == 0 {
				baseline = got
			}
			if got.Result.Error != row.Error || len(got.Result.Torrents) != len(row.Torrents) {
				t.Fatal("source error flag/selection mismatch", row.Name, got.Result.Error, len(got.Result.Torrents), row.Error, len(row.Torrents))
			}
			if got.PID != baseline.PID || i > 0 && !got.HadPrevious {
				t.Fatal("query lost persistent worker scope")
			}
			for index := range row.Torrents {
				want, have := sharedTransmissionComparable(t, row.Torrents[index].Fields), sharedTransmissionComparable(t, got.Result.Torrents[index].Fields)
				if !reflect.DeepEqual(have, want) {
					// Synthetic-only fixtures; print field names, never private values.
					for key, value := range want.(map[string]any) {
						if !reflect.DeepEqual(value, have.(map[string]any)[key]) {
							t.Errorf("source native field mismatch: %s", key)
						}
					}
					t.Fatal("complete original fields/order differed")
				}
			}
			t.Logf("actual source/vendor and standard compiled worker→host→Transmission: %s tasks=%d source_error=%t", row.Name, len(got.Result.Torrents), got.Result.Error)
		})
	}
	ownedDownloaderNoWrites(t, a, before)
	cloneResponse := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedTransmissionQuery/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, cloneResponse.Code, cloneResponse.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load("OwnedTransmissionQuerytwin")
	if !ok {
		t.Fatal("clone worker absent")
	}
	clone := ownedTransmissionQueryRead(t, value.(*originalPluginInstance), oracle.Cases[0].Request)
	if clone.PID == baseline.PID || !reflect.DeepEqual(sharedTransmissionComparable(t, clone.Result), sharedTransmissionComparable(t, baseline.Result)) {
		t.Fatal("clone read identity/result changed")
	}
	// Authentication failure is the source Error=true, not a successful empty
	// list, and credentials never cross the worker boundary.
	config.Downloaders[0].Password = "owned-deliberately-invalid"
	ownedDownloaderSave(t, a, config)
	failed := ownedTransmissionQueryRead(t, object, oracle.Cases[0].Request)
	if !failed.Result.Error || len(failed.Result.Torrents) != 0 {
		t.Fatal("real authentication failure became empty success")
	}
	for _, method := range []string{"get_completed_torrents", "get_downloading_torrents"} {
		raw, _ := json.Marshal(map[string]any{"downloader_id": "owned-tr", "query_method": method})
		failed := ownedTransmissionQueryRead(t, object, raw)
		if !failed.Result.Error || len(failed.Result.Torrents) != 0 {
			t.Fatal("source convenience authentication None became empty success")
		}
	}
	config.Downloaders[0].Password = os.Getenv("TRANSMISSION_PASSWORD")
	ownedDownloaderSave(t, a, config)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, ok = cold.pluginRunningInstances.Load("OwnedTransmissionQuery")
	if !ok {
		t.Fatal("cold worker absent")
	}
	read := ownedTransmissionQueryRead(t, value.(*originalPluginInstance), oracle.Cases[0].Request)
	if read.PID == baseline.PID || !reflect.DeepEqual(sharedTransmissionComparable(t, read.Result), sharedTransmissionComparable(t, baseline.Result)) {
		t.Fatal("cold configuration/worker query changed result")
	}
	sum := sha256.Sum256(body)
	for _, env := range []string{"OWNED_TRANSMISSION_QUERY_FIXTURE_BINARY", "OWNED_TRANSMISSION_QUERY_DENIED_FIXTURE_BINARY"} {
		raw, e := os.ReadFile(os.Getenv(env))
		if e != nil {
			t.Fatal(e)
		}
		digest := sha256.Sum256(raw)
		t.Logf("actual worker %s sha256=%s", env, hex.EncodeToString(digest[:]))
	}
	t.Logf("fresh source oracle sha256=%s; real auth failure, independently bound clone and cold App/worker verified; no mutation capability", hex.EncodeToString(sum[:]))
}

func sharedTransmissionComparable(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var out any
	if err = d.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
