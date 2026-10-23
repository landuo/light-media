package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"light-media/internal/notice"
)

// This is a host-boundary matrix, not a business-plugin Form implementation.
// It uses owned SQLite stores, actual private notification acceptance/handoff,
// and an in-memory native sender. No worker, downloader, or transport is run.
// Lower-level writer cases exercise their own entry directly; an enclosing
// guarded driver must not conceal a missing guard on the writer itself.
var pluginFormNoticeBoundaryOperations = []string{
	"init-channels-existing", "init-templates-existing", "preference-write", "route-save",
	"driver-step", "driver-step-replay", "driver-finish-replay", "publish-tx", "accept-tx",
	"queue-save", "queue-commit", "modules-empty", "attempt", "attempt-consumed",
	"invoke-sender", "drain", "drain-empty", "drain-window-closed", "intent-write",
	"row-finish", "row-commit", "route-run", "route-run-complete", "route-run-locked",
	"route-resume", "route-resume-empty", "business-v2-replay",
}

type pluginFormNoticeBoundaryMarshaler struct {
	calls *int
	raw   json.RawMessage
}

func (m pluginFormNoticeBoundaryMarshaler) MarshalJSON() ([]byte, error) {
	(*m.calls)++
	return append([]byte(nil), m.raw...), nil
}

type pluginFormNoticeBoundaryFixture struct {
	a             *App
	files         map[string][]byte
	call          func(context.Context) error
	verify        func()
	driver        *pluginNoticeDriver
	sends         int
	marshal       int
	callbacks     int
	commits       int
	callerTxOpen  bool
	changesRows   bool
	wantSends     int
	wantCallbacks int
	wantMarshal   bool
	commitEntry   bool
}

type pluginFormNoticeBoundaryMemory struct {
	routeAfter, routeThrough, queueAfter int64
	windowReady                          bool
	window                               []notice.Period
	sends, marshal, callbacks, commits   int
	cursor                               int
	failure                              *removeLinkSourceFailure
}

func pluginFormNoticeBoundaryObserve(p *PluginStore, f *pluginFormNoticeBoundaryFixture) pluginFormNoticeBoundaryMemory {
	m := pluginFormNoticeBoundaryMemory{
		routeAfter: p.notices.routeAfter, routeThrough: p.notices.routeThrough,
		queueAfter: p.notices.queueAfter, windowReady: p.notices.windowReady,
		window: append([]notice.Period(nil), p.notices.window...),
	}
	if f != nil {
		m.sends, m.marshal, m.callbacks, m.commits = f.sends, f.marshal, f.callbacks, f.commits
		if f.driver != nil {
			m.cursor = f.driver.cursor
			if f.driver.failure != nil {
				copy := *f.driver.failure
				m.failure = &copy
			}
		}
	}
	return m
}

func (f *pluginFormNoticeBoundaryFixture) preserveFiles(t *testing.T) {
	t.Helper()
	for path, before := range f.files {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Errorf("notice boundary changed owned source/target media %q: %v", path, err)
		}
	}
}

func pluginFormNoticeBoundaryWindow(t *testing.T, a *App, open bool) {
	t.Helper()
	// Noon alone is allowed; a fixed 10:00 clock makes the closed case stable
	// in every process timezone. Empty periods are the ordinary open window.
	value := []any{map[string]any{"start": "12:00", "end": "12:00"}}
	if open {
		value = []any{}
	}
	noticePreferenceTest(t, a, "window", "", value)
	if err := a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
		t.Fatalf("notice fixture window setup failed: %v", err)
	}
	periods, err := a.pluginStore.currentNoticePeriods()
	if err != nil || notice.Allowed(periods, a.pluginStore.noticeNow()) != open {
		t.Fatalf("notice fixture window is not %v: %v", open, err)
	}
}

func pluginFormNoticeBoundaryRoute(t *testing.T, a *App, id string) pluginNoticeRoute {
	t.Helper()
	r, _, err := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, id)
	if err != nil {
		t.Fatalf("notice fixture lacks an authenticated route: %v", err)
	}
	return r
}

func pluginFormNoticeBoundaryQueue(t *testing.T, a *App, id string) pluginNoticeQueueEntry {
	t.Helper()
	q, err := a.pluginStore.readPluginMessage(context.Background(), a.Store.DB, id)
	if err != nil {
		t.Fatalf("notice fixture lacks an authenticated queue row: %v", err)
	}
	return q
}

