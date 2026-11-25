package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func TestBrowserSubscriptionAcknowledgesCommittedSaveAndResumesEffects(t *testing.T) {
	for _, media := range []catalog.Media{
		{ID: 1300968, MediaType: "movie", Title: "饥饿游戏：收获日出", Year: 2026},
		{ID: 206484, MediaType: "tv", Title: "诛仙", Year: 2022},
	} {
		t.Run(media.MediaType, func(t *testing.T) {
			a := subscriptionTestApp(t)
			ctx := context.Background()
			cookie := setupTestAdmin(t, a)
			in := SubscriptionRequest{Media: media}
			if media.MediaType == "tv" {
				in.Season = 4
			}
			fixture := installNoticeSourceFixtures(t, a, media, in.Season, 5)
			subscriptionStatsInstallSettings(t, a, "http://127.0.0.1:1", false)
			handler := a.Handler()
			send := func(in SubscriptionRequest) *httptest.ResponseRecorder {
				raw, err := json.Marshal(in)
				if err != nil {
					t.Fatal(err)
				}
				return proxyRequest(t, handler, http.MethodPost, "/api/subscriptions", string(raw), "", cookie, map[string]string{"Prefer": "respond-async"})
			}
			path := "/api/subscriptions/matching?media_type=" + media.MediaType + "&tmdb_id=" + strconv.FormatInt(media.ID, 10)
			check := func(want int) {
				t.Helper()
				out := callTest(t, handler, "GET", path, "", cookie, "")
				var body struct{ Data []Subscription }
				if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &body) != nil || len(body.Data) != want {
					t.Fatalf("membership lookup: HTTP %d, %d rows; wanted %d", out.Code, len(body.Data), want)
				}
				if want == 1 && (body.Data[0].Media.ID != media.ID || body.Data[0].Season != in.Season || body.Data[0].ID == "") {
					t.Fatal("membership returned wrong media or season")
				}
			}
			check(0)
			start := time.Now()
			out := send(in)
			elapsed := time.Since(start)
			var body struct {
				Data  Subscription
				Error APIError
			}
			if json.Unmarshal(out.Body.Bytes(), &body) != nil || out.Code != 200 || body.Data.ID == "" || out.Header().Get("Preference-Applied") != "respond-async" {
				t.Fatalf("save: HTTP %d, code %s", out.Code, body.Error.Code)
			}
			t.Logf("acknowledged committed %s in %s", media.MediaType, elapsed)
			job, r := uiSourceOnlyAdd(t, a)
			if job.State != "queued" || r.Stage != "saved" || r.Saved.ID != body.Data.ID || fixture.Sends.Load() != 0 || noticeV2AddedCount(t, a) != 0 {
				t.Fatal("acknowledged without durable handoff or waited for external effects")
			}
			check(1)
			claimed, err := a.claim(ctx)
			if err != nil || claimed.ID != job.ID {
				t.Fatalf("continuation not dispatchable: %v", err)
			}
			a.runClaimedJob(ctx, claimed)
			job, r = uiSourceOnlyAdd(t, a)
			if job.State != "succeeded" || r.Stage != "complete" || fixture.Sends.Load() != 1 || noticeV2AddedCount(t, a) != 1 || fixture.Metadata.Load() != 1 {
				t.Fatalf("continuation %s/%s, %s", job.State, r.Stage, job.ErrorCode)
			}
			// Duplicated submit still identifies the original row. A validation
			// error must roll back both the row change and the queued continuation.
			dup := send(in)
			var duplicate struct{ Data Subscription }
			if dup.Code != 200 || json.Unmarshal(dup.Body.Bytes(), &duplicate) != nil || duplicate.Data.ID != body.Data.ID {
				t.Fatal("duplicate changed the saved row")
			}
			check(1)
			in.StartEpisode = -1
			in.State = "invalid"
			if invalid := send(in); invalid.Code < 400 || invalid.Header().Get("Preference-Applied") != "" {
				t.Fatal("invalid mutation acknowledged")
			}
			job, r = uiSourceOnlyAdd(t, a)
			if job.State != "failed" || r.Saved != nil {
				t.Fatal("invalid submission left runnable work")
			}
			if err = a.cancelSubscription(ctx, body.Data.ID); err != nil {
				t.Fatal(err)
			}
			check(0)
		})
	}
}

func TestSubscriptionAddedReceiptAcceptsLegacyOnlyWhenCurrentMissing(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	const key = "owned-added-event"
	store := a.pluginStore
	legacy, err := store.seal("subscription_added", "system", key, []byte(`{"revision":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`INSERT INTO plugin_record(kind,id,payload,updated_at) VALUES('subscription_added',?,?,?)`, key, legacy, now()); err != nil {
		t.Fatal(err)
	}
	read := func() (json.RawMessage, error) {
		return store.readSubscriptionCompletionSealed(ctx, a.Store.reader(), "subscription_added", key)
	}
	if got, err := read(); err != nil || string(got) != `{"revision":1}` {
		t.Fatalf("legacy event: %s %v", got, err)
	}
	if _, err = a.Store.DB.Exec(recordInsertSQL("subscription_added", recordTableForKind("subscription_added")), key, []byte("invalid signature"), now()); err != nil {
		t.Fatal(err)
	}
	if _, err := read(); err == nil {
		t.Fatal("corrupt current event was masked by the historical copy")
	}
}
