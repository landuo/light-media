package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/statistic"
)

// Expected URL cases are synthetic applications of the publicly visible v2
// MP_DOMAIN/UrlUtils implementation. They are NOT an installed-core oracle.
func TestPluginDefaultLinkPublicSourceCases(t *testing.T) {
	fragment := "#/plugins?tab=installed&id=SiteStatistic"
	for _, tc := range []struct {
		domain string
		want   any
	}{
		{"", nil},
		{"https://synthetic.invalid", "https://synthetic.invalid/" + fragment},
		{"http://synthetic.invalid/", "http://synthetic.invalid/" + fragment},
		{"synthetic.invalid/base", "http://synthetic.invalid/base/" + fragment},
		{"https://synthetic.invalid/base#old", "https://synthetic.invalid/base" + fragment},
		{"https://synthetic.invalid/base?x=1", "https://synthetic.invalid/base?x=1%2F" + fragment},
		{"https://synthetic.invalid/base?z=2&a=hello%20world&z=3&empty=#old", "https://synthetic.invalid/base?z=2&z=3&a=hello+world" + fragment},
		{"https://synthetic.invalid/base?blank=&flag#old", "https://synthetic.invalid/base" + fragment},
		{"https://synthetic.invalid/base?z=1;b=2&x=%ZZ&x=%FF#old", "https://synthetic.invalid/base?z=1%3Bb%3D2&x=%25ZZ&x=%EF%BF%BD" + fragment},
		{"https://synthetic.invalid/a/../base#old", "https://synthetic.invalid/a/../base" + fragment},
		{"https://synthetic.invalid/base?x=%E2%82&x=%FF%FF#old", "https://synthetic.invalid/base?x=%EF%BF%BD&x=%EF%BF%BD%EF%BF%BD" + fragment},
	} {
		t.Run(tc.domain, func(t *testing.T) {
			if got, err := sourcePluginDefaultLink(tc.domain, "SiteStatistic", ""); err != nil || got != tc.want {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
	for _, domain := range []string{"", "https://synthetic.invalid"} {
		if got, err := sourcePluginDefaultLink(domain, "SiteStatistic", "https://explicit.invalid/page#keep"); err != nil || got != "https://explicit.invalid/page#keep" {
			t.Fatal("explicit link overwritten", got)
		}
	}
}

func defaultLinkSettings(t *testing.T, a *App, domain string) {
	t.Helper()
	settings := a.Settings()
	settings.AppDomain = domain
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
}
func defaultLinkAccept(t *testing.T, a *App, instance, key, fingerprint string) (PluginRun, siteStatisticNoticeReceipt) {
	t.Helper()
	statisticRouterEvent(t, a, key)
	run := statisticRouterClaim(t, a, instance)
	raw, err := a.commitSiteStatisticNotice(context.Background(), run, &statistic.Notification{Title: "合成统计", Text: key, Fingerprint: strings.Repeat(fingerprint, 64)}, "all", time.Now().In(shanghai()))
	var receipt siteStatisticNoticeReceipt
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.RouteID == "" {
		t.Fatal("acceptance", err, string(raw))
	}
	return run, receipt
}
func defaultLinkRead(t *testing.T, a *App, id string) (pluginNoticeRoute, siteStatisticNotice) {
	t.Helper()
	route, _, err := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, id)
	if err != nil {
		t.Fatal(err)
	}
	box, err := a.pluginStore.siteStatisticSourceBox(context.Background(), a.Store.DB, route)
	if err != nil {
		t.Fatal(err)
	}
	return route, box
}
func defaultLinkCheck(t *testing.T, a *App, id string, want any) {
	t.Helper()
	route, box := defaultLinkRead(t, a, id)
	if err := a.pluginStore.validateSiteStatisticNotice(context.Background(), a.Store.DB, route); err != nil {
		t.Fatal(err)
	}
	var history Notification
	if err := a.Store.Get(context.Background(), "notification", box.HistoryID, &history); err != nil {
		t.Fatal(err)
	}
	wantHistory, _ := want.(string)
	if history.Link != wantHistory {
		t.Fatal("history link differs", history.Link, wantHistory)
	}
	for _, raw := range []json.RawMessage{route.Envelope, box.Envelope} {
		var env map[string]any
		if json.Unmarshal(raw, &env) != nil || env["link"] != want {
			t.Fatal("frozen link mismatch", string(raw), want)
		}
	}
}

func TestSiteStatisticDefaultLinkFrozenColdReplayAndNewEvent(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	a, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close() }()
	statisticRouterSnapshot(t, a)
	statisticRouterInstall(t, a, "SiteStatistic")
	defaultLinkSettings(t, a, "https://first.invalid/base")
	first, receipt := defaultLinkAccept(t, a, "SiteStatistic", "link-first", "1")
	_, box := defaultLinkRead(t, a, receipt.RouteID)
	if box.Version != 2 || box.AppDomain == nil || *box.AppDomain != "https://first.invalid/base" {
		t.Fatal("domain not frozen", box.Version, box.AppDomain)
	}
	defaultLinkSettings(t, a, "https://second.invalid")
	// A repeated accepted host call must return the same receipt, without
	// recapturing the new Settings or creating another route.
	raw, err := a.commitSiteStatisticNotice(ctx, first, &box.Payload, "all", time.Now().In(shanghai()))
	var replay siteStatisticNoticeReceipt
	if err != nil || json.Unmarshal(raw, &replay) != nil || replay != receipt {
		t.Fatal("acceptance replay changed", err)
	}
	want := "https://first.invalid/base/#/plugins?tab=installed&id=SiteStatistic"
	defaultLinkCheck(t, a, receipt.RouteID, want)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	defaultLinkCheck(t, a, receipt.RouteID, want)
	if err = a.resumePluginNoticeRoutes(ctx, 200); err != nil {
		t.Fatal(err)
	}
	if err = a.runNoticeRoute(ctx, receipt.RouteID); err != nil {
		t.Fatal(err)
	}
	defaultLinkCheck(t, a, receipt.RouteID, want)
	if sends.Load() != 1 {
		t.Fatal("cold/replay send count", sends.Load())
	}
	if err = a.pluginStore.recoverPluginRuns(ctx); err != nil {
		t.Fatal(err)
	}
	_, next := defaultLinkAccept(t, a, "SiteStatistic", "link-second", "2")
	defaultLinkCheck(t, a, next.RouteID, "https://second.invalid/#/plugins?tab=installed&id=SiteStatistic")
	if err = a.runNoticeRoute(ctx, next.RouteID); err != nil {
		t.Fatal(err)
	}
	if sends.Load() != 2 {
		t.Fatal("new event send count", sends.Load())
	}
}

func TestSiteStatisticDefaultLinkCloneUsesClass(t *testing.T) {
	a := newTestApp(t)
	statisticRouterSnapshot(t, a)
	for _, instance := range []string{"SiteStatistic", "SiteStatistic_owned_clone"} {
		statisticRouterInstall(t, a, instance)
	}
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	defaultLinkSettings(t, a, "synthetic.invalid/base")
	statisticRouterEvent(t, a, "link-clones")
	for _, instance := range []string{"SiteStatistic", "SiteStatistic_owned_clone"} {
		run := statisticRouterExecute(t, a, statisticRouterClaim(t, a, instance))
		receipt := statisticRouterReceipt(t, a, run)
		defaultLinkCheck(t, a, receipt.RouteID, "http://synthetic.invalid/base/#/plugins?tab=installed&id=SiteStatistic")
	}
}

// Rewrite only synthetic sealed test facts; corruption is deliberately resealed
// so that semantic source/route binding, not just AEAD, must reject it.
func defaultLinkWriteBox(t *testing.T, a *App, route pluginNoticeRoute, box siteStatisticNotice) []byte {
	t.Helper()
	raw, err := json.Marshal(box)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.pluginStore.seal("run_result", box.Instance, "site-statistic-notice:"+box.Token, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_site_statistic_notices SET sealed=? WHERE token=?`, sealed, route.Token); err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestSiteStatisticDefaultLinkCorruptionFailsClosed(t *testing.T) {
	for _, target := range []string{"domain", "missing_domain", "envelope", "route_envelope", "history_link", "version"} {
		t.Run(target, func(t *testing.T) {
			a := newTestApp(t)
			statisticRouterSnapshot(t, a)
			statisticRouterInstall(t, a, "SiteStatistic")
			defaultLinkSettings(t, a, "https://first.invalid")
			_, receipt := defaultLinkAccept(t, a, "SiteStatistic", "link-corrupt", "3")
			route, box := defaultLinkRead(t, a, receipt.RouteID)
			switch target {
			case "domain":
				changed := "https://tampered.invalid"
				box.AppDomain = &changed
			case "missing_domain":
				box.AppDomain = nil
			case "version":
				box.Version = 1
			case "history_link":
				var history Notification
				if err := a.Store.Get(context.Background(), "notification", box.HistoryID, &history); err != nil {
					t.Fatal(err)
				}
				history.Link = "https://tampered.invalid"
				if err := a.Store.Put(context.Background(), "notification", box.HistoryID, history); err != nil {
					t.Fatal(err)
				}
			case "envelope":
				box.Envelope = bytes.ReplaceAll(box.Envelope, []byte("first.invalid"), []byte("tampered.invalid"))
			case "route_envelope":
				route.Envelope = bytes.ReplaceAll(route.Envelope, []byte("first.invalid"), []byte("tampered.invalid"))
			}
			defaultLinkWriteBox(t, a, route, box)
			sealed, err := a.pluginStore.noticeSeal("route", route.ID, route)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`UPDATE plugin_notice_routes SET sealed=? WHERE id=?`, sealed, route.ID); err != nil {
				t.Fatal(err)
			}
			var sends atomic.Int32
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if err = a.runNoticeRoute(context.Background(), route.ID); err == nil {
				t.Fatal("tampering accepted")
			}
			if sends.Load() != 0 || statisticRouterCount(t, a, "plugin_notice_publications") != 0 {
				t.Fatal("tampering escaped", sends.Load())
			}
		})
	}
}

func TestSiteStatisticDefaultLinkEmptyAndLegacyV1Cold(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "v2-empty", true: "v1-absent"}[legacy], func(t *testing.T) {
			ctx := context.Background()
			data := t.TempDir()
			a, err := New(Options{DataDir: data})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { a.Close() }()
			statisticRouterSnapshot(t, a)
			statisticRouterInstall(t, a, "SiteStatistic")
			run, receipt := defaultLinkAccept(t, a, "SiteStatistic", "link-legacy", "4")
			route, box := defaultLinkRead(t, a, receipt.RouteID)
			if legacy {
				// Reconstruct the released v1 representation, including omission of the
				// new field, and its original acceptance hashes before any route runs.
				box.Version = 1
				box.AppDomain = nil
				raw := defaultLinkWriteBox(t, a, route, box)
				if bytes.Contains(raw, []byte("AppDomain")) {
					t.Fatal("v1 serialization changed")
				}
				route.AcceptedSHA = tokenHash(string(raw))
				receipt.InitialSHA = noticeInitialSHA(route)
				sealed, err := a.pluginStore.noticeSeal("route", route.ID, route)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.Store.DB.Exec(`UPDATE plugin_notice_routes SET sealed=? WHERE id=?`, sealed, route.ID); err != nil {
					t.Fatal(err)
				}
				receiptRaw, _ := json.Marshal(receipt)
				sealed, err = a.pluginStore.seal("site_statistic_result", run.InstanceID, run.ID, receiptRaw)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.Store.DB.Exec(`UPDATE plugin_site_statistic_operations SET result=? WHERE run_id=?`, sealed, run.ID); err != nil {
					t.Fatal(err)
				}
			} else if box.AppDomain == nil || *box.AppDomain != "" {
				t.Fatal("empty domain not frozen")
			}
			defaultLinkSettings(t, a, "https://later.invalid")
			defaultLinkCheck(t, a, receipt.RouteID, nil)
			var sends atomic.Int32
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if err = a.runNoticeRoute(ctx, route.ID); err != nil {
				t.Fatal(err)
			}
			if sends.Load() != 1 {
				t.Fatal("initial route count", sends.Load())
			}
			_, before := defaultLinkRead(t, a, receipt.RouteID)
			beforeRaw, _ := json.Marshal(before)
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = New(Options{DataDir: data})
			if err != nil {
				t.Fatal(err)
			}
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if err = a.resumePluginNoticeRoutes(ctx, 200); err != nil {
				t.Fatal(err)
			}
			if err = a.runNoticeRoute(ctx, route.ID); err != nil {
				t.Fatal(err)
			}
			defaultLinkCheck(t, a, receipt.RouteID, nil)
			_, after := defaultLinkRead(t, a, receipt.RouteID)
			afterRaw, _ := json.Marshal(after)
			if !bytes.Equal(beforeRaw, afterRaw) || sends.Load() != 1 || statisticRouterCount(t, a, "plugin_notice_routes") != 1 {
				t.Fatal("old fact upgraded or resent", sends.Load())
			}
		})
	}
}

func TestSiteStatisticDefaultLinkConstructionFailureRollsBack(t *testing.T) {
	a := newTestApp(t)
	statisticRouterSnapshot(t, a)
	statisticRouterInstall(t, a, "SiteStatistic")
	defaultLinkSettings(t, a, "https://[malformed.invalid")
	statisticRouterEvent(t, a, "link-invalid-domain")
	run := statisticRouterClaim(t, a, "SiteStatistic")
	_, err := a.commitSiteStatisticNotice(context.Background(), run, &statistic.Notification{Title: "合成统计", Text: "invalid domain", Fingerprint: strings.Repeat("5", 64)}, "all", time.Now().In(shanghai()))
	if err == nil {
		t.Fatal("invalid domain silently accepted")
	}
	for _, table := range []string{"plugin_site_statistic_notices", "plugin_site_statistic_operations", "plugin_notice_routes", "plugin_notice_publications", "plugin_message_queue"} {
		if statisticRouterCount(t, a, table) != 0 {
			t.Fatal("failed link escaped transaction", table)
		}
	}
	if _, err = a.pluginStore.Data(context.Background(), run.InstanceID, "last_notify"); !missingPluginValue(err) {
		t.Fatal("failed link advanced fingerprint", err)
	}
}