func pluginFormNoticeBoundaryOnlyQueue(t *testing.T, a *App) pluginNoticeQueueEntry {
	t.Helper()
	ids := noticeRouteTestIDs(t, a, "plugin_message_queue")
	if len(ids) != 1 {
		t.Fatalf("notice fixture must contain one real queue entry, got %d", len(ids))
	}
	return pluginFormNoticeBoundaryQueue(t, a, ids[0])
}

// A commit-entry denial must leave its caller-owned transaction open. Do not
// mislabel an old implementation's premature commit as a fixture failure:
// record that outcome and still let the API/sticky/side-effect assertions run.
func pluginFormNoticeBoundaryCommitCall(t *testing.T, f *pluginFormNoticeBoundaryFixture, ctx context.Context, call func(*sql.Tx) error) error {
	t.Helper()
	tx, err := f.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("notice fixture could not begin caller transaction: %v", err)
	}
	defer tx.Rollback()
	operationErr := call(tx)
	err = tx.Commit()
	f.callerTxOpen = err == nil
	if err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Errorf("notice caller transaction could not commit after API result %v: %v", operationErr, err)
	}
	return operationErr
}

func pluginFormNoticeBoundaryNew(t *testing.T, operation string) *pluginFormNoticeBoundaryFixture {
	t.Helper()
	ctx := context.Background()
	f := &pluginFormNoticeBoundaryFixture{changesRows: true, files: map[string][]byte{}}
	var route pluginNoticeRoute
	var queue pluginNoticeQueueEntry
	var row pluginNoticeQueueRow
	var owner *removeLinkEffectsFixture
	stamp := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	if operation == "business-v2-replay" {
		// The existing Lark admission fixture creates a real API-origin inbox
		// and live core scope without starting the Lark SDK or any transport.
		// Its original notice.post completes a genuine absent-context V2 route.
		f.a = newTestApp(t)
	} else {
		owner = removeLinkEffectsNew(t, map[string]any{"notify": true, "delete_torrents": true})
		f.a = owner.a
		for _, path := range []string{owner.history.Source, owner.history.Target} {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("notice fixture media unavailable: %v", err)
			}
			f.files[path] = raw
		}
	}
	p := f.a.pluginStore
	p.notices.now = func() time.Time { return stamp }
	p.notices.send = func(_ context.Context, q pluginNoticeQueueEntry) (int, error) {
		f.sends++
		if q.ID == "" {
			t.Error("controlled sender received no authenticated queue identity")
		}
		return 1, nil
	}
	pluginFormNoticeBoundaryWindow(t, f.a, false)
	if operation == "business-v2-replay" {
		installUnregisteredLarkConfig(t, f.a, map[string]any{"enabled": true})
		core, liveCtx := larkStartedCoreFixture(t, f.a)
		payload := sourceBusinessNotice{AbsentContext: true, Envelope: notice.Object{
			"title": "owned Form notice fixture", "text": "in-memory sender only", "save_history": false,
		}, Kwargs: notice.Object{}}
		if err := f.a.larkPostBusinessNotice(liveCtx, core, payload); err != nil {
			t.Fatalf("notice fixture real V2 notice.post did not complete: %v", err)
		}
		route = noticeV2OnlyRoute(t, f.a)
		if route.Version != 2 || route.BusinessV2 == nil || !route.Complete || route.SourceError != "" {
			t.Fatal("notice fixture real V2 source did not leave a successful authenticated route")
		}
		queue = pluginFormNoticeBoundaryOnlyQueue(t, f.a)
		if queue.State != "queued" || queue.Direct || f.sends != 0 {
			t.Fatal("notice fixture V2 route sent through a closed window")
		}
	} else if operation != "drain-empty" && operation != "route-resume-empty" {
		owner.authorize(t, ctx)
		x := cleanupOutboxCommit(t, owner, "notification")
		if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err != nil {
			t.Fatalf("notice fixture source outbox handoff failed: %v", err)
		}
		handoff := cleanupHandoff(t, owner, x)
		if handoff.Notice == nil || handoff.Notice.RouteID == "" {
			t.Fatal("notice fixture handoff lacks its real route receipt")
		}
		route = pluginFormNoticeBoundaryRoute(t, f.a, handoff.Notice.RouteID)
		if route.Kind != "remove_link" || route.Complete || route.Count != 0 || f.sends != 0 {
			t.Fatal("notice fixture did not stop before routing its accepted notice")
		}
	}
	if operation == "publish-tx" || operation == "accept-tx" {
		// Establish the real router prefix before calling the lower-level
		// writer directly at its next ordinal. These callbacks perform the
		// same settings/history reads as runNoticeRouteLocked; no accepted
		// source, publication, queue, or step receipt is inserted by test SQL.
		var fields notice.Object
		if err := json.Unmarshal(route.Envelope, &fields); err != nil {
			t.Fatal(err)
		}
		envelope, err := notice.NewEnvelope(fields)
		if err != nil {
			t.Fatalf("notice fixture cannot restore accepted envelope: %v", err)
		}
		message := envelope.Dump()
		prefix := &pluginNoticeDriver{a: f.a, id: route.ID}
		if _, err := prefix.step(ctx, "superuser", nil, func(tx *sql.Tx, _ pluginNoticeRoute, _ int) (any, error) {
			name, err := p.noticePreference(ctx, tx, "superuser", "")
			if err != nil || name != nil {
				return name, err
			}
			var nameFromAdmin string
			err = tx.QueryRowContext(ctx, `SELECT username FROM admin WHERE id=1`).Scan(&nameFromAdmin)
			if errors.Is(err, sql.ErrNoRows) {
				return "admin", nil
			}
			return nameFromAdmin, err
		}); err != nil {
			t.Fatalf("notice fixture superuser prefix failed: %v", err)
		}
		if _, err := prefix.step(ctx, "history", message, func(_ *sql.Tx, r pluginNoticeRoute, _ int) (any, error) {
			if r.HistoryID == "" || !bytes.Equal(mustCleanSeedJSON(message), r.Envelope) {
				t.Fatal("notice fixture history prefix differs from accepted envelope")
			}
			return map[string]any{"history_id": r.HistoryID}, nil
		}); err != nil {
			t.Fatalf("notice fixture history prefix failed: %v", err)
		}
		key, ok := message["mtype"].(string)
		if !ok || message["userid"] != nil {
			t.Fatal("notice fixture is not the original broadcast branch")
		}
		value, err := prefix.step(ctx, "switch", key, func(tx *sql.Tx, _ pluginNoticeRoute, _ int) (any, error) {
			return p.noticePreference(ctx, tx, "switch", key)
		})
		if err != nil || value != nil {
			t.Fatalf("notice fixture expected the unconfigured broadcast switch: %v %v", value, err)
		}
		if operation == "accept-tx" {
			data := envelope.Dump()
			delete(data, "save_history")
			data["type"] = data["mtype"]
			if _, err := prefix.step(ctx, "publish", data, func(tx *sql.Tx, r pluginNoticeRoute, ordinal int) (any, error) {
				return f.a.publishPluginNoticeTx(ctx, tx, r, ordinal, data)
			}); err != nil {
				t.Fatalf("notice fixture publication prefix failed: %v", err)
			}
		}
		route = pluginFormNoticeBoundaryRoute(t, f.a, route.ID)
		if route.Complete || route.Count != prefix.cursor {
			t.Fatal("notice fixture callback prefix was not authenticated")
		}
	}
	needsQueue := map[string]bool{
		"queue-save": true, "queue-commit": true, "modules-empty": true, "attempt": true,
		"attempt-consumed": true, "invoke-sender": true, "drain": true,
		"drain-window-closed": true, "intent-write": true, "row-finish": true,
		"row-commit": true, "route-run-complete": true, "driver-finish-replay": true,
	}
	if needsQueue[operation] {
		if err := f.a.runNoticeRoute(ctx, route.ID); err != nil {
			t.Fatalf("notice fixture scope-free routing failed: %v", err)
		}
		route = pluginFormNoticeBoundaryRoute(t, f.a, route.ID)
		queue = pluginFormNoticeBoundaryOnlyQueue(t, f.a)
		if !route.Complete || route.SourceError != "" || queue.State != "queued" || queue.Direct || f.sends != 0 {
			t.Fatal("notice fixture lacks its real deferred queue acceptance")
		}
	}
	if operation == "attempt" || operation == "attempt-consumed" || operation == "drain" || operation == "drain-empty" {
		pluginFormNoticeBoundaryWindow(t, f.a, true)
	}
	if operation == "attempt-consumed" {
		if err := f.a.attemptPluginMessage(ctx, queue.ID); err != nil {
			t.Fatalf("notice fixture scope-free attempt failed: %v", err)
		}
		queue = pluginFormNoticeBoundaryQueue(t, f.a, queue.ID)
		if queue.State != "consumed" || queue.Modules != 1 || f.sends != 1 {
			t.Fatal("notice fixture controlled attempt did not complete exactly once")
		}
	}
	if operation == "row-finish" || operation == "row-commit" {
		queue.State, queue.AttemptAt = "intent", stamp.Format(time.RFC3339Nano)
		if err := pluginCommonFormCommittedTx(t, p, ctx, func(tx *sql.Tx) error {
			var err error
			row, err = p.writePluginMessageIntentTx(ctx, tx, queue)
			return err
		}); err != nil {
			t.Fatalf("notice fixture genuine queued-to-intent transition failed: %v", err)
		}
		if err := row.require(ctx, f.a.Store.DB); err != nil {
			t.Fatalf("notice fixture intent row was not committed: %v", err)
		}
		if operation == "row-finish" {
			// Obtain a real result from the controlled sender. Do not invent a
			// consumed receipt or insert/seal an intent directly with SQL.
			modules, err := invokePluginNoticeSender(ctx, p.notices.send, queue)
			if err != nil || modules != 1 || f.sends != 1 {
				t.Fatalf("notice fixture controlled sender result missing: %d %v", modules, err)
			}
			queue.State, queue.Modules, queue.FinishedAt = "consumed", modules, stamp.Add(time.Second).Format(time.RFC3339Nano)
		}
	}
	f.driver = &pluginNoticeDriver{a: f.a, id: route.ID}
	if operation == "driver-step-replay" {
		if _, err := f.driver.step(ctx, "superuser", nil, func(tx *sql.Tx, _ pluginNoticeRoute, _ int) (any, error) {
			return "admin", nil
		}); err != nil {
			t.Fatalf("notice fixture initial real driver step failed: %v", err)
		}
		f.driver.cursor = 0 // Replay from the same source's start, not a new step.
	}
	if operation == "driver-finish-replay" {
		f.driver.cursor = route.Count
	}
	p.notices.afterCommit = func(string) error { f.commits++; return nil }
	writeTx := func(callCtx context.Context, call func(*sql.Tx) error) error {
		return pluginCommonFormCommittedTx(t, p, callCtx, call)
	}
	switch operation {
	case "init-channels-existing":
		f.changesRows = false
		f.call = func(callCtx context.Context) error {
			return writeTx(callCtx, func(tx *sql.Tx) error { return p.initNotificationChannelsTx(callCtx, tx) })
		}
	case "init-templates-existing":
		f.changesRows = false
		f.call = func(callCtx context.Context) error {
			return writeTx(callCtx, func(tx *sql.Tx) error { return p.initNotificationTemplatesTx(callCtx, tx) })
		}
	case "preference-write":
		f.wantMarshal = true
		f.call = func(callCtx context.Context) error {
			return writeTx(callCtx, func(tx *sql.Tx) error {
				return p.putNoticePreferenceTx(callCtx, tx, "user", "owned-form-probe", pluginFormNoticeBoundaryMarshaler{&f.marshal, json.RawMessage(`{"owned":true}`)})
			})
		}
	case "route-save":
		f.call = func(callCtx context.Context) error {
			return writeTx(callCtx, func(tx *sql.Tx) error { return p.saveNoticeRouteTx(callCtx, tx, route) })
		}
	case "driver-step", "driver-step-replay":
		f.wantMarshal = true
		f.wantCallbacks = 1
		if operation == "driver-step-replay" {
			f.changesRows, f.wantCallbacks = false, 0
		}
		f.call = func(callCtx context.Context) error {
			_, err := f.driver.step(callCtx, "superuser", pluginFormNoticeBoundaryMarshaler{&f.marshal, json.RawMessage(`null`)}, func(*sql.Tx, pluginNoticeRoute, int) (any, error) {
				f.callbacks++
				return "admin", nil
			})
			return err
		}
		f.verify = func() {
			if f.driver.cursor != 1 || f.driver.failure != nil {
				t.Error("scope-free driver did not advance exactly one authenticated step")
			}
		}
	case "driver-finish-replay":
		f.call = func(callCtx context.Context) error { return f.driver.finish(callCtx, nil) }
	case "publish-tx":
		var data notice.Object
		if err := json.Unmarshal(route.Envelope, &data); err != nil {
			t.Fatal(err)
		}
		delete(data, "save_history")
		data["type"] = data["mtype"]
		title, err := json.Marshal(data["title"])
		if err != nil {
			t.Fatal(err)
		}
		data["title"] = pluginFormNoticeBoundaryMarshaler{&f.marshal, title}
		f.wantMarshal = true
		f.call = func(callCtx context.Context) error {
			return writeTx(callCtx, func(tx *sql.Tx) error {
				_, err := f.a.publishPluginNoticeTx(callCtx, tx, route, route.Count, data)
				return err
			})
		}
		f.verify = func() {
			if cleanupEffectsCount(t, f.a, "plugin_notice_publications") != 1 {
				t.Error("scope-free publication writer did not commit exactly one private publication")
			}
		}
	case "accept-tx":
		var message notice.Object
		if err := json.Unmarshal(route.Envelope, &message); err != nil {
			t.Fatal(err)
		}
		call := notice.Call{Args: []any{"post_message"}, Kwargs: notice.Object{"message": message, "current_time": route.CurrentTime, "immediately": false}}
		f.call = func(callCtx context.Context) error {
			return writeTx(callCtx, func(tx *sql.Tx) error {
				_, err := f.a.acceptPluginMessageTx(callCtx, tx, route, route.Count, call)
				return err
			})
		}
		f.verify = func() {
			if cleanupEffectsCount(t, f.a, "plugin_message_queue") != 1 {
				t.Error("scope-free acceptance writer did not commit exactly one queue row")
			}
		}
	case "queue-save":
		f.call = func(callCtx context.Context) error {
			return writeTx(callCtx, func(tx *sql.Tx) error { return p.savePluginMessageTx(callCtx, tx, queue) })
		}
	case "queue-commit":
		f.changesRows, f.commitEntry = false, true
		f.call = func(callCtx context.Context) error {
			return pluginFormNoticeBoundaryCommitCall(t, f, callCtx, func(tx *sql.Tx) error { return p.commitPluginMessage(callCtx, tx, "owned_form_probe", queue) })
		}
	case "modules-empty":
		// No Lark module is installed in this owned RemoveLink store. Even
		// this genuine empty-module no-op must not silently accept Form scope.
		f.changesRows = false
		f.call = func(callCtx context.Context) error {
			modules, err := f.a.sendPluginMessageModules(callCtx, queue)
			if modules != 0 {
				t.Errorf("empty module fixture invoked %d modules", modules)
			}
			return err
		}
	case "attempt", "attempt-consumed":
		f.wantSends = 1
		if operation == "attempt-consumed" {
			f.changesRows, f.wantSends = false, 0
		}
		f.call = func(callCtx context.Context) error { return f.a.attemptPluginMessage(callCtx, queue.ID) }
		f.verify = func() {
			q := pluginFormNoticeBoundaryQueue(t, f.a, queue.ID)
			if q.State != "consumed" || q.Modules != 1 || q.AttemptAt == "" || q.FinishedAt == "" {
				t.Error("scope-free attempt lost its controlled sender result")
			}
		}
	case "invoke-sender":
		f.changesRows, f.wantSends = false, 1
		f.call = func(callCtx context.Context) error {
			_, err := invokePluginNoticeSender(callCtx, p.notices.send, queue)
			return err
		}
	case "drain", "drain-empty", "drain-window-closed":
		f.wantSends = 1
		if operation != "drain" {
			f.changesRows, f.wantSends = false, 0
		}
		f.call = func(callCtx context.Context) error { return f.a.drainPluginMessages(callCtx, 200) }
		if operation == "drain" {
			f.verify = func() {
				q := pluginFormNoticeBoundaryQueue(t, f.a, queue.ID)
				if q.State != "consumed" || q.Modules != 1 {
					t.Error("scope-free FIFO drain lost its one real queued result")
				}
			}
		}
	case "intent-write":
		queue.State, queue.AttemptAt = "intent", stamp.Format(time.RFC3339Nano)
		f.call = func(callCtx context.Context) error {
			return writeTx(callCtx, func(tx *sql.Tx) error { _, err := p.writePluginMessageIntentTx(callCtx, tx, queue); return err })
		}
		f.verify = func() {
			if got := pluginFormNoticeBoundaryQueue(t, f.a, queue.ID); got.State != "intent" || got.AttemptAt != queue.AttemptAt {
				t.Error("scope-free intent transition was not committed")
			}
		}
	case "row-finish":
		f.call = func(callCtx context.Context) error {
			return writeTx(callCtx, func(tx *sql.Tx) error { _, err := p.finishPluginMessageRowTx(callCtx, tx, row, queue); return err })
		}
		f.verify = func() {
			got := pluginFormNoticeBoundaryQueue(t, f.a, queue.ID)
			if got.State != "consumed" || got.Modules != 1 || got.FinishedAt != queue.FinishedAt {
				t.Error("scope-free finish failed to persist the actual controlled sender result")
			}
		}
	case "row-commit":
		f.changesRows, f.commitEntry = false, true
		f.call = func(callCtx context.Context) error {
			return pluginFormNoticeBoundaryCommitCall(t, f, callCtx, func(tx *sql.Tx) error { return p.commitPluginMessageRow(callCtx, tx, "owned_form_probe", row) })
		}
	case "route-run", "route-run-complete", "route-run-locked", "route-resume":
		if operation == "route-run-complete" {
			f.changesRows = false
		}
		f.call = func(callCtx context.Context) error {
			switch operation {
			case "route-run-locked":
				return f.a.runNoticeRouteLocked(callCtx, route.ID)
			case "route-resume":
				return f.a.resumePluginNoticeRoutes(callCtx, 200)
			default:
				return f.a.runNoticeRoute(callCtx, route.ID)
			}
		}
		f.verify = func() {
			r := pluginFormNoticeBoundaryRoute(t, f.a, route.ID)
			q := pluginFormNoticeBoundaryOnlyQueue(t, f.a)
			if !r.Complete || r.SourceError != "" || q.State != "queued" || q.Direct {
				t.Error("scope-free route did not finish with exactly one deferred queue entry")
			}
		}
	case "route-resume-empty":
		f.changesRows = false
		f.call = func(callCtx context.Context) error { return f.a.resumePluginNoticeRoutes(callCtx, 200) }
	case "business-v2-replay":
		// This is a genuine complete-route replay, not fabricated V2 binding
		// data. Replay must not resend or invent a second render/queue step.
		f.call = func(callCtx context.Context) error { return f.a.runNoticeBusinessV2(callCtx, route) }
		f.verify = func() {
			r := pluginFormNoticeBoundaryRoute(t, f.a, route.ID)
			q := pluginFormNoticeBoundaryOnlyQueue(t, f.a)
			if !r.Complete || r.SourceError != "" || r.Count != route.Count || r.LastSHA != route.LastSHA || q.State != "queued" {
				t.Error("scope-free V2 replay duplicated or altered accepted source steps")
			}
		}
	default:
		t.Fatalf("notice fixture operation %q has no implementation", operation)
	}
	if f.call == nil {
		t.Fatal("notice fixture has no call")
	}
	f.preserveFiles(t)
	return f
}

