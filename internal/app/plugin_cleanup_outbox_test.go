package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

func cleanupOutboxCommit(t *testing.T, f *removeLinkEffectsFixture, kind string) cleanuphost.Effect {
	t.Helper()
	path := f.history.Source
	if kind == "notification" {
		path = f.roots[0].Root.Path
	}
	x := f.prepare(t, path, kind)
	if ok, err := f.e.Commit(context.Background(), x); err != nil || !ok {
		t.Fatal("source effect did not commit", ok, err)
	}
	return x
}

func cleanupFileEvents(t *testing.T, a *App) []DomainEvent {
	t.Helper()
	page, err := a.domainEvents(context.Background(), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	out := []DomainEvent{}
	for _, event := range page.Events {
		if event.Type == "downloadfile.deleted" {
			out = append(out, event)
		}
	}
	return out
}

func cleanupHandoff(t *testing.T, f *removeLinkEffectsFixture, x cleanuphost.Effect) pluginCleanupHandoff {
	t.Helper()
	var sealed []byte
	token := f.e.token(x.ID)
	if err := f.a.Store.DB.QueryRow(`SELECT sealed FROM plugin_cleanup_outbox_handoffs WHERE instance_id=? AND generation=? AND token=?`, f.b.InstanceID, f.b.Generation, token).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	raw, err := f.a.pluginStore.unseal("cleanup_journal", f.b.InstanceID, f.e.aad("handoff", token), sealed)
	if err != nil {
		t.Fatal(err)
	}
	var handoff pluginCleanupHandoff
	if cleanupJournalStrict(raw, &handoff) != nil || handoff.Version != 1 || handoff.Kind != x.Kind {
		t.Fatal("invalid transfer receipt")
	}
	identity, original, err := f.e.identity(x)
	if err != nil || handoff.Identity != identity || !bytes.Equal(handoff.Effect, original) {
		t.Fatal("transfer receipt not bound to original authorized effect")
	}
	return handoff
}

func TestPluginRemoveLinkOutboxPublishesSrcWithoutInventingHashOrDeletingDownloads(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"delete_torrents": true})
	ctx := context.Background()
	f.authorize(t, ctx)
	var downloaderCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloaderCalls.Add(1)
		http.Error(w, "unexpected downloader operation", 500)
	}))
	defer server.Close()
	f.a.cfgMu.Lock()
	f.a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "synthetic-qb", Kind: "qbittorrent", BaseURL: server.URL}}}
	f.a.cfgMu.Unlock()
	before := map[string]string{"hash": strings.Repeat("a", 40), "path": f.history.Source, "state": "seeding"}
	if err := f.a.Store.Put(ctx, "native_download", "owned-source-record", before); err != nil {
		t.Fatal(err)
	}
	x := cleanupOutboxCommit(t, f, "download_file_deleted")
	if err := f.a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	events := cleanupFileEvents(t, f.a)
	if len(events) != 1 || events[0].FileEventID == "" || events[0].IntentID != "" || events[0].NativeDownloadID != "" || events[0].SubjectKind != "file_event" {
		t.Fatal("wrong source event", events)
	}
	private, err := f.a.pluginStore.downloadFileEvent(ctx, f.a.Store.DB, events[0].FileEventID)
	if err != nil || private.Src != f.history.Source || private.EffectID != x.ID || private.Instance != f.b.InstanceID || private.Generation != f.b.Generation || private.Revision != f.b.Revision {
		t.Fatal("source payload changed", err)
	}
	public, _ := json.Marshal(events)
	for _, forbidden := range []string{f.history.Source, strings.Repeat("a", 40), "hash", "payload", "src", x.ID} {
		if bytes.Contains(public, []byte(forbidden)) {
			t.Fatal("private data or invented hash exposed")
		}
	}
	if h := cleanupHandoff(t, f, x); h.Stage != "published" || h.Reference != private.ID {
		t.Fatal("publication was not recorded accurately", h.Stage)
	}
	var after map[string]string
	if err := f.a.Store.Get(ctx, "native_download", "owned-source-record", &after); err != nil || !reflect.DeepEqual(before, after) || downloaderCalls.Load() != 0 {
		t.Fatal("src-only source event mutated downloader/history", err, downloaderCalls.Load())
	}
	for _, path := range []string{f.history.Source, f.history.Target} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("outbox consumer touched a file", err)
		}
	}
	for range 3 {
		if err := f.a.dispatchPluginEvents(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if len(cleanupFileEvents(t, f.a)) != 1 || cleanupEffectsCount(t, f.a, "plugin_cleanup_outbox_handoffs") != 1 {
		t.Fatal("repeated dispatcher duplicated event")
	}
}

func TestPluginRemoveLinkOutboxHistorySavedIsNotExternalDelivery(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"notify": true})
	ctx := context.Background()
	f.authorize(t, ctx)
	x := f.prepare(t, f.roots[0].Root.Path, "notification")
	secret := "synthetic-notification-secret-714825"
	text := strings.Repeat("完整消息", 850) + secret
	f.a.cfgMu.Lock()
	f.a.config.TMDB.APIKey = secret
	f.a.cfgMu.Unlock()
	x.Payload, _ = json.Marshal(map[string]string{"title": "清理结果", "text": text})
	if err := f.j.Update(ctx, func(s *cleanuphost.Durable) error { s.Effects[x.ID] = x; return nil }); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.e.Commit(ctx, x); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err != nil {
		t.Fatal(err)
	}
	h := cleanupHandoff(t, f, x)
	if h.Stage != "history_saved" || len(cleanupFileEvents(t, f.a)) != 0 {
		t.Fatal("notification mislabeled as delivered")
	}
	var notice Notification
	if err := f.a.Store.Get(ctx, "notification", h.Reference, &notice); err != nil || notice.Title != "清理结果" || notice.Message != strings.Repeat("完整消息", 850)+"[REDACTED]" || notice.MType != "站点" || notice.Link != "" {
		t.Fatal("original history content/type/link lost or secret exposed", err)
	}
	notice.Read = true
	if err := f.a.Store.Put(ctx, "notification", notice.ID, notice); err != nil {
		t.Fatal(err)
	}
	if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err != nil {
		t.Fatal(err)
	}
	var replay Notification
	if err := f.a.Store.Get(ctx, "notification", notice.ID, &replay); err != nil || !reflect.DeepEqual(notice, replay) {
		t.Fatal("replay reset the user's read state", err)
	}
	var sealed []byte
	token := f.e.token(x.ID)
	if err := f.a.Store.DB.QueryRow(`SELECT sealed FROM plugin_cleanup_private_outbox WHERE token=?`, token).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	raw, err := f.a.pluginStore.unseal("cleanup_journal", f.b.InstanceID, f.e.aad("outbox", token), sealed)
	var box pluginRemoveLinkPrivateOutbox
	if err != nil || cleanupJournalStrict(raw, &box) != nil || box.State != "history_saved" {
		t.Fatal("external dispatch was incorrectly marked delivered", err)
	}
}

