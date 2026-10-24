package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

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
