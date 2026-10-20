package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/notice"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

func noticeV2SubscriptionFixture(t *testing.T) (*App, *pluginLarkCoreExecution, context.Context, SubscriptionRequest, Subscription, *noticeSourceFixture) {
	t.Helper()
	a := subscriptionTestApp(t)
	core, ctx, s, facts := sourceSubscriptionFixture(t, a)
	fixture := installNoticeSourceFixtures(t, a, facts.Media, 4, 5)
	request, e := a.larkSourceSubscriptionRequest(ctx, core, s, facts, s.User, "N", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := a.larkSaveSourceSubscription(ctx, core, request)
	if e != nil {
		t.Fatal(e)
	}
	return a, core, ctx, request, saved, fixture
}
func noticeV2OnlyRoute(t *testing.T, a *App) pluginNoticeRoute {
	t.Helper()
	ids := noticeRouteTestIDs(t, a, "plugin_notice_routes")
	if len(ids) != 1 {
		t.Fatal("expected one actual business route", len(ids))
	}
	r, _, e := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, ids[0])
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func noticeV2AddedCount(t *testing.T, a *App) int {
	t.Helper()
	var n int
	if e := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription_added'`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestPluginNoticeV2ActualTemplateHistoryReceiptColdAndLostACK(t *testing.T) {
	for _, boundary := range []string{"none", "business_accept", "route_step", "route_finish", "queue_intent", "queue_finish"} {
		t.Run(boundary, func(t *testing.T) {
			a, core, ctx, request, saved, fixture := noticeV2SubscriptionFixture(t)
			var lost atomic.Bool
			a.pluginStore.notices.afterCommit = func(actual string) error {
				if actual == boundary && lost.CompareAndSwap(false, true) {
					return errors.New("owned reply lost after real SQL Commit")
				}
				return nil
			}
			ordinal := core.ordinal
			if e := a.larkCompleteSourceSubscription(ctx, core, request, saved); e != nil {
				t.Fatal(e)
			}
			if boundary != "none" && !lost.Load() {
				t.Fatal("fixture did not reach actual commit", boundary)
			}
			r := noticeV2OnlyRoute(t, a)
			rendered, e := a.pluginStore.noticeBusinessRender(ctx, r.ID)
			if e != nil || rendered.SourceCache == nil || rendered.RenderSourceError != "" || !r.Complete || r.SourceError != "" || r.Count < 4 || r.LastSHA == "" {
				t.Fatal("no authenticated render/route completion", r, e)
			}
			title, _ := rendered.Snapshot.Fields["title"].(string)
			if title != "诛仙 (2022) S04 已添加订阅" {
				t.Fatal("original source template was replaced", title)
			}
			histories, e := a.Store.List(ctx, "notification", 10, 0)
			if e != nil || len(histories) != 1 || fixture.Sends.Load() != 1 || noticeV2AddedCount(t, a) != 1 {
				t.Fatal("actual history/send/source event changed", len(histories), fixture.Sends.Load(), e)
			}
			if got := a.pluginStore.noticeTemplateHelper().Cache.Get(rendered.SourceCache.Key); got == nil {
				t.Fatal("actual helper cache trace missing")
			}
			dir := a.Dir
			core.release()
			if e = a.Close(); e != nil {
				t.Fatal(e)
			}
			cold, e := New(Options{DataDir: dir})
			if e != nil {
				t.Fatal(e)
			}
			defer cold.Close()
			cold.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) {
				t.Fatal("cold replay resent consumed notice")
				return 0, seedhost.ErrUnsafe
			}
			coldJob, e := cold.claim(context.Background())
			if e == nil && coldJob.Kind == "subscription_stats" {
				// This is a real admitted child, not a competing source parent.
				// Run its missing-settings branch through the actual dispatcher.
				if childErr := cold.safeRun(context.Background(), coldJob); pluginErrorCodeInternal(childErr) != "SUBSCRIPTION_STATS_DEPENDENCY" {
					t.Fatal("cold statistics child did not retain independent dependency", childErr)
				}
				if _, e = cold.Store.DB.Exec(`UPDATE jobs SET state='failed' WHERE id=?`, coldJob.ID); e != nil {
					t.Fatal(e)
				}
				coldJob, e = cold.claim(context.Background())
			}
			if e != nil || coldJob.ID != core.job.ID {
				t.Fatal("cold source parent was not claimed from recovered real jobs", coldJob.ID, e)
			}
			coldCore := &pluginLarkCoreExecution{a: cold, job: coldJob}
			if _, e = cold.larkInboxForJob(context.Background(), coldJob, core.inbox.ID, coldCore); e != nil {
				t.Fatal(e)
			}
			defer coldCore.release()
			coldCore.ordinal = ordinal
			coldCtx := context.WithValue(context.Background(), pluginLarkCoreKey{}, coldCore)
			if e = cold.larkCompleteSourceSubscription(coldCtx, coldCore, request, saved); e != nil {
				t.Fatal("cold original sequence did not replay frozen receipts", e)
			}
			if e = cold.runNoticeRoute(coldCtx, r.ID); e != nil {
				t.Fatal(e)
			}
			got, e := cold.pluginStore.noticeBusinessRender(coldCtx, r.ID)
			if e != nil || got.SourceCache == nil || got.SourceCache.Key != rendered.SourceCache.Key || cold.pluginStore.noticeTemplateHelper().Cache.Get(got.SourceCache.Key) != nil || noticeV2AddedCount(t, cold) != 1 || fixture.Metadata.Load() != 1 || fixture.Sends.Load() != 1 {
				t.Fatal("cold replay reset cache TTL, repeated source IO/event or lost typed cache proof", e, fixture.Metadata.Load(), fixture.Sends.Load())
			}
		})
	}
}

