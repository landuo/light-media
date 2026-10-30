package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func sharedTransmissionFreshSource(t *testing.T, endpoint, name string, request any) ([]plugins.TransmissionTorrent, bool) {
	t.Helper()
	root := os.Getenv("TRANSMISSION_QUERY_SOURCE_ROOT")
	installed := os.Getenv("TRANSMISSION_QUERY_INSTALLED_ROOT")
	outputRoot := os.Getenv("TRANSMISSION_QUERY_EVIDENCE_DIR")
	if root == "" || installed == "" || outputRoot == "" {
		t.Fatal("fresh oracle source paths required")
	}
	if err := os.MkdirAll(outputRoot, 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(outputRoot, name+".json")
	command := exec.Command("python3", "../../testdata/shared-transmission-query/source_real.py", root, installed, output, "--query-only")
	command.Env = []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "TRANSMISSION_URL=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "TRANSMISSION_URL="+endpoint)
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdin = bytes.NewReader(raw)
	if _, err = command.CombinedOutput(); err != nil {
		t.Fatal("actual source read failed", err)
	}
	raw, err = os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Torrents []plugins.TransmissionTorrent `json:"torrents"`
		Error    bool                          `json:"error"`
	}
	if json.Unmarshal(raw, &source) != nil {
		t.Fatal("invalid source query result")
	}
	sum := sha256.Sum256(raw)
	t.Logf("fresh actual wrapper/vendor query %s source sha256=%s", name, hex.EncodeToString(sum[:]))
	return source.Torrents, source.Error
}

func TestSharedTransmissionQueryRealSlowAndRecentlyActive(t *testing.T) {
	if os.Getenv("TRANSMISSION_QUERY_ORACLE") == "" {
		t.Skip("owned same-exec source and daemon required")
	}
	realURL := os.Getenv("TRANSMISSION_URL")
	target, err := url.Parse(realURL)
	if err != nil || target.Hostname() != "127.0.0.1" {
		t.Fatal("owned loopback daemon required")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{Proxy: nil}
	defer proxy.Transport.(*http.Transport).CloseIdleConnections()
	var delayed, blocked atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if e != nil {
			http.Error(w, "owned malformed", 400)
			return
		}
		var request struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &request)
		if r.Method != "POST" || r.URL.Path != "/transmission/rpc" || request.Method != "session-get" && request.Method != "torrent-get" {
			blocked.Add(1)
			http.Error(w, "owned read-only boundary", 400)
			return
		}
		if request.Method == "torrent-get" {
			delayed.Add(1)
			select {
			case <-time.After(21 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	endpoint := server.URL + "/transmission/rpc"
	a, _, object := ownedTransmissionQuerySDKFixture(t, true)
	config := a.Settings()
	config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-tr", Kind: "transmission", BaseURL: endpoint, Username: os.Getenv("TRANSMISSION_USERNAME"), Password: os.Getenv("TRANSMISSION_PASSWORD")}}}
	ownedDownloaderSave(t, a, config)
	start := time.Now()
	expected, sourceError := sharedTransmissionFreshSource(t, endpoint, "source-real-delayed", map[string]any{})
	if sourceError || time.Since(start) < 21*time.Second {
		t.Fatal("source delay was not actually exercised")
	}
	start = time.Now()
	result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 70 * time.Second}, "snapshot", json.RawMessage(`{"downloader_id":"owned-tr"}`), nil)
	if err != nil {
		t.Fatal("actual typed worker retained unrelated 20s native total timeout", err)
	}
	var got ownedTransmissionQuerySDKSnapshot
	if json.Unmarshal(result.Result, &got) != nil || got.Result.Error || time.Since(start) < 21*time.Second {
		t.Fatal("target delay did not succeed")
	}
	if !reflect.DeepEqual(sharedTransmissionComparable(t, got.Result.Torrents), sharedTransmissionComparable(t, expected)) {
		t.Fatal("complete delayed actual daemon fields differ")
	}
	if delayed.Load() != 2 || blocked.Load() != 0 {
		t.Fatal("unexpected native requests at read-only real-daemon proxy")
	}
	config.Downloaders[0].BaseURL = realURL
	ownedDownloaderSave(t, a, config)
	expected, sourceError = sharedTransmissionFreshSource(t, realURL, "source-real-recently-active", map[string]any{"ids": "recently-active"})
	got = ownedTransmissionQueryRead(t, object, json.RawMessage(`{"downloader_id":"owned-tr","ids":"recently-active"}`))
	if sourceError != got.Result.Error || !reflect.DeepEqual(sharedTransmissionComparable(t, got.Result.Torrents), sharedTransmissionComparable(t, expected)) {
		t.Fatal("fresh source recently-active selection differs")
	}
	t.Log("real daemon source and standard compiled worker survive 21s read-idle delay; same full fields, recently-active separately fresh; runner deadline explicitly 70s for this case, not a claim about every platform default deadline")
}
