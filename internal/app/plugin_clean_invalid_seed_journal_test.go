package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

func cleanSeedJournalFixture(t *testing.T) (*Store, *PluginStore, *pluginCleanInvalidSeedJournal) {
	t.Helper()
	s, p := pluginStoreFixture(t)
	j, err := newPluginCleanInvalidSeedJournal(context.Background(), p, "clean-journal", 2)
	if err != nil {
		t.Fatal(err)
	}
	return s, p, j
}
func cleanSeedJournalState() seedhost.State {
	b := seedhost.Binding{InstanceID: "clean-journal", Generation: 2, Revision: 3}
	inv := seedhost.Invocation{Binding: b, RunID: "source-run", Action: "init", InputSHA: "immutable-input"}
	queued := seedhost.Invocation{Binding: b, RunID: "queued-run", Action: "event", EventID: "source-event", InputSHA: "event-input"}
	return seedhost.State{Binding: b, Config: json.RawMessage("{ \"enabled\" : false,\"n\":9007199254740993123456789 }"),
		Runs:          map[string]seedhost.Run{"source-run": {Invocation: inv, Effective: b, ConfigReceipt: "config-prefix", Phase: "confirmed", Config: json.RawMessage(`{"onlyonce":true}`), Result: json.RawMessage(`{"effects":2}`)}},
		Effects:       map[string]seedhost.Effect{"native-effect": {ID: "native-effect", RunID: inv.RunID, Binding: b, Authority: b, ConfigReceipt: "config-prefix", Kind: "native_write", Phase: "confirmed", Operation: "qb.torrents_delete", DownloaderID: "downloader-1", DownloaderRevision: 8, DownloaderSHA: "private-config-hash", NativeSnapshot: json.RawMessage(`{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","token":"synthetic-secret-value","large":9007199254740993123456789}`), Args: json.RawMessage(`{"delete_files":false}`), Result: json.RawMessage(`{"protocol_acknowledged":true,"observed":false}`)}},
		Triggers:      map[string]seedhost.Trigger{"source-event": {ID: "source-event", Kind: "service", Binding: b, Due: time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC), Phase: "queued", QueuedRunID: queued.RunID, Queued: &queued, OccurrenceID: "real-occurrence"}},
		Notices:       map[string]seedhost.Notice{"accepted-notice": {ID: "accepted-notice", RunID: inv.RunID, State: "pending", Payload: json.RawMessage(`{"title":"开始","text":"synthetic-secret-value"}`)}},
		ConfigHistory: []seedhost.ConfigWrite{{RunID: inv.RunID, Before: json.RawMessage(`false`), After: json.RawMessage(`{"onlyonce":false}`)}},
	}
}
func TestPluginCleanInvalidSeedJournalAllFieldsDeltaAndConfidentiality(t *testing.T) {
	s, _, j := cleanSeedJournalFixture(t)
	ctx := context.Background()
	want := cleanSeedJournalState()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = j.updateTx(ctx, tx, func(state *seedhost.State, meta *pluginCleanInvalidSeedMeta) error {
		*state = want
		meta.Transition = &pluginCleanInvalidSeedTransition{Version: 1, ID: "transition-1", Kind: "init", Phase: "published", RunID: "source-run", From: seedhost.Binding{InstanceID: j.instance, Generation: 2, Revision: 2}, To: want.Binding, ConfigSHA: "config-hash"}
		meta.Snapshot = &pluginCleanInvalidSeedSnapshot{Binding: want.Binding, ConfigSHA: "config-hash", Roots: []seedhost.Root{}, Downloaders: []pluginCleanInvalidSeedDownloaderRef{{ID: "downloader-1", Name: "q", Kind: "qbittorrent", Revision: 8, ConfigSHA: "private-config-hash"}}}
		meta.AppliedRun = "source-run"
		meta.AppliedBinding = want.Binding
		return nil
	})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := j.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Config, want.Config) || got.Triggers["source-event"].OccurrenceID != "real-occurrence" || got.Runs["source-run"].ConfigReceipt != "config-prefix" || len(got.ConfigHistory) != 1 || string(got.ConfigHistory[0].Before) != "false" || !bytes.Contains(got.Effects["native-effect"].NativeSnapshot, []byte("9007199254740993123456789")) {
		t.Fatal("journal lost source/effective/opaque fields")
	}
	token := j.token("effects", "native-effect", -1)
	var before, after []byte
	if err = s.DB.QueryRow(`SELECT sealed FROM plugin_clean_seed_rows WHERE collection='effects' AND token=?`, token).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, []byte("synthetic-secret-value")) || bytes.Contains(before, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")) {
		t.Fatal("private source snapshot was stored plaintext")
	}
	if err = j.Update(ctx, func(_ *sql.Tx, state *seedhost.State) error {
		r := state.Runs["source-run"]
		r.ErrorCode = "diagnostic"
		state.Runs["source-run"] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(`SELECT sealed FROM plugin_clean_seed_rows WHERE collection='effects' AND token=?`, token).Scan(&after); err != nil || !bytes.Equal(before, after) {
		t.Fatal("unchanged effect was rewritten", err)
	}
	tx, err = s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, meta, err := j.read(ctx, tx)
	tx.Rollback()
	if err != nil || meta.Transition == nil || meta.Snapshot == nil || meta.AppliedBinding != want.Binding || len(meta.Snapshot.Downloaders) != 1 {
		t.Fatal("App lifecycle metadata lost", err)
	}
}
func TestPluginCleanInvalidSeedJournalRejectsOmissionTamperAndNamespace(t *testing.T) {
	for _, mutation := range []string{"delete", "ordinal", "scope"} {
		t.Run(mutation, func(t *testing.T) {
			s, _, j := cleanSeedJournalFixture(t)
			ctx := context.Background()
			if err := j.Update(ctx, func(_ *sql.Tx, state *seedhost.State) error { *state = cleanSeedJournalState(); return nil }); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "delete":
				_, _ = s.DB.Exec(`DELETE FROM plugin_clean_seed_rows WHERE collection='effects'`)
			case "ordinal":
				_, _ = s.DB.Exec(`UPDATE plugin_clean_seed_rows SET ordinal=1 WHERE collection='config_history'`)
			case "scope":
				_, _ = s.DB.Exec(`UPDATE plugin_clean_seed_rows SET generation=3`)
			}
			if _, err := j.Read(ctx); err == nil {
				t.Fatal("unauthenticated journal was accepted", mutation)
			}
		})
	}
}
func TestPluginCleanInvalidSeedJournalSameTransactionConfigAndTransitionRollback(t *testing.T) {
	s, p, j := cleanSeedJournalFixture(t)
	ctx := context.Background()
	installStoredPlugin(t, p, j.instance)
	if _, err := p.SaveConfig(ctx, j.instance, 0, map[string]any{"onlyonce": true}); err != nil {
		t.Fatal(err)
	}
	if err := j.Update(ctx, func(_ *sql.Tx, state *seedhost.State) error {
		state.Binding = seedhost.Binding{InstanceID: j.instance, Generation: 2, Revision: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER clean_journal_fail BEFORE UPDATE ON plugin_clean_seed_meta BEGIN SELECT RAISE(ABORT,'synthetic late publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.saveValueTx(ctx, tx, j.instance, "config", "main", 1, json.RawMessage(`{"onlyonce":false}`))
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	err = j.updateTx(ctx, tx, func(state *seedhost.State, meta *pluginCleanInvalidSeedMeta) error {
		state.Binding.Revision = 2
		meta.Transition = &pluginCleanInvalidSeedTransition{Version: 1, ID: "atomic-publish", Kind: "init", Phase: "published", From: seedhost.Binding{InstanceID: j.instance, Generation: 2, Revision: 1}, To: state.Binding, RunID: "target-run"}
		return nil
	})
	if err == nil {
		tx.Rollback()
		t.Fatal("late SQL failure ignored")
	}
	tx.Rollback()
	config, err := p.Config(ctx, j.instance)
	if err != nil || config.Revision != 1 || !bytes.Contains(config.Value, []byte("true")) {
		t.Fatal("public config escaped rollback", config.Revision, err)
	}
	state, err := j.Read(ctx)
	if err != nil || state.Binding.Revision != 1 {
		t.Fatal("private transition escaped rollback", err)
	}
}
func TestPluginCleanInvalidSeedJournalRetirementScopeAndStringValidation(t *testing.T) {
	_, _, j := cleanSeedJournalFixture(t)
	ctx := context.Background()
	tx, err := j.p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = j.updateTx(ctx, tx, func(state *seedhost.State, meta *pluginCleanInvalidSeedMeta) error {
		*state = cleanSeedJournalState()
		meta.Transition = &pluginCleanInvalidSeedTransition{Version: 1, ID: "retire", Kind: "uninstall", Phase: "published", From: state.Binding, To: seedhost.Binding{InstanceID: j.instance, Generation: 3, Revision: 0}}
		return nil
	})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	err = j.Update(ctx, func(_ *sql.Tx, state *seedhost.State) error {
		r := state.Runs["source-run"]
		r.ErrorCode = string([]byte{0xff})
		state.Runs["source-run"] = r
		return nil
	})
	if !errors.Is(err, seedhost.ErrUnsafe) {
		t.Fatal("invalid UTF8 was silently changed", err)
	}
	if err = j.Update(ctx, func(_ *sql.Tx, state *seedhost.State) error {
		e := state.Effects["native-effect"]
		e.Authority.Generation = 3
		state.Effects[e.ID] = e
		return nil
	}); !errors.Is(err, seedhost.ErrUnsafe) {
		t.Fatal("new generation effect admitted into old journal", err)
	}
}
