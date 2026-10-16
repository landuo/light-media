package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

func removeLinkJournalFixture(t *testing.T, instance string, generation int64) (*Store, *PluginStore, *pluginRemoveLinkJournal) {
	t.Helper()
	store, p := pluginStoreFixture(t)
	j, err := newPluginRemoveLinkJournal(context.Background(), p, instance, generation)
	if err != nil {
		t.Fatal(err)
	}
	return store, p, j
}
func removeLinkJournalSyntheticState(instance string, generation int64) cleanuphost.Durable {
	at := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
	binding := cleanuphost.Binding{InstanceID: instance, Generation: generation, Revision: 19}
	stamp := cleanuphost.Stamp{Dev: math.MaxUint64, Inode: math.MaxUint64 - 1, Size: math.MaxInt64, ModNS: math.MinInt64 + 1, Mode: math.MaxUint32}
	pin := cleanuphost.Pin{RootID: "synthetic-root", ParentPath: "/synthetic/private-parent", Parent: stamp, File: stamp, Epoch: math.MaxUint64}
	event := cleanuphost.Event{ID: "synthetic-event", Kind: "moved", Path: "/synthetic/private-source", Dest: "/synthetic/private-dest", Mode: "hardlink", Directory: true, At: at}
	state := cleanuphost.Durable{Binding: binding, Roots: []cleanuphost.RootStamp{{Root: cleanuphost.Root{ID: "z-first", Path: "/synthetic/private-root-z", Role: "source"}, File: stamp}, {Root: cleanuphost.Root{ID: "a-last", Path: "/synthetic/private-root-a", Role: "media"}, File: stamp}}, Config: json.RawMessage(" {\n \"unknown\": {\"big\":184467440737095516151234567890,\"zero\":0.000000000000000001,\"null\":null,\"bool\":false,\"list\":[]} } "), Machine: cleanuphost.MachineState{State: []removelink.Tracked{{Path: "/synthetic/z-first", Identity: removelink.Identity{Dev: math.MaxUint64, Inode: math.MaxUint64 - 3, Added: at}}, {Path: "/synthetic/a-last", Identity: removelink.Identity{Dev: 5, Inode: 4, Added: at.Add(-time.Hour)}}}, Queue: []*removelink.Task{{Path: "/synthetic/queued-z", Identity: removelink.Identity{Dev: math.MaxUint64, Inode: 1, Added: at}, At: at, Processed: true}, nil, {Path: "/synthetic/queued-a", At: at.Add(time.Second)}}, TimerDue: &at, Watches: []removelink.Watch{{Path: "/synthetic/watch-z", Mode: "strm"}, {Path: "/synthetic/watch-a", Mode: "hardlink"}}}, Pins: map[string]cleanuphost.Pin{"/synthetic/private-map-key-'; DROP TABLE admin; --": pin, "私有键\x00original": pin}, Frozen: map[string]cleanuphost.Pin{"synthetic-frozen-private": pin}, Serial: math.MaxUint64, EventSerial: math.MaxUint64 - 2, Events: map[string]cleanuphost.EventReceipt{"synthetic-event-private-key": {Event: event, Binding: binding, Sequence: math.MaxUint64, State: "confirmed", Error: "synthetic-private-event-error"}}, Effects: map[string]cleanuphost.Effect{"synthetic-effect-private-key": {ID: "synthetic-effect", Scope: "synthetic-private-scope", Kind: "notification", Path: "/synthetic/effect", Role: "media", Binding: binding, Expected: pin, Stage: "after", State: "confirmed", Error: "synthetic-private-error", Payload: json.RawMessage(" { \"integer\": 9007199254740993123456789, \"exp\":1e-100, \"nil\":null } "), Result: true}}, Active: &event, Blocked: "synthetic-private-blocked"}
	state.Transition = &cleanuphost.Transition{ID: "synthetic-transition", Kind: "init", Phase: "published", From: binding, To: cleanuphost.Binding{InstanceID: instance, Generation: generation, Revision: 20}, RunID: "synthetic-run", WasRunning: true}
	return state
}
func removeLinkJournalRead(t *testing.T, j *pluginRemoveLinkJournal) cleanuphost.Durable {
	t.Helper()
	s, err := j.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func removeLinkJournalSealed(t *testing.T, store *Store, instance string, generation int64) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	var meta []byte
	if err := store.DB.QueryRow(`SELECT sealed FROM plugin_cleanup_journal_meta WHERE instance_id=? AND generation=?`, instance, generation).Scan(&meta); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if meta != nil {
		out["meta"] = meta
	}
	rows, err := store.DB.Query(`SELECT collection,row_key,sealed FROM plugin_cleanup_journal_rows WHERE instance_id=? AND generation=? ORDER BY collection,row_key`, instance, generation)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var collection, key string
		var sealed []byte
		if err = rows.Scan(&collection, &key, &sealed); err != nil {
			t.Fatal(err)
		}
		out[collection+":"+key] = append([]byte(nil), sealed...)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPluginRemoveLinkJournalRestartAllFieldsAndIsolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	open := true
	t.Cleanup(func() {
		if open {
			_ = store.Close()
		}
	})
	p, err := NewPluginStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	j, err := newPluginRemoveLinkJournal(ctx, p, "cleanup.instance", 7)
	if err != nil {
		t.Fatal(err)
	}
	state := removeLinkJournalSyntheticState("cleanup.instance", 7)
	if err = j.Update(ctx, func(value *cleanuphost.Durable) error { *value = state; return nil }); err != nil {
		t.Fatal(err)
	}
	got := removeLinkJournalRead(t, j)
	if !reflect.DeepEqual(got, state) {
		t.Fatal("all Durable fields/order/numbers/raw bytes changed", got)
	}
	var config map[string]any
	decoder := json.NewDecoder(bytes.NewReader(got.Config))
	decoder.UseNumber()
	if err = decoder.Decode(&config); err != nil {
		t.Fatal(err)
	}
	if config["unknown"].(map[string]any)["big"] != json.Number("184467440737095516151234567890") {
		t.Fatal("configuration integer lost")
	}
	got.Config[1] = '!'
	got.Machine.Queue[0].Path = "mutated"
	got.Machine.State[0].Path = "mutated"
	got.Machine.TimerDue.Add(time.Hour)
	*got.Machine.TimerDue = time.Time{}
	got.Roots[0].Root.Path = "mutated"
	got.Pins["mutated"] = cleanuphost.Pin{}
	delete(got.Events, "synthetic-event-private-key")
	effect := got.Effects["synthetic-effect-private-key"]
	effect.Payload[1] = '!'
	got.Effects["synthetic-effect-private-key"] = effect
	got.Active.Path = "mutated"
	got.Transition.To.Revision = 900
	got.Transition.ID = "mutated"
	if !reflect.DeepEqual(removeLinkJournalRead(t, j), state) {
		t.Fatal("Read leaked mutable references")
	}
	var retained *cleanuphost.Durable
	if err = j.Update(ctx, func(s *cleanuphost.Durable) error { retained = s; return nil }); err != nil {
		t.Fatal(err)
	}
	retained.Machine.Queue[0].Path = "later callback alias mutation"
	retained.Config[0] = '!'
	if !reflect.DeepEqual(removeLinkJournalRead(t, j), state) {
		t.Fatal("callback result retained storage references")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	open = false
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	open = true
	p, err = NewPluginStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	j, err = newPluginRemoveLinkJournal(ctx, p, "cleanup.instance", 7)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removeLinkJournalRead(t, j), state) {
		t.Fatal("restart changed complete journal")
	}
	next, err := newPluginRemoveLinkJournal(ctx, p, "cleanup.instance", 8)
	if err != nil {
		t.Fatal(err)
	}
	empty := removeLinkJournalRead(t, next)
	if empty.Binding != (cleanuphost.Binding{}) || len(empty.Machine.Queue) != 0 || len(empty.Effects) != 0 || len(empty.Events) != 0 || empty.Active != nil || empty.Blocked != "" {
		t.Fatal("new installation recovered old generation work")
	}
	if empty.Roots == nil || empty.Machine.State == nil || empty.Machine.Queue == nil || empty.Machine.Watches == nil || empty.Pins == nil || empty.Frozen == nil || empty.Events == nil || empty.Effects == nil {
		t.Fatal("empty collections not initialized")
	}
	if err = next.Update(ctx, func(s *cleanuphost.Durable) error {
		s.Binding = cleanuphost.Binding{InstanceID: "cleanup.instance", Generation: 8, Revision: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removeLinkJournalRead(t, j), state) {
		t.Fatal("new generation overwrote old receipt namespace")
	}
	for _, name := range []string{"light-media.db", "light-media.db-wal"} {
		raw, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil && !os.IsNotExist(e) {
			t.Fatal(e)
		}
		for _, secret := range []string{"private-map-key", "DROP TABLE admin", "私有键", "synthetic-private-parent", "synthetic-private-blocked", "9007199254740993123456789", "184467440737095516151234567890"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("journal leaked plaintext to database", name, secret)
			}
		}
	}
}
func TestPluginRemoveLinkJournalOnlyChangedRowsAreReencrypted(t *testing.T) {
	store, _, j := removeLinkJournalFixture(t, "cleanup.instance", 1)
	ctx := context.Background()
	state := removeLinkJournalSyntheticState(j.instance, j.generation)
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error { *s = state; return nil }); err != nil {
		t.Fatal(err)
	}
	before := removeLinkJournalSealed(t, store, j.instance, j.generation)
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, removeLinkJournalSealed(t, store, j.instance, j.generation)) {
		t.Fatal("no-op rewrote encrypted rows")
	}
	key := "私有键\x00original"
	token := j.rowToken("pins", &key, -1)
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error { pin := s.Pins[key]; pin.Epoch--; s.Pins[key] = pin; return nil }); err != nil {
		t.Fatal(err)
	}
	after := removeLinkJournalSealed(t, store, j.instance, j.generation)
	changed := []string{}
	for name, value := range before {
		if !bytes.Equal(value, after[name]) {
			changed = append(changed, name)
		}
	}
	if len(changed) != 2 || bytes.Equal(before["meta"], after["meta"]) || bytes.Equal(before["pins:"+token], after["pins:"+token]) {
		t.Fatal("single map item changed other sealed rows", changed)
	}
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error {
		s.Machine.State = []removelink.Tracked{s.Machine.State[1]}
		s.Machine.Queue = nil
		delete(s.Frozen, "synthetic-frozen-private")
		s.Active = nil
		s.Blocked = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result := removeLinkJournalRead(t, j)
	if len(result.Machine.State) != 1 || result.Machine.State[0].Path != "/synthetic/a-last" || result.Machine.Queue == nil || len(result.Machine.Queue) != 0 || len(result.Frozen) != 0 || result.Active != nil || result.Blocked != "" {
		t.Fatal("list removal/compaction/clear changed")
	}
}
func TestPluginRemoveLinkJournalCallbackCancellationAndSQLRollback(t *testing.T) {
	store, _, j := removeLinkJournalFixture(t, "cleanup.instance", 1)
	ctx := context.Background()
	state := removeLinkJournalSyntheticState(j.instance, j.generation)
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error { *s = state; return nil }); err != nil {
		t.Fatal(err)
	}
	before := removeLinkJournalSealed(t, store, j.instance, j.generation)
	sentinel := errors.New("synthetic callback failure")
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error {
		s.Binding.Revision++
		s.Machine.State = nil
		s.Effects = nil
		s.Config[0] = '!'
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, removeLinkJournalSealed(t, store, j.instance, j.generation)) {
		t.Fatal("callback failure committed mutation")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	called := false
	if err := j.Update(canceled, func(s *cleanuphost.Durable) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatal("canceled update ran callback", err, called)
	}
	if _, err := j.Read(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	during, stop := context.WithCancel(ctx)
	if err := j.Update(during, func(s *cleanuphost.Durable) error { s.Blocked = "should not commit"; stop(); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, removeLinkJournalSealed(t, store, j.instance, j.generation)) {
		t.Fatal("canceled callback committed mutation")
	}
	if _, err := store.DB.Exec(`CREATE TRIGGER synthetic_cleanup_row_abort BEFORE INSERT ON plugin_cleanup_journal_rows WHEN NEW.collection='effects' BEGIN SELECT RAISE(ABORT,'synthetic journal failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error {
		s.Serial--
		s.Machine.State[0].Path = "early row changed"
		value := s.Effects["synthetic-effect-private-key"]
		value.Result = false
		s.Effects["synthetic-effect-private-key"] = value
		return nil
	}); err == nil {
		t.Fatal("SQL failure reported success")
	}
	if !reflect.DeepEqual(before, removeLinkJournalSealed(t, store, j.instance, j.generation)) || !reflect.DeepEqual(removeLinkJournalRead(t, j), state) {
		t.Fatal("SQL failure partially committed machine/event/effect state")
	}
	if _, err := store.DB.Exec(`DROP TRIGGER synthetic_cleanup_row_abort; CREATE TRIGGER synthetic_cleanup_meta_abort BEFORE UPDATE ON plugin_cleanup_journal_meta BEGIN SELECT RAISE(ABORT,'synthetic meta failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error { s.Machine.State = nil; return nil }); err == nil {
		t.Fatal("meta failure reported success")
	}
	if !reflect.DeepEqual(before, removeLinkJournalSealed(t, store, j.instance, j.generation)) {
		t.Fatal("metadata SQL failure did not roll back collection deletes")
	}
}
func TestPluginRemoveLinkJournalScopeAndRawPresence(t *testing.T) {
	store, p, j := removeLinkJournalFixture(t, "cleanup.instance", 1)
	ctx := context.Background()
	for _, invalid := range []struct {
		ID         string
		Generation int64
	}{{"'; DROP TABLE admin;--", 1}, {"cleanup.instance", 0}, {"", 1}} {
		if _, err := newPluginRemoveLinkJournal(ctx, p, invalid.ID, invalid.Generation); err == nil {
			t.Fatal("invalid namespace accepted")
		}
	}
	var adminTable int
	if err := store.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='admin'`).Scan(&adminTable); err != nil || adminTable != 1 {
		t.Fatal("namespace entered SQL text", err)
	}
	for _, field := range []string{"binding", "event", "effect"} {
		if err := j.Update(ctx, func(s *cleanuphost.Durable) error {
			wrong := cleanuphost.Binding{InstanceID: "other-instance", Generation: 1, Revision: 1}
			switch field {
			case "binding":
				s.Binding = wrong
			case "event":
				s.Events["x"] = cleanuphost.EventReceipt{Binding: wrong}
			case "effect":
				s.Effects["x"] = cleanuphost.Effect{Binding: wrong}
			}
			return nil
		}); pluginErrorCode(err) != "CLEANUP_JOURNAL_SCOPE_MISMATCH" {
			t.Fatal(field, err)
		}
	}
	for _, config := range []json.RawMessage{nil, {}, json.RawMessage(`null`), json.RawMessage(` {"unknown":9007199254740993123456789} `)} {
		payload := append(json.RawMessage{}, config...)
		if config == nil {
			payload = nil
		}
		if err := j.Update(ctx, func(s *cleanuphost.Durable) error {
			s.Config = config
			s.Effects["raw-presence"] = cleanuphost.Effect{Payload: payload}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		s := removeLinkJournalRead(t, j)
		if !reflect.DeepEqual(s.Config, config) || !reflect.DeepEqual(s.Effects["raw-presence"].Payload, payload) {
			t.Fatal("RawMessage nil/empty/lexical presence changed", string(config))
		}
	}
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error { s.Config = json.RawMessage("{"); return nil }); pluginErrorCode(err) != "CLEANUP_JOURNAL_CORRUPT" {
		t.Fatal("invalid raw Config accepted", err)
	}
	if err := j.Update(ctx, func(s *cleanuphost.Durable) error {
		s.Effects["x"] = cleanuphost.Effect{Payload: json.RawMessage("{")}
		return nil
	}); pluginErrorCode(err) != "CLEANUP_JOURNAL_CORRUPT" {
		t.Fatal("invalid raw payload accepted", err)
	}
	before := removeLinkJournalSealed(t, store, j.instance, j.generation)
	for _, field := range []string{"path", "map-key", "event-mode"} {
		if err := j.Update(ctx, func(s *cleanuphost.Durable) error {
			switch field {
			case "path":
				s.Machine.Watches = []removelink.Watch{{Path: string([]byte{'/', 0xff})}}
			case "map-key":
				s.Pins[string([]byte{0xff})] = cleanuphost.Pin{}
			case "event-mode":
				s.Active = &cleanuphost.Event{Mode: string([]byte{0xff})}
			}
			return nil
		}); pluginErrorCode(err) != "CLEANUP_JOURNAL_CORRUPT" {
			t.Fatal("malformed UTF-8 was silently replaced", field, err)
		}
	}
	if !reflect.DeepEqual(before, removeLinkJournalSealed(t, store, j.instance, j.generation)) {
		t.Fatal("malformed UTF-8 mutation changed stored state")
	}
	if err := j.Update(ctx, nil); err == nil {
		t.Fatal("nil mutation accepted")
	}
}
func TestPluginRemoveLinkJournalLargerThanOrdinaryKV(t *testing.T) {
	_, _, j := removeLinkJournalFixture(t, "cleanup.large", 1)
	raw := json.RawMessage(`{"unknown":"` + strings.Repeat("x", pluginValueLimit+1) + `","integer":184467440737095516150000000000}`)
	if err := j.Update(context.Background(), func(s *cleanuphost.Durable) error { s.Config = raw; return nil }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(removeLinkJournalRead(t, j).Config, raw) {
		t.Fatal("cleanup journal was limited to ordinary config size or reencoded")
	}
}
func TestPluginRemoveLinkJournalRejectsTamperUnknownAndWrongMACScope(t *testing.T) {
	for _, scenario := range []string{"cipher", "digest", "ordinal", "deleted-row", "unknown-collection", "unknown-meta-field", "unknown-value-field", "key-token", "meta-version", "cross-generation-cipher", "orphan-row"} {
		t.Run(scenario, func(t *testing.T) {
			store, p, j := removeLinkJournalFixture(t, "cleanup.tamper", 1)
			ctx := context.Background()
			state := removeLinkJournalSyntheticState(j.instance, j.generation)
			if err := j.Update(ctx, func(s *cleanuphost.Durable) error { *s = state; return nil }); err != nil {
				t.Fatal(err)
			}
			var token string
			var sealed, digest []byte
			if err := store.DB.QueryRow(`SELECT row_key,sealed,digest FROM plugin_cleanup_journal_rows WHERE instance_id=? AND generation=? AND collection='pins' ORDER BY row_key LIMIT 1`, j.instance, j.generation).Scan(&token, &sealed, &digest); err != nil {
				t.Fatal(err)
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := store.DB.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "cipher":
				sealed[len(sealed)-1] ^= 1
				exec(`UPDATE plugin_cleanup_journal_rows SET sealed=? WHERE instance_id=? AND generation=1 AND collection='pins' AND row_key=?`, sealed, j.instance, token)
			case "digest":
				digest[0] ^= 1
				exec(`UPDATE plugin_cleanup_journal_rows SET digest=? WHERE instance_id=? AND generation=1 AND collection='pins' AND row_key=?`, digest, j.instance, token)
			case "ordinal":
				exec(`UPDATE plugin_cleanup_journal_rows SET ordinal=7 WHERE instance_id=? AND generation=1 AND collection='state' AND ordinal=0`, j.instance)
			case "deleted-row":
				exec(`DELETE FROM plugin_cleanup_journal_rows WHERE instance_id=? AND generation=1 AND collection='pins' AND row_key=?`, j.instance, token)
			case "unknown-collection":
				exec(`UPDATE plugin_cleanup_journal_rows SET collection='unknown' WHERE instance_id=? AND generation=1 AND collection='pins' AND row_key=?`, j.instance, token)
			case "meta-version":
				exec(`UPDATE plugin_cleanup_journal_meta SET version=2 WHERE instance_id=? AND generation=1`, j.instance)
			case "unknown-meta-field":
				var metaSealed []byte
				if err := store.DB.QueryRow(`SELECT sealed FROM plugin_cleanup_journal_meta WHERE instance_id=? AND generation=1`, j.instance).Scan(&metaSealed); err != nil {
					t.Fatal(err)
				}
				raw, err := p.unseal("cleanup_journal", j.instance, j.aad("meta", "main"), metaSealed)
				if err != nil {
					t.Fatal(err)
				}
				changed := append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"unrecognized":true}`)...)
				wire, err := cleanupJournalJSON(json.RawMessage(changed))
				if err != nil {
					t.Fatal(err)
				}
				sealed, err = p.seal("cleanup_journal", j.instance, j.aad("meta", "main"), wire)
				if err != nil {
					t.Fatal(err)
				}
				exec(`UPDATE plugin_cleanup_journal_meta SET sealed=?,digest=? WHERE instance_id=? AND generation=1`, sealed, j.mac("value:meta", wire), j.instance)
			case "unknown-value-field", "key-token":
				raw, err := p.unseal("cleanup_journal", j.instance, j.aad("pins", token), sealed)
				if err != nil {
					t.Fatal(err)
				}
				var row pluginRemoveLinkJournalRow
				if err = cleanupJournalStrict(raw, &row); err != nil {
					t.Fatal(err)
				}
				if scenario == "key-token" {
					key := "forged-original-key"
					row.Key = &key
				} else {
					row.Value = append(append([]byte(nil), row.Value[:len(row.Value)-1]...), []byte(`,"future":true}`)...)
				}
				wire, err := cleanupJournalJSON(row)
				if err != nil {
					t.Fatal(err)
				}
				sealed, err = p.seal("cleanup_journal", j.instance, j.aad("pins", token), wire)
				if err != nil {
					t.Fatal(err)
				}
				exec(`UPDATE plugin_cleanup_journal_rows SET sealed=?,digest=? WHERE instance_id=? AND generation=1 AND collection='pins' AND row_key=?`, sealed, j.mac("value:pins", wire), j.instance, token)
			case "cross-generation-cipher":
				second, err := newPluginRemoveLinkJournal(ctx, p, j.instance, 2)
				if err != nil {
					t.Fatal(err)
				}
				other := removeLinkJournalSyntheticState(j.instance, 2)
				if err = second.Update(ctx, func(s *cleanuphost.Durable) error { *s = other; return nil }); err != nil {
					t.Fatal(err)
				}
				exec(`UPDATE plugin_cleanup_journal_rows SET sealed=? WHERE instance_id=? AND generation=2 AND collection='pins'`, sealed, j.instance)
				j = second
			case "orphan-row":
				exec(`DELETE FROM plugin_cleanup_journal_meta WHERE instance_id=? AND generation=1`, j.instance)
			}
			if _, err := j.Read(ctx); err == nil {
				t.Fatal("corrupt/unknown/wrong-scope journal was accepted", scenario)
			}
			called := false
			if err := j.Update(ctx, func(s *cleanuphost.Durable) error { called = true; return nil }); err == nil || called {
				t.Fatal("corrupt storage reached mutation callback", scenario, err, called)
			}
		})
	}
}