func TestPluginRemoveLinkOutboxTransferRollsBackEveryPublishedFact(t *testing.T) {
	for _, test := range []struct{ name, kind, trigger string }{
		{"private_fact", "download_file_deleted", `BEFORE INSERT ON plugin_download_file_events`},
		{"event", "download_file_deleted", `BEFORE INSERT ON domain_events`},
		{"handoff", "download_file_deleted", `BEFORE INSERT ON plugin_cleanup_outbox_handoffs`},
		{"final_checkpoint", "download_file_deleted", `BEFORE UPDATE ON plugin_cleanup_private_outbox`},
		{"notification", "notification", `BEFORE INSERT ON records WHEN NEW.kind='notification'`},
		{"notification_handoff", "notification", `BEFORE INSERT ON plugin_cleanup_outbox_handoffs`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"notify": true, "delete_torrents": true})
			ctx := context.Background()
			f.authorize(t, ctx)
			x := cleanupOutboxCommit(t, f, test.kind)
			if _, err := f.a.Store.DB.Exec(`CREATE TRIGGER fail_outbox_transfer ` + test.trigger + ` BEGIN SELECT RAISE(FAIL,'owned fixture transfer failure'); END`); err != nil {
				t.Fatal(err)
			}
			if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err == nil {
				t.Fatal("failed transfer was accepted")
			}
			for _, table := range []string{"plugin_download_file_events", "plugin_cleanup_outbox_handoffs"} {
				if cleanupEffectsCount(t, f.a, table) != 0 {
					t.Fatal("partial transfer escaped rollback", table)
				}
			}
			var notices int
			if err := f.a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&notices); err != nil || notices != 0 || len(cleanupFileEvents(t, f.a)) != 0 {
				t.Fatal("business result escaped rollback", err)
			}
			if _, err := f.a.Store.DB.Exec(`DROP TRIGGER fail_outbox_transfer`); err != nil {
				t.Fatal(err)
			}
			if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err != nil {
				t.Fatal(err)
			}
			if h := cleanupHandoff(t, f, x); h.Stage != "published" && h.Stage != "history_saved" {
				t.Fatal(h.Stage)
			}
		})
	}
}