func TestPluginFormCommonNoticeBoundariesDenyBeforeEffects(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		for _, operation := range pluginFormNoticeBoundaryOperations {
			t.Run(pluginFormDispatchModeName(mode)+"/same-store-other-instance/"+operation, func(t *testing.T) {
				pluginFormNoticeBoundaryDenied(t, mode, operation, false)
			})
		}
		// Cross-store representatives include serialization, a true pending
		// route, the actual sender boundary, and the queue drain/intent path.
		for _, operation := range []string{"preference-write", "route-run", "attempt", "drain", "invoke-sender", "row-finish", "business-v2-replay"} {
			t.Run(pluginFormDispatchModeName(mode)+"/foreign-store/"+operation, func(t *testing.T) {
				pluginFormNoticeBoundaryDenied(t, mode, operation, true)
			})
		}
	}
}

func pluginFormNoticeBoundaryDenied(t *testing.T, mode pluginFormMode, operation string, foreign bool) {
	t.Helper()
	f := pluginFormNoticeBoundaryNew(t, operation)
	target := f.a.pluginStore
	source := target
	if foreign {
		_, source = pluginStoreFixture(t)
	}
	ctx, in, session := pluginCommonFormPreparedAttach(t, source, mode)
	beforeSource := pluginCommonFormRows(t, source)
	beforeTarget := beforeSource
	if source != target {
		beforeTarget = pluginCommonFormRows(t, target)
	}
	beforeSourceMemory := pluginFormNoticeBoundaryObserve(source, nil)
	beforeTargetMemory := pluginFormNoticeBoundaryObserve(target, f)
	// Setup, durable and in-memory snapshots precede the new request.
	requestCtx, requestCancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(requestCancel)
	requestSession, requestErr := source.beginPluginFormSession(requestCtx, in, session.origin)
	if requestErr != nil || requestSession == nil || requestSession.resolution.mode != mode {
		t.Fatal("fresh boundary request did not authenticate expected mode", requestErr)
	}
	session = requestSession
	ctx = withPluginFormSession(requestCtx, requestSession)
	hooks := 0
	result, err := source.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
		hooks++
		if denied := f.call(callCtx); pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" {
			t.Errorf("notice API did not deny at its own entry: %v", denied)
		}
		return json.RawMessage(`{"swallowed":true}`), nil
	}))
	if hooks != 1 || result != nil || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Errorf("notice denial was swallowed: hooks=%d result=%s error=%v", hooks, result, err)
	}
	if pluginErrorCode(session.validateFinal(ctx)) != "PLUGIN_CAPABILITY_DENIED" {
		t.Error("notice denial did not poison the original authenticated Form session")
	}
	pluginCommonFormRowsUnchanged(t, source, beforeSource)
	if source != target {
		pluginCommonFormRowsUnchanged(t, target, beforeTarget)
	}
	if !reflect.DeepEqual(beforeSourceMemory, pluginFormNoticeBoundaryObserve(source, nil)) {
		t.Error("notice Form call changed source runtime cursors or window")
	}
	if !reflect.DeepEqual(beforeTargetMemory, pluginFormNoticeBoundaryObserve(target, f)) {
		t.Errorf("notice Form call changed target cursor, callback, serializer, commit or sender counters: before=%+v after=%+v", beforeTargetMemory, pluginFormNoticeBoundaryObserve(target, f))
	}
	if f.commitEntry && !f.callerTxOpen {
		t.Error("denied commit API closed its caller-owned transaction")
	}
	f.preserveFiles(t)
}

