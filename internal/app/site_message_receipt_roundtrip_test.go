package app

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/sites"
)

func TestSiteMessageReceiptStillReadableAfterNullableDTOAdaptation(t *testing.T) {
	a := newTestApp(t)
	siteMessageOwnedFIFOSender(t, a)
	ctx := context.Background()
	job := siteMessageClaimBatch(t, a)
	r := siteMessageSeedCollectedDTO(t, a, job, "nullable-receipt", &sites.SourceUserData{MessageUnread: 1, Messages: [][]any{{"head", "date", "body"}}})
	stored, err := a.pluginStore.readSiteMessageCollection(ctx, a.Store.DB, r.ID)
	if err != nil {
		t.Fatal("source collection roundtrip", err)
	}
	stored.Steps[0].Business, err = siteMessageBusinessAt(stored.Name, stored.URL, stored.Snapshot, stored.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	stored.Steps[0].State = "routing"
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.saveSiteMessageCollection(ctx, tx, stored); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	identity := siteMessageIdentity{r.ID, 0}
	route, err := a.initSiteMessageNotice(ctx, identity, stored.Steps[0].Business)
	if err != nil {
		t.Fatal("init existing site notice", err)
	}
	initial, _, err := a.pluginStore.readNoticeRoute(ctx, a.Store.DB, route)
	if err != nil {
		t.Fatal("existing site notice roundtrip before routing", err)
	}
	if err = a.requireNoticeV2Execution(ctx, initial); err != nil {
		t.Fatal("existing site notice execution grant", err)
	}
	if err = a.runNoticeRoute(ctx, route); err != nil {
		current, steps, readErr := a.pluginStore.readNoticeRoute(ctx, a.Store.DB, route)
		kinds := []string{}
		for _, step := range steps {
			kinds = append(kinds, step.Kind)
		}
		t.Log("route diagnostic", current.Count, current.Complete, current.SourceError, kinds, readErr)
		t.Fatal("existing site notice routing", err)
	}
	_, steps, err := a.pluginStore.readNoticeRoute(ctx, a.Store.DB, route)
	if err != nil {
		t.Fatal(err)
	}
	var history pluginNoticeV2History
	for _, step := range steps {
		if step.Kind == "history" && json.Unmarshal(step.Output, &history) != nil {
			t.Fatal("history receipt shape")
		}
	}
	var record notificationRecord
	if err = a.Store.Get(ctx, "notification", history.History.ID, &record); err != nil || len(record.ProtectedContent) == 0 || record.Title != "" || record.Message != "" || record.Link != "" || record.Source != "" {
		t.Fatal("private history leaked into public record", err)
	}
	opened, err := a.openNotification(record)
	if err != nil || opened.Message != "时间：date\n标题：head\n内容：\nbody" {
		t.Fatal("encrypted source history changed", err)
	}
	// An encrypted record remains authenticated, rather than bypassing the
	// original receipt check merely because its public fields are projections.
	changed := record
	changed.ProtectedContent = append([]byte(nil), record.ProtectedContent...)
	changed.ProtectedContent[len(changed.ProtectedContent)-1] ^= 1
	if err = a.Store.Put(ctx, "notification", history.History.ID, changed); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.pluginStore.readNoticeRoute(ctx, a.Store.DB, route); err == nil {
		t.Fatal("corrupted encrypted history accepted")
	}
	if err = a.Store.Put(ctx, "notification", history.History.ID, record); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.pluginStore.readNoticeRoute(ctx, a.Store.DB, route); err != nil {
		t.Fatal("restored authenticated history rejected", err)
	}
}