func TestPluginNoticeV2SourceCaughtTemplateErrorIsNormalReturn(t *testing.T) {
	a, core, ctx, request, saved, fixture := noticeV2SubscriptionFixture(t)
	noticePreferenceTest(t, a, "templates", "NotificationTemplates", map[string]any{"subscribeAdded": "{'title': '{{ 1 / 0 }}'}"})
	if e := a.larkCompleteSourceSubscription(ctx, core, request, saved); e != nil {
		t.Fatal("caught source template exception escaped post_message", e)
	}
	r := noticeV2OnlyRoute(t, a)
	out, e := a.pluginStore.noticeBusinessRender(ctx, r.ID)
	if e != nil || out.RenderSourceError != "ValueError" || !r.Complete || r.SourceError != "" || noticeV2AddedCount(t, a) != 1 || fixture.Sends.Load() != 1 {
		t.Fatal("render exception conflated with outer post or delivery result", out.RenderSourceError, r.SourceError, e)
	}
}

func TestPluginNoticeV2HTTPUnknownRemainsSeparateFromSourceReturn(t *testing.T) {
	a, core, ctx, request, saved, fixture := noticeV2SubscriptionFixture(t)
	var sends atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		h, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no owned hijacker")
			return
		}
		c, _, e := h.Hijack()
		if e == nil {
			c.Close()
		}
	}))
	defer server.Close()
	a.pluginStore.notices.send = func(ctx context.Context, _ pluginNoticeQueueEntry) (int, error) {
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("owned"))
		response, e := server.Client().Do(r)
		if response != nil {
			response.Body.Close()
		}
		if e == nil {
			t.Error("owned server did not lose reply")
		}
		return 0, seedhost.ErrUnknown
	}
	if e := a.larkCompleteSourceSubscription(ctx, core, request, saved); e != nil {
		t.Fatal("source queue should consume sender unknown", e)
	}
	r := noticeV2OnlyRoute(t, a)
	ids := noticeRouteTestIDs(t, a, "plugin_message_queue")
	q, e := a.pluginStore.readPluginMessage(ctx, a.Store.DB, ids[0])
	if e != nil || q.State != "unknown" || !r.Complete || r.SourceError != "" || noticeV2AddedCount(t, a) != 1 || sends.Load() != 1 || fixture.Sends.Load() != 0 {
		t.Fatal("delivery unknown fabricated success/retried", q.State, e)
	}
	if e = a.runNoticeRoute(ctx, r.ID); e != nil || sends.Load() != 1 {
		t.Fatal("source readback repeated unknown HTTP", e)
	}
}