func TestPluginRemoveLinkOutboxRejectsUnprovenOrChangedPrivateIntents(t *testing.T) {
	for _, which := range []string{"missing_receipt", "changed_path", "ciphertext", "generation", "state", "token"} {
		t.Run(which, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"delete_torrents": true})
			ctx := context.Background()
			f.authorize(t, ctx)
			x := cleanupOutboxCommit(t, f, "download_file_deleted")
			token := f.e.token(x.ID)
			switch which {
			case "missing_receipt":
				_, _ = f.a.Store.DB.Exec(`DELETE FROM plugin_cleanup_core_receipts WHERE token=?`, token)
			case "generation":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_cleanup_private_outbox SET generation=generation+1 WHERE token=?`, token)
			case "token":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_cleanup_private_outbox SET token=? WHERE token=?`, strings.Repeat("0", 64), token)
			case "ciphertext":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_cleanup_private_outbox SET sealed=x'00' WHERE token=?`, token)
			default:
				_, original, err := f.e.identity(x)
				if err != nil {
					t.Fatal(err)
				}
				box := pluginRemoveLinkPrivateOutbox{Version: 1, Effect: original, State: "pending"}
				if which == "changed_path" {
					x.Path += ".different"
					x.Payload, _ = json.Marshal(map[string]string{"src": x.Path})
					_, box.Effect, err = f.e.identity(x)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					box.State = "published"
				}
				data, _ := json.Marshal(box)
				sealed, err := f.a.pluginStore.seal("cleanup_journal", f.b.InstanceID, f.e.aad("outbox", token), data)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_cleanup_private_outbox SET sealed=? WHERE token=?`, sealed, token)
			}
			if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err == nil {
				t.Fatal("unproven intent was published", which)
			}
			if len(cleanupFileEvents(t, f.a)) != 0 || cleanupEffectsCount(t, f.a, "plugin_download_file_events") != 0 || cleanupEffectsCount(t, f.a, "plugin_cleanup_outbox_handoffs") != 0 {
				t.Fatal("rejected intent left a result")
			}
		})
	}
}

