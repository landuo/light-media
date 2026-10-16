package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

func TestPluginRemoveLinkJournalTransitionBackwardMACAndNamespace(t *testing.T) {
	store, p, j := removeLinkJournalFixture(t, "cleanup.transition-codec", 5)
	ctx := context.Background()
	from := cleanuphost.Binding{InstanceID: j.instance, Generation: j.generation, Revision: 1}
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error { s.Binding = from; return nil }); err != nil {
		t.Fatal(err)
	}
	var sealed, digest []byte
	if err := store.DB.QueryRow(`SELECT sealed,digest FROM plugin_cleanup_journal_meta WHERE instance_id=? AND generation=?`, j.instance, j.generation).Scan(&sealed, &digest); err != nil {
		t.Fatal(err)
	}
	raw, err := p.unseal("cleanup_journal", j.instance, j.aad("meta", "main"), sealed)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]json.RawMessage
	if json.Unmarshal(raw, &legacy) != nil || legacy["transition"] != nil {
		t.Fatal("nil transition changes the legacy authenticated representation", string(raw))
	}
	legacyWire, err := cleanupJournalJSON(legacy)
	if err != nil || !hmac.Equal(digest, j.mac("value:meta", legacyWire)) {
		t.Fatal("legacy field set no longer has the same MAC", err)
	}
	sealed, err = p.seal("cleanup_journal", j.instance, j.aad("meta", "main"), legacyWire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.Exec(`UPDATE plugin_cleanup_journal_meta SET sealed=? WHERE instance_id=? AND generation=?`, sealed, j.instance, j.generation); err != nil {
		t.Fatal(err)
	}
	if got := removeLinkJournalRead(t, j); got.Binding != from || got.Transition != nil {
		t.Fatal("previous header cannot be read without re-signing its digest", got)
	}
	for _, kind := range []string{"init", "reload", "reset", "uninstall", "shutdown"} {
		to := from
		if kind == "reset" || kind == "uninstall" {
			to.Generation++
			to.Revision = 0
		}
		want := &cleanuphost.Transition{ID: "transition:" + kind, Kind: kind, From: from, To: to, RunID: "run:" + kind, Phase: "applied", WasRunning: true}
		if err := j.Update(ctx, func(s *cleanuphost.Durable) error { s.Transition = want; return nil }); err != nil {
			t.Fatal(kind, err)
		}
		got := removeLinkJournalRead(t, j)
		if !reflect.DeepEqual(got.Transition, want) || got.Binding.Generation != from.Generation {
			t.Fatal("retirement moved the old journal to a new namespace", kind, got)
		}
		got.Transition.ID = "mutated"
		if !reflect.DeepEqual(removeLinkJournalRead(t, j).Transition, want) {
			t.Fatal("read returned a shared transition pointer")
		}
	}
	before := removeLinkJournalSealed(t, store, j.instance, j.generation)
	for _, field := range []string{"empty-from", "foreign-from", "foreign-to", "init-next-generation", "retire-same-generation", "invalid-utf8"} {
		err := j.Update(ctx, func(s *cleanuphost.Durable) error {
			s.Transition = &cleanuphost.Transition{ID: "invalid-test", Kind: "init", From: from, To: from}
			switch field {
			case "empty-from":
				s.Transition.From = cleanuphost.Binding{}
			case "foreign-from":
				s.Transition.From.InstanceID = "other"
			case "foreign-to":
				s.Transition.To.InstanceID = "other"
			case "init-next-generation":
				s.Transition.To.Generation++
			case "retire-same-generation":
				s.Transition.Kind = "uninstall"
			case "invalid-utf8":
				s.Transition.ID = string([]byte{0xff})
			}
			return nil
		})
		if err == nil || !reflect.DeepEqual(before, removeLinkJournalSealed(t, store, j.instance, j.generation)) {
			t.Fatal("invalid transition changed the namespace or persisted partial state", field, err)
		}
	}
}

func removeLinkTransitionInstallation(t *testing.T, p *PluginStore, instance string, config any) cleanuphost.Binding {
	t.Helper()
	manifest := removelink.Manifest()
	raw, _ := json.Marshal(manifest)
	run, err := p.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: instance, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw}, config)
	if err != nil {
		t.Fatal(err)
	}
	return cleanuphost.Binding{InstanceID: instance, Generation: run.Generation, Revision: run.ConfigRevision}
}

func publishRemoveLinkTransitionSQL(ctx context.Context, p *PluginStore, j *pluginRemoveLinkJournal, ticket cleanuphost.TransitionTicket, config json.RawMessage) (PluginRun, error) {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginRun{}, err
	}
	defer tx.Rollback()
	stored, err := p.saveValueTx(ctx, tx, ticket.From.InstanceID, "config", "main", ticket.From.Revision, config)
	if err != nil {
		return PluginRun{}, err
	}
	run, err := p.enqueueRunTx(ctx, tx, ticket.From.InstanceID, "init", nil)
	if err != nil {
		return PluginRun{}, err
	}
	to := ticket.From
	to.Revision = stored.Revision
	if err = j.updateTx(ctx, tx, func(s *cleanuphost.Durable) error { return cleanuphost.PublishTransition(s, ticket, to, run.ID) }); err != nil {
		return PluginRun{}, err
	}
	return run, tx.Commit()
}

