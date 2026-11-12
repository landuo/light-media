package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func subscriptionCheckJobCount(t *testing.T, a *App) int {
	t.Helper()
	var count int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='subscription' AND state IN ('queued','running','retry_wait','waiting_paused')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func requireDeferredSubscription(t *testing.T, a *App, s Subscription, before time.Time) time.Time {
	t.Helper()
	due, err := time.Parse(time.RFC3339Nano, s.NextCheck)
	if err != nil || !due.After(before) || s.LastCheck != "" {
		t.Fatalf("new subscription was already checked/due: %+v (%v)", s, err)
	}
	if got := subscriptionCheckJobCount(t, a); got != 0 {
		t.Fatalf("creation enqueued %d content checks", got)
	}
	return due
}

func TestNewSubscriptionFirstCheckUsesNormalCadence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		schedule Schedule
		state    string
		interval time.Duration
	}{
		{"fixed", Schedule{Mode: "fixed", IntervalMinutes: 17}, "active", 17 * time.Minute},
		{"metadata-fallback", Schedule{Mode: "follow", FallbackMinutes: 43}, "active", 43 * time.Minute},
		{"custom-active", Schedule{Mode: "follow", Source: "custom", Rule: "daily", Timezone: "UTC", Time: time.Now().UTC().Add(-time.Hour).Format("15:04"), WindowHours: 48, ActiveMinutes: 11}, "active", 11 * time.Minute},
		{"manual", Schedule{Mode: "manual"}, "active", 0},
		{"paused", Schedule{Mode: "fixed", IntervalMinutes: 17}, "paused", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := subscriptionTestApp(t)
			ctx := context.Background()
			in := subscriptionRequest(t)
			in.Schedule, in.State = tc.schedule, tc.state
			// Keep metadata outside the active window, independently of wall-clock date.
			season := catalog.Season{SeriesID: in.TMDBID, SeasonNumber: in.Season, Episodes: []catalog.Episode{{EpisodeNumber: 1, SeasonNumber: in.Season, AirDate: "2000-01-01"}}}
			if err := a.Store.Put(ctx, "cache", "season:206484:4", map[string]any{"at": time.Now(), "data": season}); err != nil {
				t.Fatal(err)
			}
			before := time.Now()
			s, err := a.saveSubscription(ctx, "", in)
			if err != nil {
				t.Fatal(err)
			}
			after := time.Now()
			if tc.interval == 0 {
				if s.NextCheck != "" || s.LastCheck != "" {
					t.Fatalf("disabled schedule got a check: %+v", s)
				}
				a.scheduleSubscriptions(ctx, after.Add(24*time.Hour))
				if subscriptionCheckJobCount(t, a) != 0 {
					t.Fatal("disabled subscription scheduled")
				}
				return
			}
			due := requireDeferredSubscription(t, a, s, before)
			if due.Before(before.Add(tc.interval)) || due.After(after.Add(tc.interval)) {
				t.Fatalf("due %s not within normal cadence [%s,%s]", due, before.Add(tc.interval), after.Add(tc.interval))
			}
			a.scheduleSubscriptions(ctx, due.Add(-time.Nanosecond))
			if subscriptionCheckJobCount(t, a) != 0 {
				t.Fatal("scheduler ran before first due time")
			}
			a.scheduleSubscriptions(ctx, due)
			if subscriptionCheckJobCount(t, a) != 1 {
				t.Fatal("scheduler did not admit first due check")
			}
			// Check completion advances with the same cadence instead of disabling it.
			if err := a.recordSubscriptionCheck(ctx, s, "owned check completed", nil); err != nil {
				t.Fatal(err)
			}
			var updated Subscription
			if err := a.Store.Get(ctx, "subscription", s.ID, &updated); err != nil {
				t.Fatal(err)
			}
			next, err := time.Parse(time.RFC3339Nano, updated.NextCheck)
			if err != nil || !next.After(time.Now()) || updated.LastCheck == "" {
				t.Fatalf("normal subsequent cadence lost: %+v", updated)
			}
		})
	}
}