func TestPluginRemoveLinkOutboxPreservesInsertionOrderAcrossClockRegression(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"delete_torrents": true})
	ctx := context.Background()
	f.authorize(t, ctx)
	first := cleanupOutboxCommit(t, f, "download_file_deleted")
	second := cleanupOutboxCommit(t, f, "download_file_deleted")
	for i, x := range []cleanuphost.Effect{first, second} {
		at := time.Now().Add(time.Duration(1-i) * time.Hour).UTC().Format(time.RFC3339Nano)
		if _, err := f.a.Store.DB.Exec(`UPDATE plugin_cleanup_private_outbox SET created_at=? WHERE token=?`, at, f.e.token(x.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.a.dispatchRemoveLinkOutboxes(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if h := cleanupHandoff(t, f, first); h.Stage != "published" {
		t.Fatal(h.Stage)
	}
	if cleanupEffectsCount(t, f.a, "plugin_cleanup_outbox_handoffs") != 1 {
		t.Fatal("bounded transfer ignored limit")
	}
	if err := f.a.dispatchRemoveLinkOutboxes(ctx, 1); err != nil {
		t.Fatal(err)
	}
	events := cleanupFileEvents(t, f.a)
	if len(events) != 2 {
		t.Fatal(len(events))
	}
	for i, x := range []cleanuphost.Effect{first, second} {
		private, err := f.a.pluginStore.downloadFileEvent(ctx, f.a.Store.DB, events[i].FileEventID)
		if err != nil || private.EffectID != x.ID {
			t.Fatal("source queue order changed", i, err)
		}
	}
}

func TestPluginRemoveLinkOutboxCommittedIntentSurvivesRetirementAndRestart(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"delete_torrents": true, "notify": true})
	ctx := context.Background()
	release := f.authorize(t, ctx)
	x := cleanupOutboxCommit(t, f, "download_file_deleted")
	cleanupOutboxCommit(t, f, "notification")
	release()
	if _, err := f.a.Store.DB.Exec(`UPDATE plugin_installations SET installed=0,desired_enabled=0,generation=generation+1 WHERE instance_id=?`, f.b.InstanceID); err != nil {
		t.Fatal(err)
	}
	dir := f.a.Dir
	if err := f.a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	events := cleanupFileEvents(t, a)
	if len(events) != 1 || cleanupEffectsCount(t, a, "plugin_cleanup_outbox_handoffs") != 2 {
		t.Fatal("committed intent lost after producer retired")
	}
	private, err := a.pluginStore.downloadFileEvent(ctx, a.Store.DB, events[0].FileEventID)
	if err != nil || private.EffectID != x.ID || private.Generation != f.b.Generation {
		t.Fatal("old intent borrowed new generation", err)
	}
	for range 2 {
		if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if len(cleanupFileEvents(t, a)) != 1 {
		t.Fatal("recovery replayed a published event")
	}
}

func TestPluginRemoveLinkOutboxConcurrentDispatchPublishesOneFact(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"delete_torrents": true})
	ctx := context.Background()
	f.authorize(t, ctx)
	cleanupOutboxCommit(t, f, "download_file_deleted")
	var wg sync.WaitGroup
	errors := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- f.a.dispatchRemoveLinkOutboxes(ctx, 200) }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(cleanupFileEvents(t, f.a)) != 1 || cleanupEffectsCount(t, f.a, "plugin_cleanup_outbox_handoffs") != 1 {
		t.Fatal("parallel poll duplicated a committed event")
	}
}

func TestDownloadFileDomainEventRejectsUnbackedOrAdditionalAuthority(t *testing.T) {
	a := newTestApp(t)
	id := "pfe_" + strings.Repeat("a", 64)
	base := DomainEvent{Type: "downloadfile.deleted", FileEventID: id, EventID: domainEventID("downloadfile.deleted", id)}
	for _, mutate := range []func(*DomainEvent){
		func(e *DomainEvent) { e.IntentID = "invented-intent" },
		func(e *DomainEvent) { e.NativeDownloadID = "invented-download" },
		func(e *DomainEvent) { e.FileEventID = "/private/file" },
		func(e *DomainEvent) { e.Origin = "plugin" },
		func(e *DomainEvent) { e.Purpose = "cross_seed" },
		func(e *DomainEvent) { e.MediaID = "movie:123" },
		func(e *DomainEvent) { e.EventID = domainEventID(e.Type, "other") },
		func(e *DomainEvent) { e.SubjectKind = "download" },
		func(e *DomainEvent) { e.JobID = "unrelated-run" },
	} {
		v := base
		mutate(&v)
		if _, err := validateDomainEvent(v); err == nil {
			t.Fatal("additional authority accepted")
		}
	}
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := recordDomainEventTx(context.Background(), tx, base); pluginErrorCode(err) != "FILE_EVENT_UNCOMMITTED" {
		t.Fatal("unbacked private event accepted", err)
	}
	core := domainFixture("download.accepted", "core")
	core.FileEventID = id
	if _, err := validateDomainEvent(core); err == nil {
		t.Fatal("file fact attached to unrelated core download")
	}
}