func TestPluginRemoveLinkJournalConcurrentUpdatesDoNotLoseReceipts(t *testing.T) {
	_, p, j := removeLinkJournalFixture(t, "cleanup.concurrent", 1)
	ctx := context.Background()
	second, err := newPluginRemoveLinkJournal(ctx, p, j.instance, j.generation)
	if err != nil {
		t.Fatal(err)
	}
	errorsOut := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func(i int) {
			owner := j
			if i%2 == 1 {
				owner = second
			}
			errorsOut <- owner.Update(ctx, func(s *cleanuphost.Durable) error {
				s.Serial++
				s.Events[fmt.Sprintf("synthetic-%d", i)] = cleanuphost.EventReceipt{Sequence: uint64(i)}
				return nil
			})
		}(i)
	}
	for i := 0; i < 16; i++ {
		if err = <-errorsOut; err != nil {
			t.Fatal(err)
		}
	}
	state := removeLinkJournalRead(t, j)
	if state.Serial != 16 || len(state.Events) != 16 {
		t.Fatal("lost concurrent journal update", state.Serial, len(state.Events))
	}
}

func TestPluginRemoveLinkJournalJoinsCallerTransaction(t *testing.T) {
	store, _, j := removeLinkJournalFixture(t, "cleanup.transaction", 1)
	ctx := context.Background()
	if _, err := store.DB.Exec(`CREATE TABLE synthetic_cleanup_cas(id INTEGER PRIMARY KEY,revision INTEGER NOT NULL); INSERT INTO synthetic_cleanup_cas VALUES(1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := j.updateTx(ctx, nil, func(*cleanuphost.Durable) error { return nil }); err == nil {
		t.Fatal("nil caller transaction accepted")
	}
	mutate := func(s *cleanuphost.Durable) error {
		s.Binding = cleanuphost.Binding{InstanceID: j.instance, Generation: j.generation, Revision: 2}
		s.Machine.Watches = []removelink.Watch{{Path: "/synthetic/transaction-watch", Mode: "hardlink"}}
		s.Events["synthetic-transaction"] = cleanuphost.EventReceipt{Binding: s.Binding}
		s.Active = &cleanuphost.Event{ID: "synthetic-active"}
		return nil
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.updateTx(ctx, tx, mutate); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	inside, _, _, err := j.read(ctx, tx)
	if err != nil || len(inside.Events) != 1 {
		_ = tx.Rollback()
		t.Fatal("helper did not write caller's transaction", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE synthetic_cleanup_cas SET revision=2 WHERE id=1 AND revision=1`); err != nil {
		_ = tx.Rollback()
		t.Fatal("helper committed caller transaction", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := removeLinkJournalRead(t, j); len(got.Events) != 0 || len(got.Machine.Watches) != 0 || got.Active != nil {
		t.Fatal("caller rollback did not roll back journal")
	}
	tx, err = store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE synthetic_cleanup_cas SET revision=2 WHERE id=1 AND revision=1`)
	if err != nil {
		t.Fatal(err)
	}
	if count, e := result.RowsAffected(); e != nil || count != 1 {
		t.Fatal("synthetic host revision CAS did not match", count, e)
	}
	if err = j.updateTx(ctx, tx, mutate); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err = store.DB.QueryRow(`SELECT revision FROM synthetic_cleanup_cas WHERE id=1`).Scan(&revision); err != nil || revision != 2 {
		t.Fatal("caller CAS did not commit", err, revision)
	}
	got := removeLinkJournalRead(t, j)
	if got.Binding.Revision != 2 || len(got.Events) != 1 || len(got.Machine.Watches) != 1 || got.Active == nil {
		t.Fatal("caller commit did not preserve all journal changes")
	}
}
