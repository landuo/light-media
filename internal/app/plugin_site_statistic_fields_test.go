package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/sites"
)

func TestSiteStatisticExtendedFieldsActualSDKAndEncryptedColdSnapshot(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	ctx := context.Background()
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nav := `<a href="logout.php">退出</a>`
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, nav+`<a href="userdetails.php?id=7">owned</a><span><a href="messages.php">信息箱</a> (1)</span><div>上传：1 GB 下载：0 B 做种中：1</div>`)
		case "/userdetails.php":
			fmt.Fprint(w, nav)
		case "/getusertorrentlistajax.php":
			fmt.Fprint(w, `<table class="torrents"><tr><td>名称</td><td>大小</td><td>类型</td><td>在做种</td></tr><tr><td><a href="details.php?id=1">owned</a></td><td>1 KiB</td><td>video</td><td>3</td></tr></table>`)
		case "/messages.php":
			fmt.Fprint(w, nav)
			if r.URL.Query().Get("action") == "viewmessage" {
				reads.Add(1)
				fmt.Fprint(w, `<h1>owned-private-heading</h1><table><tr><td class="colhead">header</td></tr><tr><td>date</td><td>2026-10-21</td></tr><tr><td>owned-private-message</td></tr></table>`)
			} else if r.URL.Query().Get("box") == "1" {
				fmt.Fprint(w, `<table><tr><td><a href="messages.php?action=viewmessage&amp;id=1">unread</a></td></tr></table>`)
			}
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer server.Close()
	c := a.Settings()
	c.Sites[0].BaseURL = server.URL
	readMessages := true
	c.ReadSiteMessages = &readMessages
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	run := enqueueSyntheticPluginHTTP(t, a)
	a.executePluginRun(ctx, run)
	finished, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "succeeded" {
		t.Fatal("actual SDK HTTP refresh failed", err, finished.ErrorCode)
	}
	raw, err := a.pluginStore.siteStatisticReceipt(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	var response plugins.HTTPResponse
	if json.Unmarshal(raw, &response) != nil {
		t.Fatal("invalid HTTP receipt")
	}
	var payload struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if json.Unmarshal(response.Body, &payload) != nil {
		t.Fatal("invalid response body")
	}
	if !payload.Success || len(payload.Data) != 19 || payload.Data["leeching_size"] != float64(0) || payload.Data["updated_time"] != nil || payload.Data["message_unread"] != float64(1) || !reflect.DeepEqual(payload.Data["seeding_info"], []any{[]any{float64(3), float64(1024)}}) || !reflect.DeepEqual(payload.Data["message_unread_contents"], []any{[]any{"owned-private-heading", "2026-10-21", "owned-private-message"}}) || reads.Load() != 1 {
		t.Fatal("extended fields lost before SDK/API receipt")
	}
	var sealed []byte
	if a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_stats_latest' AND id='known'`).Scan(&sealed) != nil || bytes.Contains(sealed, []byte("owned-private-message")) {
		t.Fatal("message contents stored as plaintext")
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var snapshot siteSnapshot
	if err = reopened.Store.Get(ctx, "site_stats_latest", "known", &snapshot); err != nil || len(snapshot.Stats.Messages) != 1 || len(snapshot.Stats.SeedingInfo) != 1 || *snapshot.Stats.Messages[0][2] != "owned-private-message" {
		t.Fatal("cold encrypted snapshot lost message/seeding fields", err)
	}
	stored := reopened.Settings()
	if stored.ReadSiteMessages == nil || !*stored.ReadSiteMessages {
		t.Fatal("read messages option lost on cold startup")
	}
}

func TestSiteStatisticAPIRevocationBeforeMessageReadStopsSideEffect(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	ctx := context.Background()
	var totalRequests atomic.Int32
	var reads atomic.Int32
	var revoked atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		totalRequests.Add(1)
		nav := `<a href="logout.php">退出</a>`
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, nav+`<a href="userdetails.php?id=7">owned</a><span><a href="messages.php">信息箱</a> (1)</span><div>上传：1 GB 下载：0 B 总做种数：0 总做种体积：0 B</div>`)
		case "/userdetails.php":
			fmt.Fprint(w, nav)
		case "/messages.php":
			if r.URL.Query().Get("action") == "viewmessage" {
				reads.Add(1)
				t.Error("revoked API request read/marked a message")
			}
			if revoked.CompareAndSwap(false, true) {
				c := a.Settings()
				c.APIToken = "owned-revoked-during-inbox"
				if err := a.saveSettings(ctx, c); err != nil {
					t.Error(err)
				}
			}
			fmt.Fprint(w, nav+`<table><tr><td><a href="messages.php?action=viewmessage&amp;id=1">unread</a></td></tr></table>`)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer server.Close()
	c := a.Settings()
	c.Sites[0].BaseURL = server.URL
	yes := true
	c.ReadSiteMessages = &yes
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	run := enqueueSyntheticPluginHTTP(t, a)
	a.executePluginRun(ctx, run)
	finished, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "failed" || finished.ErrorCode != "PLUGIN_API_AUTH_REVOKED" || !revoked.Load() || reads.Load() != 0 {
		t.Fatal("API revocation did not guard each message IO", finished.State, finished.ErrorCode, err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('site_connection','site_stats_latest','site_stats_history')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked response published private account data", err)
	}
	siteMessageAssertColdRevocationHasNoEffects(t, a, run, func() int { return int(totalRequests.Load()) })
}

