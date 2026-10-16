package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestSubscriptionAddedFactAndFrozenIdentityShareActualCreation(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	request := subscriptionRequest(t)
	sub, err := a.saveSubscription(ctx, "", request)
	if err != nil {
		t.Fatal(err)
	}
	page, err := a.domainEvents(ctx, 0, 100)
	if err != nil || len(page.Events) != 1 {
		t.Fatal("creation fact missing", page, err)
	}
	event := page.Events[0]
	if event.Type != "subscription.added" || event.SubjectID != sub.ID || event.SubjectKind != "subscription" || event.Purpose != "subscription" || event.IntentID != "" || event.AssetID != "" || event.JobID != "" || event.WorkflowID != "" {
		t.Fatal("incorrect creation authority", event)
	}
	identity, err := a.subscriptionAddedSnapshot(ctx, event)
	if err != nil || identity.Media.Title != sub.Media.Title || identity.Revision != 1 {
		t.Fatal("frozen identity missing", identity, err)
	}
	var sealed []byte
	if err = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='subscription_added' AND id=?`, event.EventID).Scan(&sealed); err != nil || json.Valid(sealed) {
		t.Fatal("event snapshot not protected", err)
	}
	request.State = "active"
	request.Revision = sub.Revision
	changedMedia := sub.Media
	changedMedia.Title = "修改后的合成剧集"
	if err = a.Store.Put(ctx, "cache", "media:tv:206484", map[string]any{"at": time.Now(), "data": changedMedia}); err != nil {
		t.Fatal(err)
	}
	updated, err := a.saveSubscription(ctx, sub.ID, request)
	if err != nil || updated.Media.Title != changedMedia.Title {
		t.Fatal("edit did not use the newer metadata", updated.Media.Title, err)
	}
	page, err = a.domainEvents(ctx, 0, 100)
	if err != nil || len(page.Events) != 1 {
		t.Fatal("editing emitted a second added fact", err)
	}
	identity, err = a.subscriptionAddedSnapshot(ctx, event)
	if err != nil || identity.Revision != 1 || identity.Media.Title != sub.Media.Title {
		t.Fatal("editing rewrote original identity", err)
	}
	for _, change := range []func(*DomainEvent){func(e *DomainEvent) { e.SubscriptionID = "sub_other" }, func(e *DomainEvent) { e.MediaID = "tv:999" }, func(e *DomainEvent) { e.IntentID = "intent_other" }, func(e *DomainEvent) { e.Purpose = "cross_seed" }} {
		forged := event
		change(&forged)
		forged.SubjectID = ""
		forged.SubjectKind = ""
		if _, err = a.subscriptionAddedSnapshot(ctx, forged); err == nil {
			t.Fatal("forged creation identity accepted", forged)
		}
	}
	// Use an actual App close/reopen, rather than only another decoder call.
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	identity, err = reopened.subscriptionAddedSnapshot(ctx, event)
	if err != nil || identity.Media.Title != sub.Media.Title || identity.Revision != 1 {
		t.Fatal("creation identity changed across restart", identity, err)
	}
}
func TestSubscriptionAddedLateOutboxFailureRollsBackActualSubscription(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_subscription_fact BEFORE INSERT ON domain_events BEGIN SELECT RAISE(ABORT,'synthetic outbox failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveSubscription(ctx, "", subscriptionRequest(t)); err == nil {
		t.Fatal("creation accepted without its outbox fact")
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('subscription','subscription_added')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial subscription commit", count, err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER fail_subscription_fact`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveSubscription(ctx, "", subscriptionRequest(t)); err != nil {
		t.Fatal("rolled back creation blocked retry", err)
	}
}
