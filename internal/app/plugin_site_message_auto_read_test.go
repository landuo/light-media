package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/builtinplugins/plugins/sitemessageautoread"
	"light-media/internal/sites"
)

func TestBuiltinSiteMessageAutoReadFromSettingsAndService(t *testing.T) {
	t.Setenv("LIGHT_MEDIA_PLUGIN_BUILTIN", "on")
	a := newTestApp(t)
	a.pluginBuiltinDisabled = false
	ctx := t.Context()
	pluginID := sitemessageautoread.ID
	var matchedRead atomic.Int32
	var unrelatedRead atomic.Int32
	var mailboxFetched atomic.Int32
	selected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "owned=fixture" {
			t.Error("managed site credential missing")
		}
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=7">owned</a><div>上传量：2 GB 下载量：1 GB 分享率：2.00</div><span><a href="messages.php">信息箱</a> (2)</span>`)
		case "/userdetails.php", "/getusertorrentlistajax.php":
			fmt.Fprint(w, `<a href="logout.php">退出</a>`)
		case "/messages.php":
			fmt.Fprint(w, `<a href="logout.php">退出</a>`)
			q := r.URL.Query()
			switch q.Get("action") {
			case "viewmailbox":
				mailboxFetched.Add(1)
				switch {
				case q.Get("box") == "-2":
					fmt.Fprint(w, `<p>No messages.</p>`)
				case q.Get("page") == "2" && matchedRead.Load() == 0:
					fmt.Fprint(w, `<table><tr><td><img alt="Unread"></td><td><a href="messages.php?action=viewmessage&amp;id=2"> 每日通知 </a></td></tr></table>`)
				case q.Get("page") == "2":
					fmt.Fprint(w, `<p>No messages.</p>`)
				default:
					fmt.Fprint(w, `<table><tr><td><img alt="Unread"></td><td><a href="messages.php?action=viewmessage&amp;id=1">每日通知（重要）</a></td></tr></table><a href="messages.php?action=viewmailbox&amp;box=1&amp;unread=yes&amp;page=2">下一页</a>`)
				}
			case "viewmessage":
				if q.Get("id") != "2" {
					unrelatedRead.Add(1)
					http.Error(w, "unexpected message", http.StatusBadRequest)
					return
				}
				matchedRead.Add(1)
				fmt.Fprint(w, `<h1>每日通知</h1><table><tr><td class="colhead">From</td></tr><tr><td>Date</td><td>2026-11-28</td></tr><tr><td>测试短讯</td></tr></table>`)
			default:
				t.Errorf("unexpected mailbox action %q", q.Get("action"))
				http.Error(w, "unexpected action", http.StatusBadRequest)
			}
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer selected.Close()
	var unselectedRequests atomic.Int32
	unselected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unselectedRequests.Add(1)
		http.NotFound(w, r)
	}))
	defer unselected.Close()
	settings := a.Settings()
	settings.Sites = []SiteConfig{
		{Config: sites.Config{ID: "selected-pt", Kind: "jiayuan", Name: "合成已选站点", BaseURL: selected.URL, Cookie: "owned=fixture"}, Enabled: true},
		{Config: sites.Config{ID: "other-pt", Kind: "jiayuan", Name: "合成未选站点", BaseURL: unselected.URL, Cookie: "owned=fixture"}, Enabled: true},
	}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	a.adoptBuiltinVersions(ctx)
	a.adoptBuiltinVersions(ctx)
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	if in, err := a.pluginStore.Installation(ctx, pluginID); err != nil || !in.Installed || in.Generation != 1 {
		t.Fatalf("new builtin should be provisioned once: %+v, %v", in, err)
	}
	cookie := setupTestAdmin(t, a)
	handler := a.Handler()
	request := func(method, path, body string) string {
		t.Helper()
		response := callTest(t, handler, method, path, body, cookie, "")
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	services := func() []PluginServiceInfo {
		t.Helper()
		var response struct{ Data []PluginServiceInfo }
		if err := json.Unmarshal([]byte(request("GET", "/api/plugins/services", "")), &response); err != nil {
			t.Fatal(err)
		}
		return response.Data
	}
	form := request("GET", "/api/plugins/"+pluginID+"/form", "")
	if !strings.Contains(form, "selected-pt") || !strings.Contains(form, "VTextarea") || strings.Contains(form, "owned=fixture") {
		t.Fatalf("plugin form did not expose site selection and title configuration: %s", form)
	}
	if jobs := services(); len(jobs) != 0 || mailboxFetched.Load() != 0 {
		t.Fatalf("first launch should be disabled: %v", jobs)
	}
	path := "/api/plugins/" + pluginID
	invalid := callTest(t, handler, "PUT", path, `{"enabled":true,"sites":["selected-pt"],"titles":"  "}`, cookie, "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("blank title rules should fail: %d %s", invalid.Code, invalid.Body.String())
	}
	request("PUT", path, `{"enabled":true,"sites":["selected-pt"],"titles":" 每日通知 "}`)
	jobs := services()
	if len(jobs) != 1 || jobs[0].InstanceID != pluginID || jobs[0].NextRun == nil {
		t.Fatalf("missing registered schedule: %+v", jobs)
	}
	otherPolicy, err := a.siteCollectionMessagePolicy(ctx, "other-pt")
	if err != nil || !otherPolicy.enabled {
		t.Fatalf("unselected site message policy changed: %+v %v", otherPolicy, err)
	}
	// The standard account refresh must still retrieve statistics without
	// opening the selected site's mailbox when the plugin owns its reads.
	snapshot, err := a.collectManagedSiteObserved(ctx, "selected-pt", "", &siteCollectionHooks{})
	if err != nil || snapshot.Stats == nil || snapshot.Stats.Uploaded == nil || *snapshot.Stats.Uploaded != 2<<30 || mailboxFetched.Load() != 0 {
		t.Fatalf("account refresh entered the mailbox or missed upload stats: %+v %v", snapshot, err)
	}
	request("PUT", path, `{"enabled":true,"onlyonce":true,"sites":["selected-pt"],"titles":" 每日通知 "}`)
	a.wg.Wait()
	stored := request("GET", path, "")
	if !strings.Contains(stored, `"onlyonce":false`) || matchedRead.Load() != 1 {
		t.Fatalf("one-shot setting did not process the exact match once: %d %s", matchedRead.Load(), stored)
	}
	args := mustBuiltinJSON(t, map[string]string{"instance_id": pluginID, "service_id": jobs[0].ServiceID})
	request("POST", "/api/plugins/services/run", string(args))
	a.wg.Wait()
	if matchedRead.Load() != 1 || unrelatedRead.Load() != 0 || unselectedRequests.Load() != 0 {
		t.Fatalf("unexpected read or other site traffic: match=%d unrelated=%d other=%d", matchedRead.Load(), unrelatedRead.Load(), unselectedRequests.Load())
	}
	request("PUT", path, `{"enabled":false,"sites":["selected-pt"],"titles":"每日通知"}`)
	policy, err := a.siteCollectionMessagePolicy(ctx, "selected-pt")
	if err != nil || !policy.enabled || len(services()) != 0 {
		t.Fatalf("disabled schedule should restore ordinary policy: %+v %v", policy, err)
	}
}