func TestPluginFormCommonNoticeBoundariesRemainAvailableOutsideDispatch(t *testing.T) {
	for _, operation := range pluginFormNoticeBoundaryOperations {
		t.Run(operation, func(t *testing.T) {
			f := pluginFormNoticeBoundaryNew(t, operation)
			p := f.a.pluginStore
			before := pluginCommonFormRows(t, p)
			sends, callbacks, marshals := f.sends, f.callbacks, f.marshal
			if err := f.call(context.Background()); err != nil {
				t.Fatalf("scope-free notice API rejected its valid fixture: %v", err)
			}
			if f.changesRows == reflect.DeepEqual(before, pluginCommonFormRows(t, p)) {
				t.Errorf("scope-free notice committed-row change was not %v", f.changesRows)
			}
			if f.sends-sends != f.wantSends || f.callbacks-callbacks != f.wantCallbacks {
				t.Errorf("scope-free counts: sends=%d want=%d callbacks=%d want=%d", f.sends-sends, f.wantSends, f.callbacks-callbacks, f.wantCallbacks)
			}
			if (f.marshal > marshals) != f.wantMarshal {
				t.Errorf("scope-free input serialization=%d, expected exercised=%v", f.marshal-marshals, f.wantMarshal)
			}
			if f.commitEntry && (f.callerTxOpen || f.commits != 1) {
				t.Error("scope-free commit entry did not commit exactly once")
			}
			if f.verify != nil {
				f.verify()
			}
			// Replaying actual terminal attempts and route continuations cannot
			// add another sender call. A direct sender invocation is not replay.
			if operation == "attempt" || operation == "drain" || operation == "route-run" || operation == "route-run-locked" || operation == "route-resume" {
				sent := f.sends
				if err := f.call(context.Background()); err != nil || f.sends != sent {
					t.Errorf("scope-free replay resent or failed: sends=%d want=%d error=%v", f.sends, sent, err)
				}
				if f.verify != nil {
					f.verify()
				}
			}
			f.preserveFiles(t)
		})
	}
}
