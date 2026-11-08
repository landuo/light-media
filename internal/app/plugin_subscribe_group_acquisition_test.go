package app

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/plugins/subscribegroup"
)

func TestSubscribeGroupDownloadHistoryWaitsForAcquisition(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := t.Context()
	stamp := now()
	parent := Job{ID: "owned_group_acquisition", Kind: "subscription", State: "running", WorkflowID: "owned_group_workflow", Generation: 1}
	childID := "owned_group_child"
	intent := Intent{
		ID: "owned_group_intent", WorkflowID: parent.WorkflowID, SubscriptionAcquisition: parent.ID,
		SubscriptionID: "owned_group_subscription", SubscriptionRevision: 3,
		Media:  catalog.Media{ID: 812, MediaType: "tv", Title: "Owned acquisition series"},
		Season: 2, Episodes: []int{4, 5}, SiteID: "owned_group_site", DownloaderID: "owned_group_downloader",
		Hash: "0123456789abcdef0123456789abcdef01234567", State: "accepted", DownloadPath: "/owned/downloads",
	}
	// Seed only the accepted fact and job state. This is an isolated SQLite
	// setup, not an acquisition run or a call to a downloader.
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, job := range []Job{parent, {ID: childID, WorkflowID: parent.WorkflowID, Kind: "subscription_download", State: "succeeded"}} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,workflow_id,kind,state,payload,generation,run_at,created_at,updated_at) VALUES(?,?,?,?,'{}',?,0,?,?)`, job.ID, job.WorkflowID, job.Kind, job.State, job.Generation, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err = txPut(ctx, tx, "intent", intent.ID, intent); err != nil {
		t.Fatal(err)
	}
	event := DomainEvent{
		EventID: domainEventID("download.accepted", intent.ID), Type: "download.accepted", OccurredAt: stamp,
		WorkflowID: intent.WorkflowID, JobID: childID, IntentID: intent.ID, MediaID: mediaKey(intent.Media),
	}
	fact := downloadAddedSnapshot{
		DownloadID: intent.ID, DownloadKind: "intent", Hash: intent.Hash, SiteID: intent.SiteID,
		MediaType: intent.Media.MediaType, TMDBID: intent.Media.ID, HasContext: true, HasHistory: true,
		Context: &downloadContextSnapshot{
			DownloadID: intent.ID, DownloadKind: "intent", Hash: intent.Hash,
			Metadata: &subscribegroup.Metadata{Quality: "WEB-DL"}, Seasons: "S02-S03",
			Original: &originalDownloadContext{Meta: json.RawMessage(`{"resource_type":"WEB-DL"}`)},
		},
	}
	if err = a.recordDownloadAddedTx(ctx, tx, event, fact); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	readCtx, cancelRead := context.WithTimeout(ctx, 5*time.Second)
	defer cancelRead()
	type historyRead struct {
		result plugins.DownloadHistoryResultV1
		err    error
	}
	started := make(chan struct{})
	done := make(chan historyRead, 1)
	go func() {
		close(started)
		result, err := a.originalSubscribeGroupDownloadHistory(readCtx, intent.Hash)
		done <- historyRead{result, err}
	}()
	<-started
	select {
	case read := <-done:
		t.Fatalf("history returned while the parent was running: %+v, %v", read.result.Item, read.err)
	case <-time.After(150 * time.Millisecond):
	}

	// App uses one SQLite connection. A read transaction held during the wait
	// would block this real job transition and hit the independent deadline.
	finishCtx, cancelFinish := context.WithTimeout(ctx, 2*time.Second)
	defer cancelFinish()
	if changed, err := a.finishJob(finishCtx, parent, "succeeded", "", "", 0); err != nil || !changed {
		t.Fatalf("parent could not finish while the history read was waiting: changed=%v, err=%v", changed, err)
	}
	select {
	case read := <-done:
		row := read.result.Item
		if read.err != nil || row == nil || row.ID != intent.ID || row.Hash != intent.Hash || row.Type != "电视剧" || row.TMDBID != intent.Media.ID || row.Season != intent.Season || !slices.Equal(row.Episodes, intent.Episodes) || row.Seasons == nil || *row.Seasons != "S02-S03" {
			t.Fatalf("accepted source history was not released with its frozen data: %+v, %v", row, read.err)
		}
	case <-readCtx.Done():
		t.Fatal("history did not resume after the parent completed", readCtx.Err())
	}
	var stored Intent
	if err := a.Store.Get(ctx, "intent", intent.ID, &stored); err != nil || !reflect.DeepEqual(stored, intent) {
		t.Fatalf("the compatibility guard changed the accepted Intent: %+v, %v", stored, err)
	}
}