func TestPluginRemoveLinkOutboxHandoffAuditDetectsCorruptTransferredFacts(t *testing.T) {
	for _, which := range []string{"ciphertext", "identity", "stage", "reference", "missing_private", "missing_event", "missing_source_receipt", "source_state", "notice_changed"} {
		t.Run(which, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"delete_torrents": true, "notify": true})
			ctx := context.Background()
			f.authorize(t, ctx)
			kind := "download_file_deleted"
			if which == "notice_changed" {
				kind = "notification"
			}
			x := cleanupOutboxCommit(t, f, kind)
			if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err != nil {
				t.Fatal(err)
			}
			h := cleanupHandoff(t, f, x)
			token := f.e.token(x.ID)
			if _, err := f.a.auditRemoveLinkHandoffs(ctx, pluginCleanupAuditCursor{}, 200); err != nil {
				t.Fatal("valid handoff failed its own audit", err)
			}
			switch which {
			case "ciphertext":
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_cleanup_outbox_handoffs SET sealed=x'00' WHERE token=?`, token)
			case "missing_private":
				_, _ = f.a.Store.DB.Exec(`DELETE FROM plugin_download_file_events`)
			case "missing_event":
				_, _ = f.a.Store.DB.Exec(`DELETE FROM domain_events WHERE event_id=?`, domainEventID("downloadfile.deleted", h.Reference))
			case "missing_source_receipt":
				_, _ = f.a.Store.DB.Exec(`DELETE FROM plugin_cleanup_core_receipts WHERE token=?`, token)
			case "source_state":
				var sealed []byte
				if err := f.a.Store.DB.QueryRow(`SELECT sealed FROM plugin_cleanup_private_outbox WHERE token=?`, token).Scan(&sealed); err != nil {
					t.Fatal(err)
				}
				raw, err := f.a.pluginStore.unseal("cleanup_journal", f.b.InstanceID, f.e.aad("outbox", token), sealed)
				if err != nil {
					t.Fatal(err)
				}
				var box pluginRemoveLinkPrivateOutbox
				if cleanupJournalStrict(raw, &box) != nil {
					t.Fatal("decode")
				}
				box.State = "pending"
				raw, _ = json.Marshal(box)
				sealed, err = f.a.pluginStore.seal("cleanup_journal", f.b.InstanceID, f.e.aad("outbox", token), raw)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_cleanup_private_outbox SET sealed=? WHERE token=?`, sealed, token)
			case "notice_changed":
				var n Notification
				if err := f.a.Store.Get(ctx, "notification", h.Reference, &n); err != nil {
					t.Fatal(err)
				}
				n.Message = "another value at the same key"
				if err := f.a.Store.Put(ctx, "notification", n.ID, n); err != nil {
					t.Fatal(err)
				}
			default:
				switch which {
				case "identity":
					h.Identity = strings.Repeat("0", 64)
				case "stage":
					h.Stage = "delivered"
				case "reference":
					h.Reference = "pfe_" + strings.Repeat("0", 64)
				}
				raw, _ := json.Marshal(h)
				sealed, err := f.a.pluginStore.seal("cleanup_journal", f.b.InstanceID, f.e.aad("handoff", token), raw)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.a.Store.DB.Exec(`UPDATE plugin_cleanup_outbox_handoffs SET sealed=? WHERE token=?`, sealed, token)
			}
			if _, err := f.a.auditRemoveLinkHandoffs(ctx, pluginCleanupAuditCursor{}, 200); err == nil {
				t.Fatal("corrupt handoff was accepted", which)
			}
			if err := f.a.dispatchPluginEvents(ctx, time.Now()); err == nil {
				t.Fatal("actual dispatcher silently skipped corrupt receipt", which)
			}
		})
	}
}

