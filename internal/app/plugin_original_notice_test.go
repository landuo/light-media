package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func originalNoticeFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	path := os.Getenv("OWNED_NOTICE_FIXTURE_BINARY")
	if path == "" {
		t.Fatal("OWNED_NOTICE_FIXTURE_BINARY must name the compiled real worker")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(path, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedNotice" {
		t.Fatal("wrong notice worker")
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(data)), HostContract: pluginResourcesSDKContract}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index" {
			_ = json.NewEncoder(w).Encode(index)
		} else if r.URL.Path == "/worker" {
			_, _ = w.Write(data)
		} else {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]string{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	return a, cookie, originalBaseSDKLoaded(t, a, manifest.PluginID)
}

func postOwnedNotice(t *testing.T, object *originalPluginInstance, ctx context.Context, body string) plugins.NotificationDispatch {
	t.Helper()
	result, err := object.callOriginalMethod(ctx, "post_notice", json.RawMessage(body))
	if err != nil {
		t.Fatal("actual SDK post", err)
	}
	var out plugins.NotificationDispatch
	if json.Unmarshal(result.Result, &out) != nil {
		t.Fatal(string(result.Result))
	}
	return out
}

func TestOriginalNoticeActualSDKRoutesEventsHistoryAndModuleDelivery(t *testing.T) {
	a, cookie, object := originalNoticeFixture(t)
	settings := a.Settings()
	settings.AppDomain = "https://owned.invalid/base"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	received := make(chan map[string]any, 10)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			t.Error("bad actual sender body")
		}
		received <- value
		w.WriteHeader(http.StatusNoContent)
	}))
	defer sink.Close()
	configure := func(enabled, fail bool) {
		raw, _ := json.Marshal(map[string]any{"enabled": enabled, "endpoint": sink.URL, "fail": fail, "event_delay_ms": 100})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedNotice", string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	configure(true, false)
	noticePreferenceTest(t, a, "switch", "插件", "admin")
	noticePreferenceTest(t, a, "user", "admin", map[string]any{"Telegram": "owned-recipient"})
	out := postOwnedNotice(t, object, context.Background(), `{"title":"owned title","text":"owned body","mtype":"插件"}`)
	if out.Attempted != 1 || out.Failed != 0 || out.Queued != 0 {
		t.Fatal(out)
	}
	select {
	case payload := <-received:
		message := payload["kwargs"].(map[string]any)["message"].(map[string]any)
		if message["title"] != "owned title" || message["targets"].(map[string]any)["Telegram"] != "owned-recipient" || !strings.HasSuffix(message["link"].(string), "id=OwnedNotice") {
			t.Fatal(message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("actual sender never called local service")
	}
	a.wg.Wait()
	event, err := a.pluginStore.Data(context.Background(), "OwnedNotice", "notice_event")
	if err != nil || !strings.Contains(string(event.Value), "owned title") {
		t.Fatal("notice.message not consumed by actual worker", err, string(event.Value))
	}
	history, err := a.Store.List(context.Background(), "notification", 100, 0)
	if err != nil || len(history) != 1 {
		t.Fatal("history not recorded", err, len(history))
	}
	out = postOwnedNotice(t, object, context.Background(), `{"title":"integer recipient","userid":9007199254740993,"original_message_id":9007199254740995,"targets":{"owned":9007199254740997},"channel":"Telegram","image":"https://image.invalid/owned.png","action":0,"note":{"ignored":"Notification has no note field"}}`)
	if out.Attempted != 1 || out.Failed != 0 {
		t.Fatal(out)
	}
	payload := <-received
	message := payload["kwargs"].(map[string]any)["message"].(map[string]any)
	if message["userid"] != json.Number("9007199254740993") || message["original_message_id"] != json.Number("9007199254740995") || message["targets"].(map[string]any)["owned"] != json.Number("9007199254740997") {
		t.Fatal("recipient integers lost precision", message)
	}
	a.wg.Wait()
	history, err = a.Store.List(context.Background(), "notification", 100, 0)
	if err != nil || len(history) != 2 {
		t.Fatal("directed history missing", err, len(history))
	}
	for _, raw := range history {
		if strings.Contains(string(raw), "owned.png") || strings.Contains(string(raw), "integer recipient") || strings.Contains(string(raw), "9007199254740993") {
			t.Fatal("notification content stored without encryption")
		}
	}
	response := callTest(t, a.Handler(), "GET", "/api/notifications", "", cookie, "")
	var page struct {
		Data struct {
			Items []Notification `json:"items"`
		} `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Data.Items) != 2 || strings.Contains(response.Body.String(), "protected_content") {
		t.Fatal("authenticated history was not restored", response.Code, response.Body.String())
	}
	found := false
	for _, entry := range page.Data.Items {
		if entry.Title != "integer recipient" {
			continue
		}
		found = true
		if entry.UserID == nil || *entry.UserID != "9007199254740993" || entry.Channel != "Telegram" || entry.Image != "https://image.invalid/owned.png" || string(entry.Action) != "0" || string(entry.Note) != "{}" {
			t.Fatal("history lost original persisted fields", entry)
		}
		response = callTest(t, a.Handler(), "POST", "/api/notifications/"+entry.ID+"/read", "", cookie, "")
		var marked struct {
			Data Notification `json:"data"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &marked) != nil || !marked.Data.Read || marked.Data.Image != entry.Image || marked.Data.UserID == nil || *marked.Data.UserID != *entry.UserID {
			t.Fatal("marking read lost encrypted history", response.Code, response.Body.String())
		}
		var stored notificationRecord
		if err := a.Store.Get(context.Background(), "notification", entry.ID, &stored); err != nil || !stored.Read || len(stored.ProtectedContent) == 0 {
			t.Fatal("marking read discarded encrypted content", err)
		}
	}
	if !found {
		t.Fatal("directed message not saved")
	}
	configure(true, true)
	out = postOwnedNotice(t, object, context.Background(), `{"title":"failed delivery"}`)
	if out.Attempted != 1 || out.Failed != 1 || len(out.Errors) != 1 || out.Errors[0] != "PLUGIN_NOTIFICATION_SEND_FAILED" {
		t.Fatal("sender error disguised as delivery", out)
	}
	configure(false, false)
	out = postOwnedNotice(t, object, context.Background(), `{"title":"no provider","save_history":false}`)
	if out.Failed != 1 || out.Errors[0] != "PLUGIN_NOTIFICATION_PROVIDER_UNAVAILABLE" {
		t.Fatal("missing provider disguised as delivery", out)
	}
	history, err = a.Store.List(context.Background(), "notification", 100, 0)
	if err != nil || len(history) != 3 {
		t.Fatal("save_history or failed-send ordering", len(history), err)
	}
}

func TestOriginalNoticeQueuedAfterCallerEndsAndExplicitUserBypassesWindow(t *testing.T) {
	a, cookie, object := originalNoticeFixture(t)
	received := make(chan struct{}, 10)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer sink.Close()
	body, _ := json.Marshal(map[string]any{"enabled": true, "endpoint": sink.URL})
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedNotice", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	noticePreferenceTest(t, a, "window", "", []any{map[string]any{"start": "25:00", "end": "25:00"}})
	if err := a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	out := postOwnedNotice(t, object, ctx, `{"title":"later"}`)
	cancel()
	if out.Queued != 1 || out.Attempted != 0 {
		t.Fatal(out)
	}
	out = postOwnedNotice(t, object, context.Background(), `{"title":"now","userid":"owned-user","save_history":false}`)
	if out.Attempted != 1 || out.Queued != 0 || out.Failed != 0 {
		t.Fatal("explicit user was queued", out)
	}
	<-received
	noticePreferenceTest(t, a, "window", "", nil)
	if err := a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
		t.Fatal(err)
	}
	queue, err := a.originalNoticeQueue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	attempts := queue.Tick(context.Background())
	if len(attempts) != 1 || !attempts[0].Attempted || attempts[0].CallbackError != nil || len(queue.Pending()) != 0 {
		t.Fatal("queue retained expired caller context", attempts)
	}
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("queued sender did not run")
	}
	a.wg.Wait()
}
