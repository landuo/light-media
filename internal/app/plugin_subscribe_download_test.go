package app

import (
	"context"
	"light-media/internal/plugins/subscribegroup"
	"strings"
	"testing"
)

func subscribeDownloadConfig() subscribegroup.Configuration {
	return subscribegroup.Configuration{Enabled: true, UpdateDetails: []string{"分辨率", "资源质量", "特效", "制作组", "站点"}}
}

func subscribeDownloadSnapshot() downloadAddedSnapshot {
	return downloadAddedSnapshot{DownloadKind: "intent", Hash: strings.Repeat("a", 40), SiteID: "rss-fixture", MediaType: "tv", TMDBID: 206484, Seasons: "S04", HasContext: true, HasHistory: true,
		Metadata: &subscribegroup.Metadata{Resolution: "2160p", Quality: "WEB-DL", Effect: "HDR10", Team: "Team", Customization: "Custom"}}
}

func subscribeDownloadWriterFixture(kind string) (DomainEvent, downloadAddedSnapshot, map[string]any) {
	snapshot := subscribeDownloadSnapshot()
	snapshot.DownloadKind, snapshot.DownloadID = kind, kind+"_writer_fixture"
	event := DomainEvent{Type: "download.accepted", WorkflowID: "wf_writer_fixture", JobID: "job_writer_fixture", MediaID: "tv:206484"}
	if kind == "intent" {
		event.IntentID = snapshot.DownloadID
		event.EventID = domainEventID(event.Type, snapshot.DownloadID)
	} else {
		event.NativeDownloadID = snapshot.DownloadID
		event.EventID = domainEventID(event.Type, "native_download:"+snapshot.DownloadID)
	}
	row := map[string]any{"id": snapshot.DownloadID, "hash": snapshot.Hash, "site_id": snapshot.SiteID, "state": "planned"}
	return event, snapshot, row
}

func TestDownloadAddedWriterRejectsPlannedAndRequiresBusinessTransaction(t *testing.T) {
	for _, kind := range []string{"intent", "native_download"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			event, snapshot, planned := subscribeDownloadWriterFixture(kind)
			if err := a.recordDownloadAddedTx(ctx, nil, event, snapshot); pluginErrorCode(err) != "DOMAIN_EVENT_TX_REQUIRED" {
				t.Fatal("publication did not require owning business transaction", err)
			}
			if _, err := a.domainEvents(ctx, 0, 10); err != nil {
				t.Fatal(err)
			}
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = txPut(ctx, tx, kind, snapshot.DownloadID, planned); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			err = a.recordDownloadAddedTx(ctx, tx, event, snapshot)
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				t.Fatal(rollbackErr)
			}
			if pluginErrorCode(err) != "INVALID_DOWNLOAD_EVENT" {
				t.Fatal("planned downloader row was published as accepted", err)
			}
			page, err := a.domainEvents(ctx, 0, 10)
			var snapshots int
			if err != nil || len(page.Events) != 0 {
				t.Fatal("planned row left a download fact", err)
			}
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='download_added'`).Scan(&snapshots); err != nil || snapshots != 0 {
				t.Fatal("planned row left a frozen download snapshot", err)
			}
		})
	}
}

func TestDownloadAddedOutboxFailureRollsBackAcceptedRowAndFrozenSnapshot(t *testing.T) {
	for _, kind := range []string{"intent", "native_download"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			event, snapshot, row := subscribeDownloadWriterFixture(kind)
			if err := a.Store.Put(ctx, kind, snapshot.DownloadID, row); err != nil {
				t.Fatal(err)
			}
			if _, err := a.domainEvents(ctx, 0, 10); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_download_fact BEFORE INSERT ON domain_events BEGIN SELECT RAISE(ABORT,'synthetic outbox failure'); END`); err != nil {
				t.Fatal(err)
			}
			row["state"] = "accepted"
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = txPut(ctx, tx, kind, snapshot.DownloadID, row); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			err = a.recordDownloadAddedTx(ctx, tx, event, snapshot)
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				t.Fatal(rollbackErr)
			}
			if err == nil {
				t.Fatal("late outbox failure reported successful publication")
			}
			var kept map[string]any
			if err = a.Store.Get(ctx, kind, snapshot.DownloadID, &kept); err != nil || kept["state"] != "planned" {
				t.Fatal("accepted state escaped rollback", err)
			}
			var snapshots int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='download_added'`).Scan(&snapshots); err != nil || snapshots != 0 {
				t.Fatal("private snapshot escaped failed outbox transaction", err)
			}
			if page, err := a.domainEvents(ctx, 0, 10); err != nil || len(page.Events) != 0 {
				t.Fatal("failed outbox left a visible fact", err)
			}
			if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_download_fact`); err != nil {
				t.Fatal(err)
			}
			tx, err = a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err = txPut(ctx, tx, kind, snapshot.DownloadID, row); err != nil {
				t.Fatal(err)
			}
			if err = a.recordDownloadAddedTx(ctx, tx, event, snapshot); err != nil {
				t.Fatal("same accepted fact could not be retried", err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if page, err := a.domainEvents(ctx, 0, 10); err != nil || len(page.Events) != 1 {
				t.Fatal("successful retry did not publish exactly one fact", err)
			}
		})
	}
}