func TestSiteStatisticSourceDefaultsStaySeparateFromNativeUnknowns(t *testing.T) {
	size, count := int64(1234), int64(0)
	message := "owned error"
	title, date, body := "title", "date", "body"
	s := siteSnapshot{Stats: &sites.UserStats{}, SourceData: &sites.SourceUserData{LeechingSize: size, MessageUnread: count, Messages: [][]any{{title, date, body}}, SeedingInfo: [][2]int64{{2, 1024}}, Error: &message}}
	for _, populated := range []bool{true, false} {
		if !populated {
			s.SourceData = &sites.SourceUserData{SeedingInfo: [][2]int64{}, Messages: [][]any{}}
		}
		raw, e := json.Marshal(siteStatisticAPIData(s))
		if e != nil {
			t.Fatal(e)
		}
		var m map[string]any
		json.Unmarshal(raw, &m)
		if populated {
			if m["leeching_size"] != float64(size) || m["err_msg"] != message || len(m["seeding_info"].([]any)) != 1 {
				t.Fatal("source field values lost")
			}
		} else if m["leeching_size"] != float64(0) || m["err_msg"] != nil || len(m["seeding_info"].([]any)) != 0 {
			t.Fatal("source constructor defaults lost")
		}
		if s.Stats.LeechingBytes != nil || s.Stats.MessageUnread != nil || s.Stats.SeedingInfo != nil {
			t.Fatal("native unknown became constructor zero")
		}
	}
}

func TestSiteStatisticConnectionTestDoesNotReadMessages(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/messages.php" {
			reads.Add(1)
			t.Error("connection test read/marked messages")
		}
		fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=7">owned</a><span><a href="messages.php">信息箱</a> (1)</span><div>上传：0 B 下载：0 B 总做种数：0 总做种体积：0 B</div>`)
	}))
	defer server.Close()
	c := a.Settings()
	yes := true
	c.ReadSiteMessages = &yes
	c.Sites = []SiteConfig{parallelSearchSite("owned-connection", server.URL)}
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.refreshManagedSite(ctx, "owned-connection"); err != nil || reads.Load() != 0 {
		t.Fatal("connection test changed inbox state", err)
	}
	var snapshot siteSnapshot
	if err := a.Store.Get(ctx, "site_stats_latest", "owned-connection", &snapshot); err != nil || snapshot.Stats.Messages != nil || snapshot.Stats.MessageUnread == nil || *snapshot.Stats.MessageUnread != 1 {
		t.Fatal("disabled message fetch invented an empty inbox", err)
	}
}

func TestSiteStatisticJobCancellationBeforeMessageReadStopsSideEffect(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	var job Job
	var reads atomic.Int32
	var cancelled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nav := `<a href="logout.php">退出</a>`
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, nav+`<a href="userdetails.php?id=7">owned</a><span><a href="messages.php">信息箱</a> (1)</span><div>上传：0 B 下载：0 B 总做种数：0 总做种体积：0 B</div>`)
		case "/userdetails.php":
			fmt.Fprint(w, nav)
		case "/messages.php":
			if r.URL.Query().Get("action") == "viewmessage" {
				reads.Add(1)
				t.Error("cancelled job read/marked a message")
			}
			if cancelled.CompareAndSwap(false, true) {
				if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id=?`, job.ID); err != nil {
					t.Error(err)
				}
			}
			fmt.Fprint(w, nav+`<table><tr><td><a href="messages.php?action=viewmessage&amp;id=1">unread</a></td></tr></table>`)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer server.Close()
	c := a.Settings()
	yes := true
	c.ReadSiteMessages = &yes
	c.Sites = []SiteConfig{parallelSearchSite("owned-job", server.URL)}
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.enqueue(ctx, "site_refresh", map[string]string{"id": "site_refresh"}, ""); err != nil {
		t.Fatal(err)
	}
	var err error
	job, err = a.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = a.runSiteRefresh(ctx, job)
	if pluginErrorCode(err) != "JOB_CANCELLED" || !cancelled.Load() || reads.Load() != 0 {
		t.Fatal("cancelled refresh job retained message IO authority", err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('site_connection','site_stats_latest','site_stats_history')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("cancelled job committed private observations", err)
	}
}