func TestPluginNoticeV2NativeDependencyAndMissingHistoryBlockSourceEvent(t *testing.T) {
	for _, mode := range []string{"dependency", "history_failure", "render_deleted", "history_deleted"} {
		t.Run(mode, func(t *testing.T) {
			a, core, ctx, request, saved, _ := noticeV2SubscriptionFixture(t)
			switch mode {
			case "dependency":
				noticePreferenceTest(t, a, "templates", "NotificationTemplates", map[string]any{"subscribeAdded": "{'title':'{{ lipsum() }}'}"})
			case "history_failure":
				if _, e := a.Store.DB.Exec(`CREATE TRIGGER notice_v2_fail BEFORE INSERT ON records WHEN NEW.kind='notification' BEGIN SELECT RAISE(ABORT,'owned history failure'); END`); e != nil {
					t.Fatal(e)
				}
			}
			e := a.larkCompleteSourceSubscription(ctx, core, request, saved)
			if mode == "dependency" || mode == "history_failure" {
				if e == nil || noticeV2AddedCount(t, a) != 0 {
					t.Fatal("incomplete native/storage route published source event", e)
				}
				ids := noticeRouteTestIDs(t, a, "plugin_notice_routes")
				if len(ids) != 1 {
					t.Fatal(ids)
				}
				r, _, readErr := a.pluginStore.readNoticeRoute(ctx, a.Store.DB, ids[0])
				if readErr != nil || r.Complete {
					t.Fatal("native/storage failure sealed as source completion", r.SourceError, readErr)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			r := noticeV2OnlyRoute(t, a)
			if mode == "render_deleted" {
				_, e = a.Store.DB.Exec(`DELETE FROM plugin_notice_route_steps WHERE route_id=? AND ordinal=1`, r.ID)
			} else {
				_, e = a.Store.DB.Exec(`DELETE FROM records WHERE kind='notification'`)
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = a.pluginStore.readNoticeRoute(ctx, a.Store.DB, r.ID); e == nil {
				t.Fatal("complete bit bypassed missing actual render/history")
			}
		})
	}
}

func TestPluginNoticeV2ExactSourceInputAndSealedCompletionGuard(t *testing.T) {
	for _, mutation := range []string{"none", "different_input", "incomplete", "count", "last_sha", "wrong_binding"} {
		t.Run(mutation, func(t *testing.T) {
			a, core, ctx, request, saved, _ := noticeV2SubscriptionFixture(t)
			if e := a.larkCompleteSourceSubscription(ctx, core, request, saved); e != nil {
				t.Fatal(e)
			}
			r := noticeV2OnlyRoute(t, a)
			var payload pluginLarkBusinessNotice
			if e := cleanupJournalStrict(r.BusinessV2.Input, &payload); e != nil {
				t.Fatal(e)
			}
			core.ordinal-- // the actual subscription.added statement checks its preceding notice.post
			tx, e := a.Store.DB.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			if e = a.requireLarkBusinessNoticeResultTx(ctx, tx, core, payload); e != nil {
				tx.Rollback()
				t.Fatal("actual sealed notice did not satisfy exact event guard", e)
			}
			tx.Rollback()
			switch mutation {
			case "different_input":
				payload.Kwargs["username"] = "another source"
			case "incomplete":
				r.Complete = false
			case "count":
				r.Count++
			case "last_sha":
				r.LastSHA = strings.Repeat("0", 64)
			case "wrong_binding":
				r.BusinessV2.Binding.Ordinal--
			}
			if mutation != "none" && mutation != "different_input" {
				tx, e = a.Store.DB.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				if e = a.pluginStore.saveNoticeRouteTx(ctx, tx, r); e != nil {
					t.Fatal(e)
				}
				if e = tx.Commit(); e != nil {
					t.Fatal(e)
				}
			}
			tx, e = a.Store.DB.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			e = a.requireLarkBusinessNoticeResultTx(ctx, tx, core, payload)
			if (mutation == "none") != (e == nil) {
				t.Fatal("normal bit/nil result bypassed exact route input/chain proof", mutation, e)
			}
		})
	}
}

func TestPluginNoticeV2SourceRawObjectDependencyIsExplicit(t *testing.T) {
	for _, c := range []struct {
		text        string
		unsupported bool
	}{{"{'title':'{{ __mediainfo__.title }}'}", true}, {"{'title':'{{ __meta__.name }}'}", true}, {"{'title':'{{ \"__mediainfo__\" }}'}", false}, {"{'title':'literal __meta__'}", false}, {"{'title':'{% raw %}{{ __meta__ }}{% endraw %}'}", false}, {"{'title':'{# __meta__ #}safe'}", false}} {
		env, e := notice.NewEnvelope(notice.Object{"ctype": "subscribeAdded"})
		if e != nil {
			t.Fatal(e)
		}
		e = noticeV2RequireSourceObjectImplementation(env, map[string]any{"subscribeAdded": c.text})
		if c.unsupported != errors.Is(e, notice.ErrTemplateDependency) {
			t.Fatal("raw object dependency guard changed literal/comment semantics", c.text, e)
		}
	}
}