func TestNewSubscriptionFirstCheckUsesKnownMetadataWindowAndExceptions(t *testing.T) {
	for _, mode := range []string{"metadata", "override", "skipped"} {
		t.Run(mode, func(t *testing.T) {
			skip := mode == "skipped"
			a := subscriptionTestApp(t)
			ctx := context.Background()
			in := subscriptionRequest(t)
			today := time.Now().UTC().Format("2006-01-02")
			airDate := ""
			if mode == "metadata" {
				airDate = today
			}
			if err := a.Store.Put(ctx, "cache", "season:206484:4", map[string]any{"at": time.Now(), "data": catalog.Season{SeriesID: in.TMDBID, SeasonNumber: in.Season, Episodes: []catalog.Episode{{EpisodeNumber: 1, SeasonNumber: in.Season, AirDate: airDate}}}}); err != nil {
				t.Fatal(err)
			}
			in.Schedule = Schedule{Mode: "follow", Timezone: "UTC", Time: "00:00", WindowHours: 48, ActiveMinutes: 7, FallbackMinutes: 73}
			if mode != "metadata" {
				in.EpisodeDates = map[int]string{1: today}
			}
			if skip {
				in.Schedule.Exceptions = []ScheduleException{{Date: today, Action: "skip", Episode: 1}}
			}
			before := time.Now()
			s, err := a.saveSubscription(ctx, "", in)
			if err != nil {
				t.Fatal(err)
			}
			due := requireDeferredSubscription(t, a, s, before)
			interval := 7 * time.Minute
			if skip {
				interval = 73 * time.Minute
			}
			if due.Before(before.Add(interval)) || due.After(time.Now().Add(interval)) {
				t.Fatalf("wrong metadata cadence: %s", due)
			}
		})
	}
}

func TestNewSubscriptionHTTPSeasonBatchDefersButExplicitCheckStillEnqueues(t *testing.T) {
	a, in, fixture := uiSourceAddFixture(t)
	ctx := context.Background()
	subscriptionStatsInstallSettings(t, a, "", false)
	if err := a.bootstrapAdmin("owned-admin", "owned-password-only-test"); err != nil {
		t.Fatal(err)
	}
	token := "owned-deferred-check-session"
	if _, err := a.Store.DB.Exec(`INSERT INTO sessions(token_hash,expires_at) VALUES(?,?)`, tokenHash(token), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	post := func(path string, body any) []byte {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, server.URL+"/api/"+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(sessionCookie(req, token, 3600))
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result json.RawMessage
		if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status=%d body=%s", path, resp.StatusCode, result)
		}
		return result
	}
	var first Subscription
	for _, season := range []int{4, 5} {
		// Both bulk/explore and the season dialog issue this same POST for each row.
		in.Season = season
		in.TotalEpisodes = 1
		in.Schedule = Schedule{Mode: "fixed", IntervalMinutes: 30}
		if err := a.Store.Put(ctx, "cache", "season:206484:"+jsonNumber(int64(season)), map[string]any{"at": time.Now(), "data": catalog.Season{SeriesID: in.TMDBID, SeasonNumber: season, Episodes: []catalog.Episode{{SeasonNumber: season, EpisodeNumber: 1, AirDate: "2000-01-01"}}}}); err != nil {
			t.Fatal(err)
		}
		before := time.Now()
		post("subscriptions", in)
		_, receipt := uiSourceOnlyAdd(t, a)
		if receipt.Saved == nil || receipt.Stage != "complete" {
			t.Fatal("creation not completed")
		}
		s := *receipt.Saved
		requireDeferredSubscription(t, a, s, before)
		if first.ID == "" {
			first = s
		}
	}
	if fixture.Sends.Load() != 2 || noticeV2AddedCount(t, a) != 2 {
		t.Fatal("creation notices/Added events lost")
	}
	a.scheduleSubscriptions(ctx, time.Now())
	if subscriptionCheckJobCount(t, a) != 0 {
		t.Fatal("season batch checked on next scheduler tick")
	}
	// Retrying a source-compatible create must not pull the existing due date forward.
	var existing Subscription
	_, last := uiSourceOnlyAdd(t, a)
	old := *last.Saved
	post("subscriptions", in)
	if err := a.Store.Get(ctx, "subscription", old.ID, &existing); err != nil {
		t.Fatal(err)
	}
	if existing.NextCheck != old.NextCheck || existing.Revision != old.Revision {
		t.Fatal("duplicate creation changed existing schedule")
	}
	post("subscriptions/"+first.ID+"/check", map[string]any{})
	if subscriptionCheckJobCount(t, a) != 1 {
		t.Fatal("explicit manual check was suppressed by future due time")
	}
}