func TestPluginRemoveLinkJournalTransitionSharesRealConfigAndRunTransaction(t *testing.T) {
	store, p, j := removeLinkJournalFixture(t, "cleanup.atomic-transition", 1)
	ctx := context.Background()
	from := removeLinkTransitionInstallation(t, p, j.instance, map[string]any{"enabled": false})
	ticket := cleanuphost.TransitionTicket{ID: "synthetic-config-save", Kind: "init", From: from}
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error {
		s.Binding = from
		s.Transition = &cleanuphost.Transition{ID: ticket.ID, Kind: ticket.Kind, From: from, Phase: "prepared"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var initialRuns int
	if err := store.DB.QueryRow(`SELECT count(*) FROM plugin_runs`).Scan(&initialRuns); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`CREATE TRIGGER synthetic_transition_failure BEFORE UPDATE ON plugin_cleanup_journal_meta BEGIN SELECT RAISE(ABORT,'synthetic journal failure after config and run writes'); END`); err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"enabled":true,"unknown":9007199254740993123456789}`)
	if _, err := publishRemoveLinkTransitionSQL(ctx, p, j, ticket, config); err == nil {
		t.Fatal("journal failure did not fail config publication")
	}
	stored, err := p.Config(ctx, j.instance)
	var runs int
	store.DB.QueryRow(`SELECT count(*) FROM plugin_runs`).Scan(&runs)
	if err != nil || stored.Revision != from.Revision || runs != initialRuns || removeLinkJournalRead(t, j).Transition.Phase != "prepared" {
		t.Fatal("journal failure left a partial config/run publication", err, stored.Revision, runs)
	}
	if _, err = store.DB.Exec(`DROP TRIGGER synthetic_transition_failure`); err != nil {
		t.Fatal(err)
	}
	run, err := publishRemoveLinkTransitionSQL(ctx, p, j, ticket, config)
	if err != nil {
		t.Fatal(err)
	}
	got := removeLinkJournalRead(t, j)
	stored, err = p.Config(ctx, j.instance)
	if err != nil || !bytes.Equal(stored.Value, config) || stored.Revision != from.Revision+1 || got.Transition.RunID != run.ID || got.Transition.To.Revision != stored.Revision || got.Transition.Phase != "published" || got.Binding != from {
		t.Fatal("real config/run/journal did not commit together", stored, got.Transition, run, err)
	}
	before := removeLinkJournalSealed(t, store, j.instance, j.generation)
	if _, err := publishRemoveLinkTransitionSQL(ctx, p, j, ticket, json.RawMessage(`{}`)); pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT" {
		t.Fatal("stale config save unexpectedly replaced its transition", err)
	}
	if !reflect.DeepEqual(before, removeLinkJournalSealed(t, store, j.instance, j.generation)) {
		t.Fatal("stale CAS changed journal")
	}
}

type removeLinkSQLTestWatcher struct {
	events chan cleanuphost.Event
	errors chan error
	once   sync.Once
}

func (w *removeLinkSQLTestWatcher) Events() <-chan cleanuphost.Event { return w.events }
func (w *removeLinkSQLTestWatcher) Errors() <-chan error             { return w.errors }
func (w *removeLinkSQLTestWatcher) Close() error {
	w.once.Do(func() { close(w.events); close(w.errors) })
	return nil
}

type removeLinkSQLNoCoreEffects struct{}

func (removeLinkSQLNoCoreEffects) Commit(context.Context, cleanuphost.Effect) (bool, error) {
	return false, errors.New("core effects are disabled in this owned filesystem/SQL fixture")
}

func TestPluginRemoveLinkJournalRealOwnerAppliesOnlyPublishedConfig(t *testing.T) {
	_, p, j := removeLinkJournalFixture(t, "cleanup.real-owner", 1)
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, media := filepath.Join(base, "source"), filepath.Join(base, "media")
	for _, dir := range []string{source, media} {
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	origin, linked := filepath.Join(source, "owned.mkv"), filepath.Join(media, "owned.mkv")
	if err = os.WriteFile(origin, []byte("synthetic owned video"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(origin, linked); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"enabled": true, "monitor_dirs": source + "\n" + media, "delayed_deletion": true, "delay_seconds": 60})
	from := removeLinkTransitionInstallation(t, p, j.instance, json.RawMessage(config))
	options := cleanuphost.Options{Binding: from, Roots: []cleanuphost.Root{{ID: "source", Path: source, Role: "source"}, {ID: "media", Path: media, Role: "media"}}, Journal: j, Effects: removeLinkSQLNoCoreEffects{}, Guard: func(ctx context.Context, binding cleanuphost.Binding, _ string) (func(), error) {
		stored, err := p.Config(ctx, j.instance)
		if err != nil || binding.InstanceID != j.instance || binding.Generation != j.generation || binding.Revision != stored.Revision {
			return nil, cleanuphost.ErrUnsafe
		}
		return func() {}, nil
	}, Watch: func(context.Context, []removelink.Watch, func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
		return &removeLinkSQLTestWatcher{events: make(chan cleanuphost.Event, 32), errors: make(chan error, 1)}, nil
	}}
	host, err := cleanuphost.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if err = host.Configure(ctx, from, config); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(origin); err != nil {
		t.Fatal(err)
	}
	if err = host.Observe(ctx, cleanuphost.Event{ID: "owned-source-deleted", Kind: "deleted", Path: origin, Mode: "hardlink"}); err != nil {
		t.Fatal(err)
	}
	if got := removeLinkJournalRead(t, j); len(got.Machine.Queue) != 1 {
		t.Fatal("source did not retain its pending cleanup", len(got.Machine.Queue))
	}
	ticket, err := host.BeginTransition(ctx, from, "owned-config-save", "init")
	if err != nil {
		t.Fatal(err)
	}
	var changed map[string]any
	json.Unmarshal(config, &changed)
	changed["delayed_deletion"] = false
	next, _ := json.Marshal(changed)
	run, err := publishRemoveLinkTransitionSQL(ctx, p, j, ticket, next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(linked); err != nil {
		t.Fatal("SQL publication itself performed a file deletion", err)
	}
	if err = host.ApplyTransition(ctx, ticket, run.ID, next); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(linked); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("new config did not apply the old queue through the real host", err)
	}
	got := removeLinkJournalRead(t, j)
	receipt := got.Effects["lifecycle:"+run.ID]
	if got.Transition.Phase != "applied" || got.Binding.Revision != run.ConfigRevision || len(got.Machine.Queue) != 0 || receipt.State != "confirmed" || !receipt.Result {
		t.Fatal("filesystem and final lifecycle checkpoint did not preserve their receipts", got.Transition, receipt)
	}
	if err = host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	options.Binding = got.Binding
	host, err = cleanuphost.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = host.Recover(ctx, options.Binding); err != nil {
		t.Fatal("SQL checkpoint cannot recover a cleanly stopped owner", err)
	}
}

func TestPluginRemoveLinkJournalResetRevisionZeroPublishesFirstConfigAtomically(t *testing.T) {
	_, p, first := removeLinkJournalFixture(t, "cleanup.reset-first-save", 1)
	ctx := context.Background()
	removeLinkTransitionInstallation(t, p, first.instance, map[string]any{"enabled": false})
	if err := p.Reset(ctx, first.instance); err != nil {
		t.Fatal(err)
	}
	installation, err := p.Installation(ctx, first.instance)
	if err != nil {
		t.Fatal(err)
	}
	if installation.Generation != 2 || installation.DesiredEnabled {
		t.Fatal("reset scope", installation.Generation, installation.DesiredEnabled)
	}
	j, err := newPluginRemoveLinkJournal(ctx, p, first.instance, installation.Generation)
	if err != nil {
		t.Fatal(err)
	}
	from := cleanuphost.Binding{InstanceID: first.instance, Generation: 2, Revision: 0}
	ticket := cleanuphost.TransitionTicket{ID: "first-save-after-reset", Kind: "init", From: from}
	if err = j.Update(ctx, func(s *cleanuphost.Durable) error {
		s.Binding = from
		s.Transition = &cleanuphost.Transition{ID: ticket.ID, Kind: ticket.Kind, From: from, Phase: "prepared"}
		return nil
	}); err != nil {
		t.Fatal("revision-zero transition rejected", err)
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	raw := json.RawMessage(`{"enabled":false,"monitor_dirs":""}`)
	config, err := p.saveValueTx(ctx, tx, first.instance, "config", "main", 0, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET desired_enabled=1 WHERE instance_id=?`, first.instance); err != nil {
		t.Fatal(err)
	}
	run, err := p.enqueueRunTx(ctx, tx, first.instance, "init", nil)
	if err != nil {
		t.Fatal(err)
	}
	to := from
	to.Revision = config.Revision
	if err = j.updateTx(ctx, tx, func(s *cleanuphost.Durable) error { return cleanuphost.PublishTransition(s, ticket, to, run.ID) }); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	saved, err := p.Config(ctx, first.instance)
	if err != nil || saved.Revision != 1 || !bytes.Equal(saved.Value, raw) {
		t.Fatal(saved.Revision, err)
	}
	state := removeLinkJournalRead(t, j)
	if state.Binding != from || state.Transition.Phase != "published" || state.Transition.To != to || state.Transition.RunID != run.ID {
		t.Fatal("first config/run/journal not published together", state.Transition)
	}
	before := removeLinkJournalSealed(t, p.store, j.instance, j.generation)
	if err = j.Update(ctx, func(s *cleanuphost.Durable) error { s.Transition.From.Revision = -1; return nil }); err == nil {
		t.Fatal("negative revision accepted")
	}
	if !reflect.DeepEqual(before, removeLinkJournalSealed(t, p.store, j.instance, j.generation)) {
		t.Fatal("invalid negative revision changed journal")
	}
}
