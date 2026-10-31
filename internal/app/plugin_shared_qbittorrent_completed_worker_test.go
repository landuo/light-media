package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func ownedQBittorrentCompletedRead(t *testing.T, object *originalPluginInstance, raw json.RawMessage) ownedQBittorrentCompletedSDKSnapshot {
	t.Helper()
	result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 20 * time.Second}, "snapshot", raw, nil)
	if err != nil {
		t.Fatal("actual standard worker query failed", err)
	}
	var out ownedQBittorrentCompletedSDKSnapshot
	if json.Unmarshal(result.Result, &out) != nil || out.PID <= 0 || out.PID == os.Getpid() || !out.StaleDenied {
		t.Fatal("query was not a distinct scoped worker")
	}
	return out
}

func TestSharedQBittorrentCompletedWorkerOwnedProtocolCold(t *testing.T) {
	// Real standard SDK process and production host, against a synthetic owned
	// protocol endpoint. NOT a native qB daemon or newly executed Python source.
	a, cookie, object := ownedQBittorrentCompletedSDKFixture(t, true)
	var mode, requests atomic.Int32
	hashA, hashB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	rows := []map[string]json.RawMessage{
		{"hash": json.RawMessage(`"` + hashA + `"`), "name": json.RawMessage(`"owned completed A"`), "progress": json.RawMessage(`1`), "amount_left": json.RawMessage(`0`), "state": json.RawMessage(`"queuedUP"`), "tags": json.RawMessage(`" a , b "`), "uploaded": json.RawMessage(`18446744073709551615`), "private_extension": json.RawMessage(`{"x":9007199254740993}`)},
		{"hash": json.RawMessage(`"` + hashB + `"`), "name": json.RawMessage(`"owned completed B"`), "progress": json.RawMessage(`1`), "state": json.RawMessage(`"stoppedUP"`), "tags": json.RawMessage(`"b"`)},
		{"hash": json.RawMessage(`"` + strings.Repeat("c", 40) + `"`), "progress": json.RawMessage(`0.5`), "state": json.RawMessage(`"downloading"`), "tags": json.RawMessage(`"a,b"`)},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_ = r.ParseForm()
			if r.Form.Get("password") != "owned-password" {
				io.WriteString(w, "Fails.")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned-session", Path: "/"})
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/info":
			if r.Method != "POST" {
				t.Error("completed info must POST")
			}
			_ = r.ParseForm()
			if r.Form.Get("filter") != "completed" || len(r.Form) > 2 {
				t.Error("expanded finite selector")
			}
			if cookie, _ := r.Cookie("SID"); cookie == nil || cookie.Value != "owned-session" {
				t.Error("session missing")
			}
			switch mode.Load() {
			case 1:
				http.Error(w, "owned rejected", http.StatusForbidden)
				return
			case 2:
				io.WriteString(w, `not-json`)
				return
			case 3:
				io.WriteString(w, `[{"progress":"1"}]`)
				return
			case 4:
				io.WriteString(w, `[{"progress":0,"tags":null}]`)
				return
			case 5:
				io.WriteString(w, `null`)
				return
			case 6:
				io.WriteString(w, `[null]`)
				return
			case 7:
				io.WriteString(w, `[{"progress":1,"reannounce":18446744073709551615,"reannounce_in":22}]`)
				return
			}
			selected := []map[string]json.RawMessage{}
			for _, row := range rows {
				var hash string
				_ = json.Unmarshal(row["hash"], &hash)
				if r.Form.Get("hashes") == "" || strings.Contains("|"+r.Form.Get("hashes")+"|", "|"+hash+"|") {
					selected = append(selected, row)
				}
			}
			_ = json.NewEncoder(w).Encode(selected)
		default:
			t.Error("preferences or mutations outside finite scope")
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	config := a.Settings()
	yes := true
	config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-qb", Kind: "qbittorrent", BaseURL: server.URL, Username: "owned-user", Password: "owned-password"}, Name: "Owned protocol qB", Enabled: &yes}}
	ownedDownloaderSave(t, a, config)
	before := ownedDownloaderChanges(t, a)
	baseline := ownedQBittorrentCompletedRead(t, object, json.RawMessage(`{"downloader_id":"owned-qb"}`))
	if baseline.Result.Error || len(baseline.Result.Torrents) != 2 || !reflect.DeepEqual(baseline.Result.Torrents[0].Fields, rows[0]) {
		t.Fatal("unknown-hash complete enumeration/fields/order changed")
	}
	for _, row := range []struct {
		payload string
		count   int
	}{
		{`{"downloader_id":"owned-qb","ids":[]}`, 2},
		{`{"downloader_id":"owned-qb","convenience":true}`, 2},
		{`{"downloader_id":"owned-qb","ids":"` + hashB + `"}`, 1},
		{`{"downloader_id":"owned-qb","ids":["` + hashA + `","` + hashA + `"],"tags":["a","b"]}`, 1},
		{`{"downloader_id":"owned-qb","tags":"a, b"}`, 0},
	} {
		got := ownedQBittorrentCompletedRead(t, object, json.RawMessage(row.payload))
		if got.Result.Error || len(got.Result.Torrents) != row.count || got.PID != baseline.PID || !got.HadPrevious {
			t.Fatal("source selection/current retained callback changed")
		}
	}
	ownedDownloaderNoWrites(t, a, before)
	for _, failureMode := range []int32{1, 2, 4, 6} {
		mode.Store(failureMode)
		got := ownedQBittorrentCompletedRead(t, object, json.RawMessage(`{"downloader_id":"owned-qb","tags":"a","convenience":true}`))
		if !got.Result.Error || len(got.Result.Torrents) != 0 {
			t.Fatal("caught query/tag error became successful []")
		}
	}
	mode.Store(5)
	if got := ownedQBittorrentCompletedRead(t, object, json.RawMessage(`{"downloader_id":"owned-qb","convenience":true}`)); got.Result.Error || got.Result.Torrents == nil || len(got.Result.Torrents) != 0 {
		t.Fatal("vendor null response became failure")
	}
	mode.Store(7)
	rename := ownedQBittorrentCompletedRead(t, object, json.RawMessage(`{"downloader_id":"owned-qb"}`))
	if rename.Result.Error || len(rename.Result.Torrents) != 1 || string(rename.Result.Torrents[0].Fields["reannounce_in"]) != "18446744073709551615" {
		t.Fatal("vendor key rename/precedence lost")
	}
	if _, exists := rename.Result.Torrents[0].Fields["reannounce"]; exists {
		t.Fatal("vendor old key retained")
	}
	mode.Store(6)
	shapeFailed, shapeErr := object.callSession(context.Background(), plugins.Runner{Timeout: 20 * time.Second}, "snapshot", json.RawMessage(`{"downloader_id":"owned-qb"}`), nil)
	if shapeErr == nil || len(shapeFailed.Result) != 0 {
		t.Fatal("outside-try null-row AttributeError became None")
	}
	mode.Store(3)
	failed, err := object.callSession(context.Background(), plugins.Runner{Timeout: 20 * time.Second}, "snapshot", json.RawMessage(`{"downloader_id":"owned-qb"}`), nil)
	if err == nil || len(failed.Result) != 0 {
		t.Fatal("completion TypeError became None or empty success")
	}
	mode.Store(0)
	config.Downloaders[0].Password = "owned-wrong-password"
	ownedDownloaderSave(t, a, config)
	if got := ownedQBittorrentCompletedRead(t, object, json.RawMessage(`{"downloader_id":"owned-qb"}`)); !got.Result.Error {
		t.Fatal("login failure accepted")
	}
	config.Downloaders[0].Password = "owned-password"
	ownedDownloaderSave(t, a, config)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedQBittorrentCompleted/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load("OwnedQBittorrentCompletedtwin")
	if !ok {
		t.Fatal("actual clone worker absent")
	}
	clone := ownedQBittorrentCompletedRead(t, value.(*originalPluginInstance), json.RawMessage(`{"downloader_id":"owned-qb"}`))
	if clone.PID == baseline.PID || !reflect.DeepEqual(clone.Result, baseline.Result) {
		t.Fatal("clone worker/result isolation failed")
	}
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
	value, ok = cold.pluginRunningInstances.Load("OwnedQBittorrentCompleted")
	if !ok {
		t.Fatal("same-data cold worker absent")
	}
	got := ownedQBittorrentCompletedRead(t, value.(*originalPluginInstance), json.RawMessage(`{"downloader_id":"owned-qb"}`))
	if got.PID == baseline.PID || !reflect.DeepEqual(got.Result, baseline.Result) {
		t.Fatal("cold host config/session recovery changed query")
	}
	t.Logf("actual standard worker/host and owned HTTP protocol: unknown-hash enumeration, exact selectors, caught failures, uncaught predicate, repair, clone and same-data cold; requests=%d; not real qB daemon", requests.Load())
}
