package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestSubscriptionPollAdmission(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 1, 0, 0, 0, 123, time.UTC)
	future := at.Add(time.Nanosecond).Format(time.RFC3339Nano)
	for i, tc := range []struct{ id, state, mode, next, reason string }{
		{"boundary", "active", "fixed", at.In(time.FixedZone("east", 8*3600)).Format(time.RFC3339Nano), ""},
		{"future", "active", "fixed", future, ""},
		{"manual", "active", "manual", "", ""},
		{"paused", "paused", "fixed", "", ""},
		{"legacy", "active", "follow", future, "站点调用预算不足，已按最早恢复时间延期检查"},
		{"legacy-partial", "active", "follow", future, "已检查；部分站点预算不足，检查将按恢复时间延期"},
		{"invalid-clock", "active", "fixed", "yesterday", ""},
	} {
		s := Subscription{ID: tc.id, Revision: i + 1, State: tc.state, Schedule: Schedule{Mode: tc.mode}, NextCheck: tc.next, LastReason: tc.reason}
		if err := a.Store.Put(ctx, "subscription", tc.id, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Store.Put(ctx, "subscription", "bad-episodes", json.RawMessage(`{"id":"bad-episodes","state":"active","episodes":"not an array"}`)); err != nil {
		t.Fatal(err)
	}
	a.scheduleSubscriptions(ctx, at)
	a.scheduleSubscriptions(ctx, at) // Existing queued jobs remain deduplicated.
	rows, err := a.Store.DB.Query(`SELECT json_extract(payload,'$.id'),json_extract(payload,'$.revision') FROM jobs WHERE kind='subscription' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []subscriptionPollJob
	for rows.Next() {
		var j subscriptionPollJob
		if err := rows.Scan(&j.ID, &j.Revision); err != nil {
			t.Fatal(err)
		}
		got = append(got, j)
	}
	want := []subscriptionPollJob{{"boundary", 1}, {"invalid-clock", 7}, {"legacy", 5}, {"legacy-partial", 6}}
	if err := rows.Err(); err != nil || !slices.Equal(got, want) {
		t.Fatalf("queued %v, want %v: %v", got, want, err)
	}
}

func TestSubscriptionPollWindowBeforeAdmission(t *testing.T) {
	a := subscriptionTestApp(t)
	// 501 tied timestamps: the last record by uid must not enter the tick
	// even when all the records ahead of it except one are paused.
	_, err := a.Store.DB.Exec(`WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<500)
		INSERT INTO subscribe(uid,record_kind,payload,updated_at)
		SELECT printf('sub-%03d',i),'subscription',json_object('id',printf('sub-%03d',i),'state',CASE WHEN i>=499 THEN 'active' ELSE 'paused' END),'same' FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	due, err := a.dueSubscriptionChecks(context.Background(), time.Now())
	if err != nil || !slices.Equal(due, []subscriptionPollJob{{ID: "sub-499"}}) {
		t.Fatalf("unexpected poll window: %v (%v)", due, err)
	}
}

// Every subscription is active but not due: a normal 15-second poll must do
// the read/check work without creating a job or contacting a provider.
func BenchmarkSubscriptionIdlePoll(b *testing.B) {
	store, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	a := &App{Store: store, control: "running"}
	ctx := context.Background()
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	s := Subscription{State: "active", Schedule: Schedule{Mode: "fixed"}, NextCheck: at.Add(time.Hour).Format(time.RFC3339), EpisodeDates: map[int]string{}}
	for episode := 1; episode <= 24; episode++ {
		s.Episodes = append(s.Episodes, episode)
		s.EpisodeDates[episode] = at.AddDate(0, 0, episode).Format("2006-01-02")
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	for i := range 500 {
		s.ID = fmt.Sprintf("poll-%03d", i)
		if err := txPut(ctx, tx, "subscription", s.ID, s); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		a.scheduleSubscriptions(ctx, at)
	}
	var count int
	if err := store.DB.QueryRow(`SELECT count(*) FROM jobs`).Scan(&count); err != nil || count != 0 {
		b.Fatalf("idle poll created %d jobs: %v", count, err)
	}
}
