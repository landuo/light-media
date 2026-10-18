package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"light-media/internal/notice"
	seed "light-media/internal/plugins/cleaninvalidseed"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

func cleanSeedNoticeTokens(t *testing.T, a *App, instance string) []string {
	t.Helper()
	rows, err := a.Store.DB.Query(`SELECT token FROM plugin_clean_seed_outbox WHERE instance_id=? ORDER BY token`, instance)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tokens []string
	for rows.Next() {
		var token string
		if err = rows.Scan(&token); err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tokens
}

func TestPluginCleanInvalidSeedNoticeValidationPrecedesConfigThroughRuntime(t *testing.T) {
	for _, kind := range []string{"channel", "userid"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			initial := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(nil), true)
			if got := cleanSeedRuntimeExecute(t, a, initial); got.State != "succeeded" {
				t.Fatal(got.State, got.ErrorCode)
			}
			original, err := a.pluginStore.Config(context.Background(), initial.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			event := map[string]any{"action": "toggle_notify_all", "channel": "Web", "user": "owned-user"}
			if kind == "channel" {
				event["channel"] = "not-a-message-channel"
			} else {
				event["user"] = []any{"not-an-id"}
			}
			if _, err = a.pluginStore.enqueueCleanInvalidSeedCommand(context.Background(), initial.InstanceID, event); err != nil {
				t.Fatal(err)
			}
			got := cleanSeedRuntimeExecute(t, a, cleanSeedOwnerClaim(t, a, initial.InstanceID))
			if got.State != "failed" || got.ErrorCode != "CLEAN_SEED_SOURCE_FAILED" {
				t.Fatal("invalid Notification falsely accepted", got.State, got.ErrorCode)
			}
			latest, err := a.pluginStore.Config(context.Background(), initial.InstanceID)
			if err != nil || latest.Revision != original.Revision || !bytes.Equal(latest.Value, original.Value) {
				t.Fatal("invalid first notice changed source config", latest.Revision, err)
			}
			var histories, outbox int
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&histories); err != nil {
				t.Fatal(err)
			}
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_clean_seed_outbox`).Scan(&outbox); err != nil {
				t.Fatal(err)
			}
			if histories != 0 || outbox != 0 {
				t.Fatal("invalid first notice committed effects", histories, outbox)
			}
			journal := a.pluginCleanInvalidSeed.owners[initial.InstanceID].j
			state, err := journal.Read(context.Background())
			var result seedhost.Result
			if err != nil || json.Unmarshal(state.Runs[got.ID].Result, &result) != nil || result.SourceError != "ValidationError" || result.HostError != "" {
				t.Fatal("original constructor failure type lost", result, err)
			}
			event["channel"], event["user"] = "Web", "owned-user"
			if _, err = a.pluginStore.enqueueCleanInvalidSeedCommand(context.Background(), initial.InstanceID, event); err != nil {
				t.Fatal(err)
			}
			if next := cleanSeedRuntimeExecute(t, a, cleanSeedOwnerClaim(t, a, initial.InstanceID)); next.State != "succeeded" {
				t.Fatal("later valid source command blocked", next.State, next.ErrorCode)
			}
			latest, err = a.pluginStore.Config(context.Background(), initial.InstanceID)
			if err != nil || latest.Revision != original.Revision+1 || !bytes.Contains(latest.Value, []byte(`"notify_all":true`)) {
				t.Fatal("valid source command did not update", latest.Revision, err)
			}
		})
	}
}

func TestPluginCleanInvalidSeedNoticeAcceptedEnvelopeAuthenticationAndColdRead(t *testing.T) {
	data := t.TempDir()
	a, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a != nil {
			_ = a.Close()
		}
	})
	a.config = Settings{}
	fixed := time.Date(2026, 10, 18, 1, 2, 3, 4000000, time.UTC)
	a.pluginCleanInvalidSeed.now = func() time.Time { return fixed }
	useBuiltInPluginProcess(t, a)
	initial := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(nil), true)
	if got := cleanSeedRuntimeExecute(t, a, initial); got.State != "succeeded" {
		t.Fatal(got.State, got.ErrorCode)
	}
	const userID = "900719925474099312345678901"
	if _, err = a.pluginStore.enqueueCleanInvalidSeedCommand(context.Background(), initial.InstanceID, map[string]any{"action": "toggle_notify_all", "channel": "Web", "user": json.Number(userID)}); err != nil {
		t.Fatal(err)
	}
	run := cleanSeedOwnerClaim(t, a, initial.InstanceID)
	input := append([]byte(nil), run.Input...)
	if got := cleanSeedRuntimeExecute(t, a, run); got.State != "succeeded" || !bytes.Equal(got.Input, input) {
		t.Fatal(got.State, got.ErrorCode)
	}
	tokens := cleanSeedNoticeTokens(t, a, initial.InstanceID)
	if len(tokens) != 2 {
		t.Fatal("source prefix/final notice missing", len(tokens))
	}
	j := a.pluginCleanInvalidSeed.owners[initial.InstanceID].j
	var first pluginCleanInvalidSeedPrivateNotice
	for _, token := range tokens {
		box, err := readCleanSeedPrivateNotice(context.Background(), a.Store.DB, j, token)
		if err != nil {
			t.Fatal("actual source acceptance was not authenticated", err)
		}
		var envelope notice.Object
		if cleanupJournalStrict(box.Envelope, &envelope) != nil || len(envelope) != 24 || envelope["channel"] != "Web" || envelope["date"] != nil || envelope["source"] != nil || envelope["text"] != nil || envelope["targets"] != nil {
			t.Fatal("complete validated defaults missing")
		}
		value, ok := envelope["userid"].(json.Number)
		if !ok || value.String() != userID || box.CurrentTime != "2026-10-18 09:02:03" || box.CreatedAt != fixed.Format(time.RFC3339Nano) || box.State != "history_saved" {
			t.Fatal("source numeric identity/time was reconstructed or lost")
		}
		var sealed []byte
		if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_clean_seed_outbox WHERE token=?`, token).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte(userID)) {
			t.Fatal("private target is plaintext", err)
		}
		first = box
	}
	// Read-state changes are not new source history or a second route acceptance.
	var history Notification
	if err = a.Store.Get(context.Background(), "notification", first.HistoryID, &history); err != nil {
		t.Fatal(err)
	}
	history.Read = true
	if err = a.Store.Put(context.Background(), "notification", history.ID, history); err != nil {
		t.Fatal(err)
	}
	selected := j.token("outbox", first.Notice.ID, -1)
	if _, err = readCleanSeedPrivateNotice(context.Background(), a.Store.DB, j, selected); err != nil {
		t.Fatal("mark-read invalidated original acceptance", err)
	}
	// Even an internally sealed edited envelope is not the committed source fact.
	raw, err := cleanupJournalJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	tampered := first
	var changed notice.Object
	if cleanupJournalStrict(tampered.Envelope, &changed) != nil {
		t.Fatal("fixture")
	}
	changed["userid"] = "unowned-recipient"
	tampered.Envelope = mustCleanSeedJSON(changed)
	changedRaw := mustCleanSeedJSON(tampered)
	sealed, err := a.pluginStore.seal("clean_seed_journal", j.instance, j.key("outbox", selected), changedRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_clean_seed_outbox SET sealed=? WHERE token=?`, sealed, selected); err != nil {
		t.Fatal(err)
	}
	if _, err = readCleanSeedPrivateNotice(context.Background(), a.Store.DB, j, selected); err == nil {
		t.Fatal("edited recipient accepted as original fact")
	}
	sealed, err = a.pluginStore.seal("clean_seed_journal", j.instance, j.key("outbox", selected), raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_clean_seed_outbox SET sealed=? WHERE token=?`, sealed, selected); err != nil {
		t.Fatal(err)
	}
	// Lose process memory without invoking source StopService; reconstruct from
	// the actual reopened Store, original receipts and sealed source action cells.
	a.logs.close()
	if err = a.Store.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	a, err = New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	j = &pluginCleanInvalidSeedJournal{p: a.pluginStore, instance: initial.InstanceID, generation: initial.Generation}
	box, err := readCleanSeedPrivateNotice(context.Background(), a.Store.DB, j, selected)
	if err != nil || !bytes.Equal(box.Envelope, first.Envelope) || box.CreatedAt != first.CreatedAt {
		t.Fatal("cold read lost exact accepted source envelope", err)
	}
	if err = a.Store.Get(context.Background(), "notification", first.HistoryID, &history); err != nil || !history.Read || history.CreatedAt != fixed.Format(time.RFC3339Nano) {
		t.Fatal("cold read reset first inbox time/read state", err)
	}
	if len(cleanSeedNoticeTokens(t, a, initial.InstanceID)) != 2 {
		t.Fatal("cold historical read created another source notification")
	}
}

