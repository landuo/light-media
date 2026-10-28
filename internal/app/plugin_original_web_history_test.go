package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"light-media/internal/notice"
)

func originalWebHistoryRead(t *testing.T, a *App, cookie *http.Cookie, path string) []map[string]any {
	t.Helper()
	response := callTest(t, a.Handler(), "GET", path, "", cookie, "")
	var result struct{ Data []map[string]any }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatal("actual history read failed", response.Code)
	}
	return result.Data
}

func TestOriginalWebMessageHistoryUsesFixedPaginationAndStableIdentity(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-web-message/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Commit  string
		History []struct {
			Page, Count int
			IDs         []int
		}
	}
	if json.Unmarshal(raw, &ref) != nil || ref.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(ref.History) != 6 {
		t.Fatal("source history replay missing")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ids := []int64{}
	for i := 1; i <= 25; i++ {
		n := Notification{ID: "owned-message-" + strconv.Itoa(i), Title: "owned title", Message: "owned payload", Channel: "Web", CreatedAt: time.Date(2026, 10, 27, 1, 0, 0, 0, time.Local).UTC().Format(time.RFC3339Nano)}
		record, err := a.originalMessageHistoryRecord(n, notice.Object{"channel": "Web", "title": n.Title, "text": n.Message, "action": i % 2})
		if err != nil {
			t.Fatal(err)
		}
		record.Notification = Notification{ID: n.ID, CreatedAt: n.CreatedAt}
		if err = a.Store.Put(context.Background(), "notification", n.ID, record); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err = a.Store.DB.QueryRow(`SELECT rowid FROM records WHERE kind='notification' AND id=?`, n.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, sample := range ref.History {
		rows := originalWebHistoryRead(t, a, cookie, "/api/message/web?page="+strconv.Itoa(sample.Page)+"&count="+strconv.Itoa(sample.Count))
		if len(rows) != len(sample.IDs) {
			t.Fatal("source count/offset differs", sample.Page, sample.Count, len(rows), len(sample.IDs))
		}
		for i, ordinal := range sample.IDs {
			if int64(rows[i]["id"].(float64)) != ids[ordinal-1] {
				t.Fatal("source same-second id DESC order differs", sample.Page, sample.Count)
			}
		}
	}
	if got := originalWebHistoryRead(t, a, cookie, "/api/v1/message/web?size=1"); len(got) != 20 {
		t.Fatal("fixed frontend size unexpectedly changed backend count", len(got))
	}
	for _, path := range []string{"/api/message/web?page=bad", "/api/message/web?count=1e0"} {
		if response := callTest(t, a.Handler(), "GET", path, "", cookie, ""); response.Code != 422 {
			t.Fatal("invalid source integer accepted", response.Code)
		}
	}
	if response := callTest(t, a.Handler(), "GET", "/api/message/web", "", nil, ""); response.Code != 401 {
		t.Fatal("anonymous history disclosure")
	}
}

func TestOriginalWebMessageHistoryOrdersPersistedSourceLocalTime(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-web-message/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Timezone struct {
			Samples []struct {
				ID      int
				UTC     string
				RegTime string `json:"reg_time"`
			}
			IDs []int
		} `json:"timezone_history"`
	}
	if json.Unmarshal(raw, &ref) != nil || len(ref.Timezone.Samples) != 2 || len(ref.Timezone.IDs) != 2 {
		t.Fatal("fixed source timezone ordering replay missing")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ids := map[int]int64{}
	for _, sample := range ref.Timezone.Samples {
		n := Notification{ID: "owned-time-" + strconv.Itoa(sample.ID), CreatedAt: sample.UTC}
		record, err := a.originalMessageHistoryRecord(n, notice.Object{"channel": "Web"})
		if err != nil {
			t.Fatal(err)
		}
		var payload originalMessageHistoryPayload
		if a.Store.Unseal(record.ProtectedContent, &payload) != nil {
			t.Fatal("owned prior-process content unavailable")
		}
		payload.SourceMessage, _ = originalMessageHistoryColumns(notice.Object{"channel": "Web"}, sample.RegTime)
		record.SourceRegisteredAt = sample.RegTime
		record.ProtectedContent, err = a.Store.Seal(payload)
		if err != nil || a.Store.Put(context.Background(), "notification", n.ID, record) != nil {
			t.Fatal("owned prior-process history could not be stored")
		}
		var id int64
		if a.Store.DB.QueryRow(`SELECT rowid FROM records WHERE kind='notification' AND id=?`, n.ID).Scan(&id) != nil {
			t.Fatal("owned source row identity missing")
		}
		ids[sample.ID] = id
	}
	rows := originalWebHistoryRead(t, a, cookie, "/api/message/web")
	for i, ordinal := range ref.Timezone.IDs {
		if int64(rows[i]["id"].(float64)) != ids[ordinal] {
			t.Fatal("persisted local reg_time order was replaced by UTC order")
		}
	}
}

func TestOriginalWebMessageActualSDKHistoryAndInvisibleNoHistoryPrompt(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-web-message/note-source-oracle.json")
	var noteSource struct {
		Ignored bool            `json:"notification_ignores_note"`
		Note    json.RawMessage `json:"history_note"`
	}
	if err != nil || json.Unmarshal(raw, &noteSource) != nil || !noteSource.Ignored {
		t.Fatal("fixed Notification extra-note replay missing", err)
	}
	a, cookie, object := originalNoticeFixture(t)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer sink.Close()
	config, _ := json.Marshal(map[string]any{"enabled": true, "endpoint": sink.URL})
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedNotice", string(config), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	postOwnedNotice(t, object, context.Background(), `{"channel":"Web","title":"owned source title","text":"owned source body","userid":9007199254740993,"note":[{"title_year":"Owned note (2026)","type":"电影","vote_average":8.5}]}`)
	rows := originalWebHistoryRead(t, a, cookie, "/api/message/web")
	if len(rows) != 1 || rows[0]["title"] != "owned source title" || rows[0]["text"] != "owned source body" || rows[0]["userid"] != "9007199254740993" || rows[0]["action"] != float64(1) || rows[0]["channel"] != "Web" {
		t.Fatal("actual SDK source columns were lost", rows)
	}
	note, _ := json.Marshal(rows[0]["note"])
	if !originalBaseJSONEqual(note, noteSource.Note) {
		t.Fatal("Notification accepted note unlike the fixed source model")
	}
	var payload []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='notification' LIMIT 1`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "owned source body") {
		t.Fatal("private source message columns persisted in clear public projection")
	}
	a.systemMessages.put("system", nil, "owned preserved system", nil, time.Now())
	a.systemMessages.put("plugin", nil, "owned preserved plugin", nil, time.Now())
	out := postOwnedNotice(t, object, context.Background(), `{"channel":"Web","title":"owned invisible prompt","userid":"admin","save_history":false}`)
	if out.Attempted != 1 || out.Failed != 0 {
		t.Fatal("actual post_message override was not executed", out)
	}
	if got := originalWebHistoryRead(t, a, cookie, "/api/message/web"); len(got) != 1 {
		t.Fatal("source save_history=false became a fake history row")
	}
	if a.systemMessages.put("user", nil, "owned invisible prompt", nil, time.Now()) {
		t.Fatal("fixed role=user rejection was silently redesigned")
	}
	first, second := a.systemMessages.get("user"), a.systemMessages.get("system")
	if !strings.Contains(string(first), "owned preserved system") || !strings.Contains(string(second), "owned preserved plugin") {
		t.Fatal("history read consumed or split shared FIFO")
	}
}

func TestOriginalWebMessageActualSDKInputBeforeIncomingHistory(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	ownedInputCreate(t, a, cookie, "OwnedInput")
	if response := callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"owned answer","userid":"forged"}`, cookie, ""); response.Code != 200 {
		t.Fatal(response.Code)
	}
	if got := originalWebHistoryRead(t, a, cookie, "/api/message/web"); len(got) != 0 {
		t.Fatal("consumed plugin reply was recorded unlike source")
	}
	if response := callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"/owned","user":"forged","source":"forged","channel":"Telegram"}`, cookie, ""); response.Code != 200 {
		t.Fatal(response.Code)
	}
	rows := originalWebHistoryRead(t, a, cookie, "/api/message/web")
	if len(rows) != 1 || rows[0]["action"] != float64(0) || rows[0]["text"] != "/owned" || rows[0]["userid"] != "admin" || rows[0]["source"] != "admin" || rows[0]["channel"] != "Web" || rows[0]["title"] != nil {
		t.Fatal("incoming source history or account binding differs", rows)
	}
	ownedInputCreate(t, a, cookie, "OwnedInput")
	if response := callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":"quit"}`, cookie, ""); response.Code != 200 {
		t.Fatal(response.Code)
	}
	if got := originalWebHistoryRead(t, a, cookie, "/api/message/web"); len(got) != 1 {
		t.Fatal("cancel/prompt invented a persisted row")
	}
	callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"CALLBACK:owned"}`, cookie, "")
	if got := originalWebHistoryRead(t, a, cookie, "/api/message/web"); len(got) != 1 {
		t.Fatal("callback was recorded as ordinary user input")
	}
	var incoming, notifications int
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='message'`).Scan(&incoming)
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&notifications)
	if incoming != 1 || notifications != 0 {
		t.Fatal("incoming history polluted sent-notification center", incoming, notifications)
	}
}