func TestPluginRemoveLinkOutboxForgedExistenceAndLostHandoffDoNotAcknowledgeIntent(t *testing.T) {
	for _, which := range []string{"forged_existence", "lost_handoff"} {
		t.Run(which, func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"delete_torrents": true})
			ctx := context.Background()
			f.authorize(t, ctx)
			x := cleanupOutboxCommit(t, f, "download_file_deleted")
			token := f.e.token(x.ID)
			if which == "forged_existence" {
				if _, err := f.a.Store.DB.Exec(`INSERT INTO plugin_cleanup_outbox_handoffs VALUES(?,?,?,x'00')`, f.b.InstanceID, f.b.Generation, token); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err != nil {
					t.Fatal(err)
				}
				if _, err := f.a.Store.DB.Exec(`DELETE FROM plugin_cleanup_outbox_handoffs`); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.a.dispatchPluginEvents(ctx, time.Now()); err == nil {
				t.Fatal("incomplete handoff accepted", which)
			}
			want := 0
			if which == "lost_handoff" {
				want = 1
			}
			if len(cleanupFileEvents(t, f.a)) != want {
				t.Fatal("unproven transfer was replayed")
			}
		})
	}
}

func TestPluginRemoveLinkOutboxNoticeIDConflictNeverOverwritesReadMessage(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching", true: "conflict"}[conflict], func(t *testing.T) {
			f := removeLinkEffectsNew(t, map[string]any{"notify": true})
			ctx := context.Background()
			f.authorize(t, ctx)
			x := cleanupOutboxCommit(t, f, "notification")
			token := f.e.token(x.ID)
			var created string
			if err := f.a.Store.DB.QueryRow(`SELECT created_at FROM plugin_cleanup_private_outbox WHERE token=?`, token).Scan(&created); err != nil {
				t.Fatal(err)
			}
			id := "cleanup_notice_" + hex.EncodeToString(f.j.mac("notice-history", []byte(token)))
			n := Notification{ID: id, Title: "private notice title", Message: "private notice body", MType: "站点", CreatedAt: created, Read: true}
			// New v2 acceptance freezes an empty domain as an empty history link.
			// Genuine legacy fragment behavior is covered separately.
			if conflict {
				n.Title = "a different pre-existing message"
			}
			if err := f.a.Store.Put(ctx, "notification", id, n); err != nil {
				t.Fatal(err)
			}
			err := f.a.dispatchRemoveLinkOutboxes(ctx, 200)
			if (err != nil) != conflict {
				t.Fatal("incorrect conflict result", err)
			}
			var actual Notification
			if err := f.a.Store.Get(ctx, "notification", id, &actual); err != nil || actual != n {
				t.Fatal("existing notification was overwritten", err)
			}
			if !conflict {
				if _, err := f.a.auditRemoveLinkHandoffs(ctx, pluginCleanupAuditCursor{}, 200); err != nil {
					t.Fatal("read flag invalidated receipt", err)
				}
			}
		})
	}
}

func TestPluginRemoveLinkOutboxAuditRoundDoesNotChaseNewAppendsForever(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"delete_torrents": true})
	ctx := context.Background()
	f.authorize(t, ctx)
	first := cleanupOutboxCommit(t, f, "download_file_deleted")
	cleanupOutboxCommit(t, f, "download_file_deleted")
	if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err != nil {
		t.Fatal(err)
	}
	cursor, err := f.a.auditRemoveLinkHandoffs(ctx, pluginCleanupAuditCursor{}, 1)
	if err != nil || cursor.After == 0 || cursor.Through <= cursor.After {
		t.Fatal("audit did not freeze the original round", cursor, err)
	}
	// Append one full page while that round is in progress, then corrupt a
	// previously checked row. A moving max would postpone its recheck forever.
	cleanupOutboxCommit(t, f, "download_file_deleted")
	if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.Store.DB.Exec(`UPDATE plugin_cleanup_outbox_handoffs SET sealed=x'00' WHERE token=?`, f.e.token(first.ID)); err != nil {
		t.Fatal(err)
	}
	cursor, err = f.a.auditRemoveLinkHandoffs(ctx, cursor, 1)
	if err != nil || cursor != (pluginCleanupAuditCursor{}) {
		t.Fatal("new append extended the old round", cursor, err)
	}
	if _, err := f.a.auditRemoveLinkHandoffs(ctx, cursor, 1); err == nil {
		t.Fatal("next round did not recheck old corrupt handoff")
	}
}

