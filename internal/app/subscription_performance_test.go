package app

import (
	"context"
	"fmt"
	"light-media/internal/catalog"
	"reflect"
	"strings"
	"testing"
	"time"
)

func BenchmarkSubscriptionCoverageLoaded(b *testing.B) {
	a := &App{}
	s := Subscription{Media: catalog.Media{ID: 42, MediaType: "tv"}, Season: 1, StartEpisode: 1}
	data := &subscriptionDisplayCoverage{dates: map[int]string{}}
	known := []int{}
	for ep := 1; ep <= 30; ep++ {
		known = append(known, ep)
		data.dates[ep] = "2025-01-01"
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.subscriptionCoverageLoaded(context.Background(), s, known, nil, data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSubscriptionCoverageExclusions(b *testing.B) {
	a, err := New(Options{DataDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	defer a.Close()
	tx, err := a.Store.DB.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		if _, err = tx.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES('exclusion',?,'{"blocked":true}','2026-01-01')`, fmt.Sprintf("tv:%d:1:1", i+100)); err != nil {
			b.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		b.Fatal(err)
	}
	s := Subscription{Media: catalog.Media{ID: 42, MediaType: "movie"}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err = a.subscriptionCoverage(context.Background(), s, nil); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

func TestSubscriptionCoverageSharedShanghaiClock(t *testing.T) {
	a := &App{}
	s := Subscription{Media: catalog.Media{ID: 42, MediaType: "tv"}, Season: 2, StartEpisode: 1, Excluded: []int{8}, EpisodeDates: map[int]string{3: "2026-11-13"}, Schedule: Schedule{Exceptions: []ScheduleException{{Date: "2026-11-10", Episode: 4, Action: "postpone", TargetDate: "2026-11-14"}}}}
	data := &subscriptionDisplayCoverage{library: map[int]bool{1: true}, reserved: map[int]bool{2: true}, pending: map[int]bool{2: true}, excluded: map[int]bool{5: true}, dates: map[int]string{1: "2026-11-01", 2: "2026-11-01", 3: "2026-11-01", 4: "2026-11-10", 6: "invalid", 7: "2026-11-12"}}
	known := []int{1, 2, 3, 4, 5, 6, 7, 8}
	at := time.Date(2026, 11, 12, 0, 5, 0, 0, shanghai()) // Still November 11 in UTC.
	got, err := a.subscriptionCoverageLoadedAt(context.Background(), s, known, nil, data, at)
	if err != nil {
		t.Fatal(err)
	}
	want := subscriptionCoverage{Known: known, Missing: []int{3, 4, 6, 7}, Library: []int{1}, Reserved: []int{2}, Excluded: []int{8, 5}, Unreleased: []int{3, 4}, UnknownDate: []int{6}, PendingImport: []int{2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coverage=%+v want=%+v", got, want)
	}
	if !reflect.DeepEqual(s.Excluded, []int{8}) || len(data.excluded) != 1 {
		t.Fatal("shared snapshot mutated")
	}
}

func TestSubscriptionCoverageExclusionRangeUsesExactSeasonAndIndex(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for key, blocked := range map[string]bool{"tv:42:1:1": true, "tv:42:1:2": false, "tv:42:10:2": true, "tv:420:1:2": true, "tv:42:1:99": true} {
		if err := a.Store.Put(ctx, "exclusion", key, map[string]bool{"blocked": blocked}); err != nil {
			t.Fatal(err)
		}
	}
	s := Subscription{Media: catalog.Media{ID: 42, MediaType: "tv"}, Season: 1, StartEpisode: 1}
	got, err := a.subscriptionCoverage(ctx, s, []int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Excluded, []int{1}) || !reflect.DeepEqual(got.Missing, []int{2}) {
		t.Fatalf("wrong exclusion scope: %+v", got)
	}
	rows, err := a.Store.reader().QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT id,payload FROM records WHERE kind='exclusion' AND id>=? AND id<?`, "tv:42:1:", "tv:42:1;")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var id, parent, unused int
		var plan string
		if err := rows.Scan(&id, &parent, &unused, &plan); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(plan, "id>?") && strings.Contains(plan, "id<?") {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("exclusion prefix did not use an indexed id range")
	}
}

func BenchmarkSubscriptionListFiftySeasons(b *testing.B) {
	a, err := New(Options{DataDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	for i := 1; i <= 50; i++ {
		sub := Subscription{ID: fmt.Sprint(i), State: "active", Media: catalog.Media{ID: int64(i), MediaType: "tv", Title: "Synthetic season"}, Season: 1, StartEpisode: 1}
		if err = a.Store.Put(ctx, "subscription", sub.ID, sub); err != nil {
			b.Fatal(err)
		}
		season := catalog.Season{}
		for ep := 1; ep <= 30; ep++ {
			season.Episodes = append(season.Episodes, catalog.Episode{EpisodeNumber: ep, AirDate: "2025-01-01"})
		}
		if err = a.Store.Put(ctx, "cache", fmt.Sprintf("season:%d:1", i), map[string]any{"data": season}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err = a.subscriptionList(ctx); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}
