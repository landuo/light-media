package app

import (
	"context"
	"fmt"
	"light-media/internal/sites"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func siteMessageClosedFIFO(t *testing.T, a *App) *atomic.Int64 {
	t.Helper()
	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC).UnixNano())
	a.pluginStore.notices.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	noticePreferenceTest(t, a, "window", "", []any{map[string]any{"start": "12:00", "end": "12:00"}})
	if err := a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
		t.Fatal(err)
	}
	return clock
}

func siteMessageFIFOIDs(t *testing.T, a *App) []string {
	t.Helper()
	rows, err := a.Store.DB.Query(`SELECT id FROM plugin_message_queue ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func siteMessageOwnedFIFOSender(t *testing.T, a *App) func() []string {
	t.Helper()
	var mu sync.Mutex
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.Header.Get("X-Queue"))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	a.pluginStore.notices.send = func(ctx context.Context, q pluginNoticeQueueEntry) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("X-Queue", q.ID)
		response, err := server.Client().Do(req)
		if err != nil {
			return 0, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			return 0, fmt.Errorf("owned receiver status %d", response.StatusCode)
		}
		return 1, nil
	}
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), received...) }
}

func siteMessageColdDrainNoResend(t *testing.T, a *App) {
	t.Helper()
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	cold.pluginStore.notices.now = func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) }
	sent := siteMessageOwnedFIFOSender(t, cold)
	if err = cold.drainPluginMessages(context.Background(), 200); err != nil || len(sent()) != 0 {
		t.Fatal("cold terminal receipt resent or blocked FIFO", sent(), err)
	}
}

func TestSiteMessageAcceptedFIFOOutlivesLaterSourceFailureAndDoesNotBlockNext(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	clock := siteMessageClosedFIFO(t, a)
	sent := siteMessageOwnedFIFOSender(t, a)
	job := siteMessageClaimBatch(t, a)
	// Synthetic frozen DTO, genuine job grant, Router, history and delayed queue.
	first := siteMessageSeedCollectedDTO(t, a, job, "source-later-fails", &sites.SourceUserData{MessageUnread: 3, Ratio: .5, Messages: [][]any{{"accepted first", "date", "body"}, {"short"}, {"never", "date", "body"}}})
	failed, err := a.finishSiteMessageCollection(ctx, first.ID)
	if err == nil || failed.Stage != "failed" || failed.Failure != "ValueError" || failed.Steps[0].State != "complete" {
		t.Fatal("source did not fail after its first accepted post", failed, err)
	}
	if len(sent()) != 0 || len(siteMessageHistory(t, a)) != 1 {
		t.Fatal("closed window sent early or lost prior history", sent())
	}
	second := siteMessageSeedCollectedDTO(t, a, job, "later-valid", &sites.SourceUserData{MessageUnread: 1, Messages: [][]any{{"later valid", "date", "body"}}})
	complete, err := a.finishSiteMessageCollection(ctx, second.ID)
	if err != nil || complete.Stage != "complete" {
		t.Fatal("following source not accepted", complete.Stage, err)
	}
	ids := siteMessageFIFOIDs(t, a)
	if len(ids) != 2 || len(sent()) != 0 {
		t.Fatal("two source returns should leave two delayed FIFO entries", ids, sent())
	}
	for _, id := range ids {
		entry, err := a.pluginStore.readPluginMessage(ctx, a.Store.DB, id)
		if err != nil || entry.State != "queued" {
			t.Fatal("closed-window receipt missing", entry.State, err)
		}
	}
	clock.Store(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC).UnixNano())
	if err = a.drainPluginMessages(ctx, 200); err != nil {
		t.Fatal("later source failure revoked earlier accepted FIFO item", err)
	}
	if len(sent()) != 2 || sent()[0] != ids[0] || sent()[1] != ids[1] {
		t.Fatal("accepted first item did not send or blocked later FIFO", ids, sent())
	}
	for _, id := range ids {
		entry, err := a.pluginStore.readPluginMessage(ctx, a.Store.DB, id)
		if err != nil || entry.State != "consumed" {
			t.Fatal("successful delayed send lacks terminal proof", entry.State, err)
		}
	}
	if err = a.drainPluginMessages(ctx, 200); err != nil || len(sent()) != 2 {
		t.Fatal("completed FIFO item resent", sent(), err)
	}
	if got, err := a.pluginStore.readSiteMessageCollection(ctx, a.Store.DB, first.ID); err != nil || got.Stage != "failed" || got.Failure != "ValueError" {
		t.Fatal("delivery rewrote source exception", got.Stage, err)
	}
	siteMessageColdDrainNoResend(t, a)
}

func TestSiteMessageCoreRevokedQueuedGrantIsNotSentAndDoesNotBlockLaterFIFO(t *testing.T) {
	for _, mode := range []string{"disable_site", "connection"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
			calls := siteMessageHTTPFixture(t, a, false)
			ctx := context.Background()
			clock := siteMessageClosedFIFO(t, a)
			sent := siteMessageOwnedFIFOSender(t, a)
			changedURL := siteMessageRevokedConnectionFixture(t)
			job := siteMessageClaimBatch(t, a)
			err := a.runSiteRefresh(ctx, job)
			if err != nil || calls.Load() != 6 || len(sent()) != 0 {
				t.Fatal("real core job did not leave an accepted delayed message", calls.Load(), sent(), err)
			}
			old := siteMessageFIFOIDs(t, a)
			if len(old) != 1 {
				t.Fatal("missing original delayed queue", old)
			}
			a.settingsMu.Lock()
			settings := a.Settings()
			if mode == "connection" {
				settings.Sites[0].BaseURL = changedURL
			} else {
				settings.Sites[0].Enabled = false
			}
			err = a.saveSettings(ctx, settings)
			a.settingsMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			// A separate enabled site and core job grant remain valid. Never
			// re-enable the revoked source's site merely to build the next item.
			next := siteMessageSeedCollectedDTO(t, a, job, "independent-valid", &sites.SourceUserData{MessageUnread: 1, Messages: [][]any{{"independent next", "date", "body"}}})
			complete, err := a.finishSiteMessageCollection(ctx, next.ID)
			if err != nil || complete.Stage != "complete" {
				t.Fatal("independent queue source failed", complete.Stage, err)
			}
			ids := siteMessageFIFOIDs(t, a)
			if len(ids) != 2 || ids[0] != old[0] || len(sent()) != 0 {
				t.Fatal("FIFO setup changed original order", ids, sent())
			}
			clock.Store(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC).UnixNano())
			if err = a.drainPluginMessages(ctx, 200); err != nil {
				t.Fatal("definitively revoked item permanently blocked later FIFO", err)
			}
			if len(sent()) != 1 || sent()[0] != ids[1] || calls.Load() != 6 {
				t.Fatal("revoked item sent, valid successor blocked, or HTTP repeated", ids, sent(), calls.Load())
			}
			revoked, err := a.pluginStore.readPluginMessage(ctx, a.Store.DB, ids[0])
			if err != nil || revoked.State != "revoked" {
				t.Fatal("revocation lacks authenticated definitely-unsent terminal receipt", revoked.State, err)
			}
			valid, err := a.pluginStore.readPluginMessage(ctx, a.Store.DB, ids[1])
			if err != nil || valid.State != "consumed" {
				t.Fatal("valid successor did not complete", valid.State, err)
			}
			if err = a.drainPluginMessages(ctx, 200); err != nil || len(sent()) != 1 {
				t.Fatal("terminal revoked item retried or blocked repeated drain", sent(), err)
			}
			if len(siteMessageHistory(t, a)) != 2 {
				t.Fatal("queue revocation erased existing source history")
			}
			siteMessageColdDrainNoResend(t, a)
		})
	}
}

// A legal, independently owned endpoint for connection-revocation setup only.
// The core must reject the old grant before any request can reach this endpoint.
func siteMessageRevokedConnectionFixture(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("revoked site connection unexpectedly requested: %s", r.URL.Path)
		http.Error(w, "must not request revoked origin", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	return server.URL
}