func cleanupOutboxRegisterConsumer(t *testing.T, a *App, types []string) {
	t.Helper()
	ctx := context.Background()
	installStoredPlugin(t, a.pluginStore, "outbox-consumer")
	if _, _, err := a.pluginStore.SaveConfigAndQueue(ctx, "outbox-consumer", 0, map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || run.InstanceID != "outbox-consumer" {
		t.Fatal(run.InstanceID, err)
	}
	if err := a.pluginStore.configureEventSubscription(ctx, run, "source-events", types, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestPluginRemoveLinkOutboxUsesExistingBoundConsumerBatch(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"delete_torrents": true})
	ctx := context.Background()
	f.authorize(t, ctx)
	cleanupOutboxRegisterConsumer(t, f.a, []string{"downloadfile.deleted"})
	cleanupOutboxCommit(t, f, "download_file_deleted")
	if err := f.a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	run, err := f.a.pluginStore.ClaimRun(ctx)
	if err != nil || run.InstanceID != "outbox-consumer" || run.Action != "event" {
		t.Fatal(run.InstanceID, run.Action, err)
	}
	var batchID string
	if err := f.a.Store.DB.QueryRow(`SELECT id FROM plugin_event_batches WHERE run_id=?`, run.ID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	page, err := f.a.pluginStore.pluginBatchEvents(ctx, run, batchID, 0, 200)
	if err != nil || len(page.Events) != 1 || page.Events[0].Type != "downloadfile.deleted" || page.Events[0].FileEventID == "" || !page.Complete {
		t.Fatal("source event did not enter existing batch path", page, err)
	}
	public, _ := json.Marshal(page)
	if bytes.Contains(public, []byte(f.history.Source)) {
		t.Fatal("consumer batch exposed private path")
	}
	if err := f.a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if cleanupEffectsCount(t, f.a, "plugin_event_deliveries") != 1 || cleanupEffectsCount(t, f.a, "plugin_event_batches") != 1 {
		t.Fatal("same event was batched twice")
	}
}

func TestPluginRemoveLinkOutboxAuditFailureStillDrainsUnrelatedPluginEvents(t *testing.T) {
	f := removeLinkEffectsNew(t, map[string]any{"delete_torrents": true})
	ctx := context.Background()
	f.authorize(t, ctx)
	cleanupOutboxRegisterConsumer(t, f.a, []string{"import.completed"})
	x := cleanupOutboxCommit(t, f, "download_file_deleted")
	if err := f.a.dispatchRemoveLinkOutboxes(ctx, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.Store.DB.Exec(`UPDATE plugin_cleanup_outbox_handoffs SET sealed=x'00' WHERE token=?`, f.e.token(x.ID)); err != nil {
		t.Fatal(err)
	}
	commitDomainFixture(t, f.a, domainFixture("import.completed", "independent-after-corrupt-handoff"))
	cursor := f.a.pluginCleanupOutboxAudit
	if err := f.a.dispatchPluginEvents(ctx, time.Now()); err == nil {
		t.Fatal("bad receipt did not surface")
	}
	if f.a.pluginCleanupOutboxAudit != cursor {
		t.Fatal("failed integrity audit advanced cursor")
	}
	run, err := f.a.pluginStore.ClaimRun(ctx)
	if err != nil || run.InstanceID != "outbox-consumer" || run.Action != "event" {
		t.Fatal("outbox error starved unrelated committed events", run.InstanceID, run.Action, err)
	}
}

func TestPluginRemoveLinkOutboxActualSDKOwnerCleanupFeedsEventBusAndInbox(t *testing.T) {
	a, item := historyRemovalFixture(t)
	ctx := context.Background()
	removeLinkOwnerFakeWatch(a)
	cfg := map[string]any{"enabled": true, "monitor_dirs": a.Settings().AllowedRoots[0] + "\n" + a.Settings().Roots[0].Path, "delayed_deletion": false, "delete_history": false, "delete_torrents": true, "notify": true, "scrap": false}
	run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), cfg, true)
	removeLinkOwnerWorker(t, a, run)
	o := a.pluginRemoveLink.owners[run.InstanceID]
	if o == nil || o.h == nil {
		t.Fatal("actual owner was not started by SDK")
	}
	if err := os.Remove(item.Source); err != nil {
		t.Fatal(err)
	}
	if err := o.h.Observe(ctx, cleanuphost.Event{ID: "owned-cleanup-to-outbox", Kind: "deleted", Mode: "hardlink", Path: item.Source, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	state, err := o.j.Read(ctx)
	if err != nil || state.Blocked != "" || state.Active != nil {
		t.Fatal("actual owner failed before committing source effects", state.Blocked, err)
	}
	if _, err := os.Stat(item.Target); !os.IsNotExist(err) {
		t.Fatal("owned hardlink was not actually removed", err)
	}
	if cleanupEffectsCount(t, a, "plugin_cleanup_private_outbox") != 3 {
		t.Fatal("owner did not commit the two source events and one notice")
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	events := cleanupFileEvents(t, a)
	if len(events) != 2 || cleanupEffectsCount(t, a, "plugin_cleanup_outbox_handoffs") != 3 {
		t.Fatal("source effects were not handed over", len(events))
	}
	var notices int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification' AND json_extract(payload,'$.mtype')='站点'`).Scan(&notices); err != nil || notices != 1 {
		t.Fatal("source batch notice is not in the inbox", notices, err)
	}
	for _, event := range events {
		private, err := a.pluginStore.downloadFileEvent(ctx, a.Store.DB, event.FileEventID)
		if err != nil || private.Src != item.Source && private.Src != item.Target {
			t.Fatal("wrong cleanup event body", err)
		}
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(cleanupFileEvents(t, a)) != 2 {
		t.Fatal("actual owner effects replayed")
	}
}

func TestPluginRemoveLinkOutboxInstalledSourceReferenceIntegrity(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "plugins", "remove-link-outbox")
	for file, want := range map[string]string{
		"installed-source.json":          "f72c9142c01970d2ddabe03ddfda1012a14c65c8c4003c15f287ee2ad4277f0f",
		"installed-function-oracle.json": "ff03a5032d05e6e7baa206a61f9c9eef46a1634f282a3aa1c5bdc8218efb0f00",
		"generate_oracle.py":             "cc7c91b289dc5e28ae4405d7ab0df181581fd066bce5f83cb70d76978bb67cd6",
	} {
		raw, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("installed reference changed without review", file)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "installed-function-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Downloads []struct {
			Name  string            `json:"name"`
			Trace []json.RawMessage `json:"trace"`
		} `json:"download_cases"`
		Notifications []json.RawMessage `json:"notification_cases"`
		Defaults      struct {
			MType                   string `json:"mtype"`
			Link                    string `json:"link"`
			Source, UserID, Channel any
			SaveHistory             bool `json:"save_history"`
		} `json:"plugin_notification_defaults"`
		Broadcast struct {
			Queued   int `json:"queued"`
			Handlers int `json:"handlers_called_inline"`
		} `json:"broadcast_enqueue"`
	}
	// The 17 AST scenarios establish source boundaries; the hashful downloader
	// and routing scenarios are not counted as implemented Go consumer branches.
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle.Downloads) != 8 || len(oracle.Notifications) != 7 {
		t.Fatal("source oracle inventory incomplete")
	}
	for _, v := range oracle.Downloads {
		if strings.HasPrefix(v.Name, "src-only-") && len(v.Trace) != 0 {
			t.Fatal("installed core unexpectedly acts on src-only event")
		}
	}
	if oracle.Defaults.MType != "站点" || !oracle.Defaults.SaveHistory || oracle.Defaults.Source != nil || oracle.Defaults.UserID != nil || oracle.Defaults.Channel != nil || !strings.HasSuffix(oracle.Defaults.Link, "#/plugins?tab=installed&id=RemoveLink") {
		t.Fatal("source notification defaults changed")
	}
	if oracle.Broadcast.Queued != 1 || oracle.Broadcast.Handlers != 0 {
		t.Fatal("source enqueue unexpectedly acknowledges inline delivery")
	}
}