func TestPluginCleanInvalidSeedNoticeSourceEnumBecomesOriginalNotificationValue(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	d := cleanSeedOwnerDaemonNew(t)
	d.settings(a)
	initial := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"notify": true}), true)
	if got := cleanSeedRuntimeExecute(t, a, initial); got.State != "succeeded" {
		t.Fatal(got.State, got.ErrorCode)
	}
	if _, err := a.pluginStore.EnqueueRun(context.Background(), initial.InstanceID, "run", nil); err != nil {
		t.Fatal(err)
	}
	if got := cleanSeedRuntimeExecute(t, a, cleanSeedOwnerClaim(t, a, initial.InstanceID)); got.State != "succeeded" {
		t.Fatal("source enum rejected as external string", got.State, got.ErrorCode)
	}
	j := a.pluginCleanInvalidSeed.owners[initial.InstanceID].j
	tokens := cleanSeedNoticeTokens(t, a, initial.InstanceID)
	if len(tokens) == 0 {
		t.Fatal("actual empty downloader source notice missing")
	}
	for _, token := range tokens {
		box, err := readCleanSeedPrivateNotice(context.Background(), a.Store.DB, j, token)
		if err != nil {
			t.Fatal(err)
		}
		var original, envelope notice.Object
		if cleanupJournalStrict(box.Notice.Payload, &original) != nil || cleanupJournalStrict(box.Envelope, &envelope) != nil || original["mtype"] != "SiteMessage" || envelope["mtype"] != "站点" {
			t.Fatal("original enum trace or schema value lost")
		}
		var history Notification
		if err = a.Store.Get(context.Background(), "notification", box.HistoryID, &history); err != nil || history.MType != "站点" {
			t.Fatal("inbox stored enum symbol instead of value", err)
		}
	}
}
