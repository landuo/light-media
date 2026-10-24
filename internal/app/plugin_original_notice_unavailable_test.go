package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
)

type noticeUnavailableTransport func(*http.Request) (*http.Response, error)

func (f noticeUnavailableTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func noticeUnavailableNoHTTP(t *testing.T) *atomic.Int32 {
	t.Helper()
	calls := new(atomic.Int32)
	prior := http.DefaultTransport
	http.DefaultTransport = noticeUnavailableTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("owned removed notification must not send")
	})
	t.Cleanup(func() { http.DefaultTransport = prior })
	return calls
}

// A historical sealed row is input evidence, not a successful business receipt.
// No native owner, generation grant, source callback or delivery is fabricated.
func TestOriginalNoticeHistoricalNativeFirstAndReplayUnavailable(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	calls := noticeUnavailableNoHTTP(t)
	p := a.pluginStore
	fact := pluginNoticeRoute{Version: 1, Instance: "AutoBackup", Generation: 1, Token: "owned-retired-source", Kind: "auto_backup", CreatedAt: now(), CurrentTime: now()}
	fact.ID = p.noticeID("route", fact.Instance, fact.Generation, fact.Token)
	fact.LastSHA = noticeInitialSHA(fact)
	sealed, err := p.noticeSeal("route", fact.ID, fact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.ExecContext(ctx, `INSERT INTO plugin_notice_routes(id,instance_id,generation,source_token,sealed) VALUES(?,?,?,?,?)`, fact.ID, fact.Instance, fact.Generation, fact.Token, sealed); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		_, _, err = p.readNoticeRoute(ctx, a.Store.DB, fact.ID)
		if pluginErrorCodeInternal(err) != "PLUGIN_ADAPTER_UNAVAILABLE" {
			t.Fatal("history read", attempt, err)
		}
		err = a.runNoticeRoute(ctx, fact.ID)
		if pluginErrorCodeInternal(err) != "PLUGIN_ADAPTER_UNAVAILABLE" {
			t.Fatal("first/replay must preserve explicit unavailable", attempt, err)
		}
		var after []byte
		if err = a.Store.DB.QueryRowContext(ctx, `SELECT sealed FROM plugin_notice_routes WHERE id=?`, fact.ID).Scan(&after); err != nil || !bytes.Equal(after, sealed) {
			t.Fatal("retired source rewritten", attempt, err)
		}
	}
	for _, table := range []string{"plugin_notice_route_steps", "plugin_notice_publications", "plugin_message_queue", "plugin_runs", "jobs"} {
		var count int
		if err = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("retired route produced effects", table, count, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("retired route attempted external send", calls.Load())
	}
}

// This is the default sender boundary, not fabricated acceptance into a V2
// queue. Actual accepted V2 replay remains the separate standard Base contract.
func TestOriginalNoticeDefaultUnadaptedSenderNeverSends(t *testing.T) {
	a := newTestApp(t)
	calls := noticeUnavailableNoHTTP(t)
	if a.pluginStore.notices.send != nil {
		t.Fatal("default sender replaced by a test callback")
	}
	for attempt := 0; attempt < 2; attempt++ {
		modules, err := a.sendPluginMessageModules(context.Background(), pluginNoticeQueueEntry{})
		if modules != 0 || pluginErrorCodeInternal(err) != "PLUGIN_ADAPTER_UNAVAILABLE" {
			t.Fatal("unadapted sender claimed delivery", modules, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("default sender attempted network", calls.Load())
	}
}
