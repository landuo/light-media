package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/builtin"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	"light-media/internal/plugins/schedule"
)

// Called after the actual qB/TR effect lost its ACK. RemoteIdentifiers uses the
// real public configuration, persisted schedule, SDK worker and HTTP consumer.
func iyuuAssertUnknownDispatchDoesNotStarve(t *testing.T, a *App, cookie *http.Cookie, unknown PluginRun) {
	t.Helper()
	ctx := context.Background()
	useBuiltInPluginProcess(t, a)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "OwnedUnknownQueue => OwnedStillRuns\n")
	}))
	defer server.Close()
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"RemoteIdentifiers"}`, cookie, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	identifiersExecuteNext(t, a)
	identifiersConfigure(t, a, cookie, 1, builtin.RemoteIdentifiersConfiguration{Enable: true, Flitter: true, Cron: "0 0 * * *", FileURLs: server.URL + "/words"})
	identifiersExecuteNext(t, a)
	var dueText string
	if e := a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id='RemoteIdentifiers' AND service_id='RemoteIdentifiers'`).Scan(&dueText); e != nil {
		t.Fatal(e)
	}
	at, e := time.Parse(time.RFC3339Nano, dueText)
	if e != nil {
		t.Fatal(e)
	}
	// Both source IYUU services precede the unrelated service. They are also
	// beyond their grace, proving unknown cannot be consumed as a missed tick.
	if _, e = a.Store.DB.Exec(`UPDATE plugin_schedules SET next_due=? WHERE instance_id=?`, schedule.FormatTime(at.Add(-2*time.Second)), unknown.InstanceID); e != nil {
		t.Fatal(e)
	}
	type originalSchedule struct{ ID, Due string }
	snapshot := func() ([]originalSchedule, int, int, []byte) {
		t.Helper()
		rows, e := a.Store.DB.Query(`SELECT service_id,next_due FROM plugin_schedules WHERE instance_id=? ORDER BY service_id`, unknown.InstanceID)
		if e != nil {
			t.Fatal(e)
		}
		out := []originalSchedule{}
		for rows.Next() {
			var s originalSchedule
			if e = rows.Scan(&s.ID, &s.Due); e != nil {
				t.Fatal(e)
			}
			out = append(out, s)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(e)
		}
		var runs, occurrences int
		if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, unknown.InstanceID).Scan(&runs); e != nil {
			t.Fatal(e)
		}
		if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE instance_id=?`, unknown.InstanceID).Scan(&occurrences); e != nil {
			t.Fatal(e)
		}
		old, e := a.pluginStore.Run(ctx, unknown.ID)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := json.Marshal(old)
		if e != nil {
			t.Fatal(e)
		}
		return out, runs, occurrences, raw
	}
	before, runs, occurrences, oldRun := snapshot()
	if len(before) != 2 {
		t.Fatal("actual source schedules missing", before)
	}
	for attempt := 0; attempt < 2; attempt++ {
		n, e := a.pluginStore.enqueueDuePluginSchedules(ctx, at)
		want := 0
		if attempt == 0 {
			want = 1
		}
		if e != nil || n != want {
			t.Fatal("unknown earliest instance starved unrelated service", attempt, n, e)
		}
		after, nextRuns, nextOccurrences, nextRun := snapshot()
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(after)
		if string(beforeJSON) != string(afterJSON) || runs != nextRuns || occurrences != nextOccurrences || string(oldRun) != string(nextRun) {
			t.Fatal("dispatch consumed unknown due/occurrence/run", before, after, runs, nextRuns, occurrences, nextOccurrences)
		}
	}
	var pending int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='RemoteIdentifiers' AND state='pending'`).Scan(&pending); e != nil || pending != 1 {
		t.Fatal("real unrelated source not queued", pending, e)
	}
	got := identifiersExecuteNext(t, a)
	_, words, e := a.recognitionWords(ctx)
	if e != nil || got.Action != "service" || requests.Load() != 1 || len(words) != 2 || words[1] != "OwnedUnknownQueue => OwnedStillRuns" {
		t.Fatal("unrelated source did not execute real SDK/HTTP business", got.Action, requests.Load(), words, e)
	}
}

func TestIYUUUnknownSchedulerRejectionRequiresExactCause(t *testing.T) {
	for _, e := range []error{iyuu.ErrJournalIntegrity, iyuu.ErrReplayDiverged, errors.New("SQL owned failure"), bad("IYUU_UNCONFIRMED", "code alone"), fmt.Errorf("%w: %w", bad("IYUU_UNCONFIRMED", "owned"), errors.Join(iyuu.ErrUnconfirmed, iyuu.ErrJournalIntegrity))} {
		if iyuuUnconfirmedQueueAdmission(e) {
			t.Fatal("non-safe rejection was swallowed", e)
		}
	}
	if !iyuuUnconfirmedQueueAdmission(fmt.Errorf("%w: %w", bad("IYUU_UNCONFIRMED", "owned"), iyuu.ErrUnconfirmed)) {
		t.Fatal("validated unknown cause lost")
	}
}

// The root fact set is independent of the mutable App ledger. A missing fact's
// row must still be diagnosed after another accepted run is already unknown.
func iyuuAssertUnknownDoesNotHideMissingRootFact(t *testing.T, a *App, run PluginRun) {
	t.Helper()
	ctx := context.Background()
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	root, _, e := a.pluginStore.iyuuRootReadTx(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		t.Fatal(e)
	}
	original, e := a.pluginStore.iyuuTransitionParentTx(ctx, tx, run.InstanceID)
	if !errors.Is(e, iyuu.ErrUnconfirmed) {
		t.Fatal("owned unknown baseline", original, e)
	}
	// Copy the independently verified root, representing loss of one previously
	// authenticated App row without altering the live owned fixture's evidence.
	missing := root.Runs[run.ID]
	missing.Run = "owned-missing-original-app-row"
	root.Runs[missing.Run] = missing
	if e = a.pluginStore.iyuuValidateDeferredTx(ctx, tx, root); !errors.Is(e, iyuu.ErrJournalIntegrity) {
		t.Fatal("unknown hid missing accepted run fact", e)
	}
}
