package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func sharedSubscriptionReadRow(t *testing.T, p *originalPluginInstance, id string) (plugins.SharedSubscriptionV1, map[string]json.RawMessage) {
	t.Helper()
	b, e := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: id})
	if e != nil {
		t.Fatal(e)
	}
	v := decodeSharedSubscription(t, b)
	var row map[string]json.RawMessage
	if e = json.Unmarshal(v.Row, &row); e != nil {
		t.Fatal(e)
	}
	return v, row
}
func TestSharedSubscriptionsActualCloneCASRollbackAndNativeABA(t *testing.T) {
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = a.Close() }()
	cookie := setupTestAdmin(t, a)
	p := installSharedSubscriptionsFixture(t, a, cookie, sharedSubscriptionsAll)
	ctx := context.Background()
	sid := "owned-s05-concurrent"
	seedSharedSubscription(t, a, sid)
	resp := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedSharedHost/clone", `{"suffix":"Shared"}`, cookie, "")
	assertOriginalRegistrySuccess(t, resp.Code, resp.Body.Bytes())
	a.wg.Wait()
	v, ok := a.pluginRunningInstances.Load("OwnedSharedHostshared")
	if !ok {
		t.Fatal("actual clone not loaded")
	}
	clone := v.(*originalPluginInstance)
	pSnap := sharedSubscriptionsSnapshot(t, p)
	cSnap := sharedSubscriptionsSnapshot(t, clone)
	if bytes.Equal(pSnap["pid"], cSnap["pid"]) {
		t.Fatal("clone reused worker")
	}
	t.Logf("real worker pid=%s clone=%s", pSnap["pid"], cSnap["pid"])
	update := func(worker *originalPluginInstance, patch string, revision *int) error {
		_, e := sharedSubscriptionsCall(t, worker, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: sid, Patch: json.RawMessage(patch), ExpectedRevision: revision})
		return e
	}
	revision := 1
	start := make(chan struct{})
	errors := make(chan error, 2)
	for i, w := range []*originalPluginInstance{p, clone} {
		go func(i int, w *originalPluginInstance) {
			<-start
			errors <- update(w, `{"keyword":"CAS `+string(rune('A'+i))+`"}`, &revision)
		}(i, w)
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if <-errors == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("CAS successes=%d", success)
	}
	before, _ := sharedSubscriptionReadRow(t, p, sid)
	if before.Revision != 2 {
		t.Fatal("CAS revision not atomic")
	}
	// With no optional revision, independent ordinary column writes both commit.
	var wg sync.WaitGroup
	wg.Add(2)
	for i, w := range []*originalPluginInstance{p, clone} {
		go func(i int, w *originalPluginInstance) {
			defer wg.Done()
			patch := `{"description":"last-write description"}`
			if i == 1 {
				patch = `{"keyword":"last-write keyword"}`
			}
			if e := update(w, patch, nil); e != nil {
				t.Error(e)
			}
		}(i, w)
	}
	wg.Wait()
	_, row := sharedSubscriptionReadRow(t, clone, sid)
	if string(row["description"]) != `"last-write description"` || string(row["keyword"]) != `"last-write keyword"` {
		t.Fatal("ordinary disjoint writes lost")
	}
	// Source writer's encrypted private words and native row/revision share TX.
	before, _ = sharedSubscriptionReadRow(t, p, sid)
	if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_shared_late_abort BEFORE UPDATE ON records WHEN OLD.kind='subscription' BEGIN SELECT RAISE(ABORT,'owned late failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e := update(p, `{"keyword":"must rollback","custom_words":"owned-private-rollback"}`, nil); e == nil {
		t.Fatal("SQL failure accepted")
	}
	if _, e := a.Store.DB.Exec(`DROP TRIGGER owned_shared_late_abort`); e != nil {
		t.Fatal(e)
	}
	after, _ := sharedSubscriptionReadRow(t, p, sid)
	assertSharedOracleJSON(t, after.Row, before.Row)
	if after.Revision != before.Revision {
		t.Fatal("failed TX changed revision")
	}
	words, e := a.subscriptionWords(ctx, sid)
	if e != nil || words != "" {
		t.Fatal("failed TX leaked private write", e)
	}
	if e := update(p, `{"id":"replacement","keyword":"must reject"}`, nil); e == nil {
		t.Fatal("opaque identity mutation accepted")
	}
	after, _ = sharedSubscriptionReadRow(t, p, sid)
	assertSharedOracleJSON(t, after.Row, before.Row)
	// A reused native caller cannot revive NULL after A -> B -> A; unrelated
	// quality edits must preserve include NULL rather than invalidate all filters.
	if e := update(p, `{"keyword":null,"include":null,"quality":null,"custom_words":null}`, nil); e != nil {
		t.Fatal(e)
	}
	var old Subscription
	if e = a.Store.Get(ctx, "subscription", sid, &old); e != nil {
		t.Fatal(e)
	}
	quality := "native-quality"
	old.Policy.FilterParams.Quality = &quality
	old.Policy.Source = quality
	old.Keyword = "native B"
	if e = a.Store.Put(ctx, "subscription", sid, old); e != nil {
		t.Fatal(e)
	}
	_, row = sharedSubscriptionReadRow(t, p, sid)
	if string(row["include"]) != "null" || string(row["quality"]) != `"native-quality"` {
		t.Fatalf("unrelated filter override lost: %s", mustCleanSeedJSON(row))
	}
	old.Keyword = ""
	if e = a.Store.Put(ctx, "subscription", sid, old); e != nil {
		t.Fatal(e)
	}
	_, row = sharedSubscriptionReadRow(t, clone, sid)
	if string(row["keyword"]) != `""` {
		t.Fatal("stale NULL resurrected")
	}
	for _, text := range []string{"owned-native-private", ""} {
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = a.saveSubscriptionWordsTx(ctx, tx, sid, &text); e != nil {
			_ = tx.Rollback()
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	var current Subscription
	if e = a.Store.Get(ctx, "subscription", sid, &current); e != nil {
		t.Fatal(e)
	}
	if _, ok = current.SharedSourceFields["custom_words"]; ok {
		t.Fatal("native private update retained stale source NULL marker")
	}
	if e := update(p, `{"note":false}`, nil); e != nil {
		t.Fatal(e)
	}
	var staleJSON Subscription
	if e = a.Store.Get(ctx, "subscription", sid, &staleJSON); e != nil {
		t.Fatal(e)
	}
	if e := update(clone, `{"note":0}`, nil); e != nil {
		t.Fatal(e)
	}
	staleJSON.Keyword = "native unrelated edit"
	if e = a.Store.Put(ctx, "subscription", sid, staleJSON); e != nil {
		t.Fatal(e)
	}
	_, row = sharedSubscriptionReadRow(t, p, sid)
	if string(row["note"]) != "0" {
		t.Fatalf("stale JSON false replaced current numeric 0: %s", row["note"])
	}
	// Concurrent ordinary native writes and SDK writes use the same real DB
	// transaction boundary. The stale source marker must never reappear.
	for round := 0; round < 20; round++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if e := a.Store.Put(ctx, "subscription", sid, old); e != nil {
				t.Error(e)
			}
		}()
		go func() {
			defer wg.Done()
			if e := update(clone, `{"keyword":"native versus SDK"}`, nil); e != nil {
				t.Error(e)
			}
		}()
		wg.Wait()
		_, row = sharedSubscriptionReadRow(t, p, sid)
		if string(row["keyword"]) == "null" {
			t.Fatal("concurrent stale marker resurrected")
		}
	}
	// Temporary SQL projection contains no surviving records after all operations.
	var count int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM temp.shared_subscription_projection`).Scan(&count); e != nil || count != 0 {
		t.Fatal("temporary projection leaked", count, e)
	}
	sharedSubscriptionsSnapshot(t, p)
	sharedSubscriptionsSnapshot(t, clone)
	_, beforeCold := sharedSubscriptionReadRow(t, p, sid)
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	p = a.startOriginalPlugin(ctx, "OwnedSharedHost")
	clone = a.startOriginalPlugin(ctx, "OwnedSharedHostshared")
	if p == nil || clone == nil {
		t.Fatal("cold clone missing")
	}
	_, afterCold := sharedSubscriptionReadRow(t, clone, sid)
	assertSharedOracleJSON(t, mustCleanSeedJSON(afterCold), mustCleanSeedJSON(beforeCold))
}
func TestSharedSubscriptionsPermissionDeniedActualWorker(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, nil)
	seedSharedSubscription(t, a, "owned-denied")
	for _, method := range sharedSubscriptionsAll {
		payload := map[string]any{"id": "owned-denied", "patch": map[string]any{"keyword": "must-not-write"}, "tmdbid": 7001}
		if _, e := sharedSubscriptionsCall(t, p, method, payload); e == nil {
			t.Fatalf("ungranted %s succeeded", method)
		}
	}
	var s Subscription
	if e := a.Store.Get(context.Background(), "subscription", "owned-denied", &s); e != nil || s.Revision != 1 || s.Keyword != "" {
		t.Fatal("denied worker changed DB", e)
	}
}
func TestSharedSubscriptionsLargeActualWorkerCold(t *testing.T) {
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = a.Close() }()
	cookie := setupTestAdmin(t, a)
	p := installSharedSubscriptionsFixture(t, a, cookie, sharedSubscriptionsAll)
	seedSharedSubscription(t, a, "owned-large")
	description := strings.Repeat("owned-字-", 90*1024)
	if len(description) < 700*1024 {
		t.Fatal("large fixture under target")
	}
	patch := mustCleanSeedJSON(map[string]any{"description": description, "note": map[string]any{"full": description}})
	b, e := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-large", Patch: patch})
	if e != nil {
		t.Fatal(e)
	}
	want := decodeSharedSubscription(t, b)
	_, row := sharedSubscriptionReadRow(t, p, "owned-large")
	if sharedSubscriptionString(row["description"]) != description {
		t.Fatal("large value truncated")
	}
	t.Logf("actual worker patch bytes=%d returned row bytes=%d", len(patch), len(want.Row))
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	p = a.startOriginalPlugin(context.Background(), "OwnedSharedHost")
	if p == nil {
		t.Fatal("cold large worker absent")
	}
	got, _ := sharedSubscriptionReadRow(t, p, "owned-large")
	assertSharedOracleJSON(t, got.Row, want.Row)
	if got.Revision != want.Revision {
		t.Fatal("cold revision changed")
	}
}

func TestSharedSubscriptionsNativeEditorPreservesUnrelatedSourceFacts(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	p := installSharedSubscriptionsFixture(t, a, cookie, sharedSubscriptionsAll)
	request := subscriptionRequest(t)
	request.State = "paused"
	sub, e := a.saveSubscription(ctx, "", request)
	if e != nil {
		t.Fatal(e)
	}
	_, e = sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: sub.ID, Patch: json.RawMessage(`{"keyword":null,"include":null,"quality":null,"doubanid":"source-only-fact","vote":9.5}`)})
	if e != nil {
		t.Fatal(e)
	}
	current, row := sharedSubscriptionReadRow(t, p, sub.ID)
	if string(row["keyword"]) != "null" {
		t.Fatal("source null absent")
	}
	request.Revision = current.Revision
	request.Keyword = "native B"
	request.Policy.Source = "native-quality"
	saved, e := a.saveSubscription(ctx, sub.ID, request)
	if e != nil {
		t.Fatal(e)
	}
	_, row = sharedSubscriptionReadRow(t, p, sub.ID)
	if string(row["include"]) != "null" || string(row["doubanid"]) != `"source-only-fact"` || string(row["quality"]) != `"native-quality"` {
		t.Fatalf("native editor changed unrelated facts: %s", mustCleanSeedJSON(row))
	}
	request.Revision = saved.Revision
	request.Keyword = ""
	if _, e = a.saveSubscription(ctx, sub.ID, request); e != nil {
		t.Fatal(e)
	}
	_, row = sharedSubscriptionReadRow(t, p, sub.ID)
	if string(row["keyword"]) != `""` {
		t.Fatal("native editor ABA restored stale null")
	}
	if _, e = sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: sub.ID, Patch: json.RawMessage(`{"vote":null}`)}); e != nil {
		t.Fatal(e)
	}
	var native Subscription
	if e = a.Store.Get(ctx, "subscription", sub.ID, &native); e != nil || native.Media.VoteAverage != 0 {
		t.Fatal("NULL retained stale native vote", native.Media.VoteAverage, e)
	}
}

func TestSharedSubscriptionsCancelledActualWorkerDoesNotWrite(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, sharedSubscriptionsAll)
	seedSharedSubscription(t, a, "owned-cancel")
	before, _ := sharedSubscriptionReadRow(t, p, "owned-cancel")
	blocker, e := a.Store.DB.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Rollback()
	waits := a.Store.DB.Stats().WaitCount
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, e := p.callSession(ctx, plugins.Runner{Timeout: 10 * time.Second}, "resource", mustCleanSeedJSON(map[string]any{"method": plugins.SharedSubscriptionsUpdate, "payload": plugins.SharedSubscriptionUpdateV1{ID: "owned-cancel", Patch: json.RawMessage(`{"keyword":"must not commit cancelled call"}`)}}), nil)
		done <- e
	}()
	deadline := time.Now().Add(5 * time.Second)
	for a.Store.DB.Stats().WaitCount == waits && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if a.Store.DB.Stats().WaitCount == waits {
		t.Fatal("actual worker never reached the DB transaction boundary")
	}
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("cancelled actual call succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled worker call did not return")
	}
	if e = blocker.Rollback(); e != nil {
		t.Fatal(e)
	}
	after, _ := sharedSubscriptionReadRow(t, p, "owned-cancel")
	assertSharedOracleJSON(t, after.Row, before.Row)
	if after.Revision != before.Revision {
		t.Fatal("cancelled call changed revision")
	}
}
