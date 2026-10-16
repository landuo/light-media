package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"unicode/utf8"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

// This private journal shares the App database and publication transactions. It
// stores source plans and effect evidence, never a second execution queue.
const pluginCleanInvalidSeedSchema = `CREATE TABLE IF NOT EXISTS plugin_clean_seed_meta(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,digest BLOB NOT NULL,PRIMARY KEY(instance_id,generation));
CREATE TABLE IF NOT EXISTS plugin_clean_seed_rows(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,collection TEXT NOT NULL,token TEXT NOT NULL,ordinal INTEGER NOT NULL,sealed BLOB NOT NULL,digest BLOB NOT NULL,
 PRIMARY KEY(instance_id,generation,collection,token));
CREATE TABLE IF NOT EXISTS plugin_clean_seed_core_receipts(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,token TEXT NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation,token));
CREATE TABLE IF NOT EXISTS plugin_clean_seed_outbox(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,token TEXT NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation,token));`
const pluginCleanInvalidSeedJournalLimit = 32 << 20
const pluginCleanInvalidSeedJournalRows = 200000

type pluginCleanInvalidSeedTransition struct {
	Version                int `json:"version"`
	ID, Kind, Phase, RunID string
	From, To               seedhost.Binding
	ConfigSHA              string
}
type pluginCleanInvalidSeedDownloaderRef struct {
	ID, Name, Kind, ConfigSHA string
	Revision                  int64
	Inactive                  bool
}
type pluginCleanInvalidSeedSnapshot struct {
	Binding     seedhost.Binding
	ConfigSHA   string
	Downloaders []pluginCleanInvalidSeedDownloaderRef
	Roots       []seedhost.Root
}
type pluginCleanInvalidSeedMeta struct {
	Version        int                                         `json:"version"`
	Instance       string                                      `json:"instance"`
	Generation     int64                                       `json:"generation"`
	Binding        seedhost.Binding                            `json:"binding"`
	Config         []byte                                      `json:"config"`
	Collections    map[string]pluginCleanInvalidSeedCollection `json:"collections"`
	Transition     *pluginCleanInvalidSeedTransition           `json:"transition,omitempty"`
	Snapshot       *pluginCleanInvalidSeedSnapshot             `json:"snapshot,omitempty"`
	Closed         bool                                        `json:"closed,omitempty"`
	AppliedRun     string                                      `json:"applied_run,omitempty"`
	AppliedBinding seedhost.Binding                            `json:"applied_binding,omitempty"`
}
type pluginCleanInvalidSeedCollection struct {
	Count  int
	Digest []byte
}
type pluginCleanInvalidSeedCell struct {
	Version                int
	Instance               string
	Generation             int64
	Collection, Token, Key string
	Ordinal                int
	Data                   []byte
}
type pluginCleanInvalidSeedPhysical struct {
	Collection, Token string
	Ordinal           int
	Digest, Sealed    []byte
}
type pluginCleanInvalidSeedJournal struct {
	p          *PluginStore
	instance   string
	generation int64
}

var _ seedhost.Journal = (*pluginCleanInvalidSeedJournal)(nil)
var pluginCleanInvalidSeedCollections = []string{"runs", "effects", "triggers", "notices", "config_history"}

func newPluginCleanInvalidSeedJournal(ctx context.Context, p *PluginStore, instance string, generation int64) (*pluginCleanInvalidSeedJournal, error) {
	if p == nil || p.store == nil || p.store.DB == nil || !pluginStorageID.MatchString(instance) || generation < 1 {
		return nil, seedhost.ErrUnsafe
	}
	if _, err := p.store.DB.ExecContext(ctx, pluginCleanInvalidSeedSchema); err != nil {
		return nil, err
	}
	return &pluginCleanInvalidSeedJournal{p, instance, generation}, nil
}
func (j *pluginCleanInvalidSeedJournal) mac(kind string, b []byte) []byte {
	m := hmac.New(sha256.New, j.p.store.key)
	scope, _ := json.Marshal([]any{"light-media/clean-seed/v1", j.instance, j.generation, kind})
	m.Write(scope)
	m.Write([]byte{0})
	m.Write(b)
	return m.Sum(nil)
}
func (j *pluginCleanInvalidSeedJournal) token(collection, key string, ordinal int) string {
	b, _ := json.Marshal([]any{collection, key, ordinal})
	return hex.EncodeToString(j.mac("row-key", b))
}
func (j *pluginCleanInvalidSeedJournal) key(collection, token string) string {
	return "seed:v1:" + strconv.FormatInt(j.generation, 10) + ":" + collection + ":" + token
}
func (j *pluginCleanInvalidSeedJournal) seal(collection, token string, value any) ([]byte, []byte, error) {
	b, err := cleanupJournalJSON(value)
	if err != nil || len(b) > pluginCleanInvalidSeedJournalLimit {
		return nil, nil, seedhost.ErrUnsafe
	}
	encrypted, err := j.p.seal("clean_seed_journal", j.instance, j.key(collection, token), b)
	return encrypted, j.mac(collection+":"+token, b), err
}
func (j *pluginCleanInvalidSeedJournal) unseal(collection, token string, encrypted, digest []byte, out any) error {
	b, err := j.p.unseal("clean_seed_journal", j.instance, j.key(collection, token), encrypted)
	if err != nil {
		return err
	}
	if len(b) > pluginCleanInvalidSeedJournalLimit || !hmac.Equal(j.mac(collection+":"+token, b), digest) || cleanupJournalStrict(b, out) != nil {
		return seedhost.ErrUnsafe
	}
	return nil
}
func cleanSeedBinding(b seedhost.Binding, instance string, generation int64, zero bool) bool {
	return zero && b == (seedhost.Binding{}) || b.InstanceID == instance && b.Generation == generation && b.Revision > 0
}
func (j *pluginCleanInvalidSeedJournal) validate(state seedhost.State, meta pluginCleanInvalidSeedMeta) error {
	if !cleanupJournalStringsValid(reflect.ValueOf(state)) || !cleanupJournalStringsValid(reflect.ValueOf(meta)) {
		return seedhost.ErrUnsafe
	}
	if !cleanSeedBinding(state.Binding, j.instance, j.generation, true) {
		return seedhost.ErrUnsafe
	}
	for id, r := range state.Runs {
		if id != r.Invocation.RunID || !cleanSeedBinding(r.Invocation.Binding, j.instance, j.generation, false) || !cleanSeedBinding(r.Effective, j.instance, j.generation, false) || r.Invocation.InputSHA == "" {
			return seedhost.ErrUnsafe
		}
	}
	for id, e := range state.Effects {
		if id != e.ID || !cleanSeedBinding(e.Binding, j.instance, j.generation, false) || !cleanSeedBinding(e.Authority, j.instance, j.generation, false) || e.RunID == "" {
			return seedhost.ErrUnsafe
		}
	}
	for id, t := range state.Triggers {
		if id != t.ID || !cleanSeedBinding(t.Binding, j.instance, j.generation, false) || t.Queued != nil && (t.Queued.RunID != t.QueuedRunID || t.Queued.Binding != t.Binding || t.Queued.EventID != t.ID) {
			return seedhost.ErrUnsafe
		}
	}
	for id, n := range state.Notices {
		if id != n.ID || n.RunID == "" {
			return seedhost.ErrUnsafe
		}
	}
	if s := meta.Snapshot; s != nil && !cleanSeedBinding(s.Binding, j.instance, j.generation, false) {
		return seedhost.ErrUnsafe
	}
	if t := meta.Transition; t != nil {
		if t.Version != 1 || t.ID == "" || t.From.InstanceID != j.instance || t.From.Generation != j.generation || t.From.Revision < 0 || t.To.InstanceID != "" && t.To.InstanceID != j.instance {
			return seedhost.ErrUnsafe
		}
		if t.To.InstanceID != "" {
			if t.Kind == "reset" || t.Kind == "uninstall" {
				if t.To.Generation != j.generation+1 || t.To.Revision != 0 {
					return seedhost.ErrUnsafe
				}
			} else if t.To.Generation != j.generation || t.To.Revision < 1 {
				return seedhost.ErrUnsafe
			}
		}
	}
	return nil
}
func cleanSeedEmpty() seedhost.State {
	return seedhost.State{Runs: map[string]seedhost.Run{}, Effects: map[string]seedhost.Effect{}, Triggers: map[string]seedhost.Trigger{}, Notices: map[string]seedhost.Notice{}}
}
func (j *pluginCleanInvalidSeedJournal) cells(s seedhost.State) ([]pluginCleanInvalidSeedCell, error) {
	cells := []pluginCleanInvalidSeedCell{}
	put := func(collection, key string, ordinal int, v any) error {
		if !utf8.ValidString(key) {
			return seedhost.ErrUnsafe
		}
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		c := pluginCleanInvalidSeedCell{Version: 1, Instance: j.instance, Generation: j.generation, Collection: collection, Key: key, Ordinal: ordinal, Data: data}
		c.Token = j.token(collection, key, ordinal)
		cells = append(cells, c)
		return nil
	}
	for k, v := range s.Runs {
		if err := put("runs", k, -1, v); err != nil {
			return nil, err
		}
	}
	for k, v := range s.Effects {
		if err := put("effects", k, -1, v); err != nil {
			return nil, err
		}
	}
	for k, v := range s.Triggers {
		if err := put("triggers", k, -1, v); err != nil {
			return nil, err
		}
	}
	for k, v := range s.Notices {
		if err := put("notices", k, -1, v); err != nil {
			return nil, err
		}
	}
	for n, v := range s.ConfigHistory {
		if err := put("config_history", "", n, v); err != nil {
			return nil, err
		}
	}
	if len(cells) > pluginCleanInvalidSeedJournalRows {
		return nil, seedhost.ErrUnsafe
	}
	sort.Slice(cells, func(i, k int) bool {
		if cells[i].Collection == cells[k].Collection {
			return cells[i].Token < cells[k].Token
		}
		return cells[i].Collection < cells[k].Collection
	})
	return cells, nil
}
func (j *pluginCleanInvalidSeedJournal) stats(rows []pluginCleanInvalidSeedPhysical) map[string]pluginCleanInvalidSeedCollection {
	stats := map[string]pluginCleanInvalidSeedCollection{}
	for _, collection := range pluginCleanInvalidSeedCollections {
		values := []any{}
		for _, r := range rows {
			if r.Collection == collection {
				values = append(values, []any{r.Token, r.Ordinal, r.Digest})
			}
		}
		b, _ := json.Marshal(values)
		stats[collection] = pluginCleanInvalidSeedCollection{len(values), j.mac("aggregate:"+collection, b)}
	}
	return stats
}
func (j *pluginCleanInvalidSeedJournal) read(ctx context.Context, q pluginRemoveLinkJournalReader) (seedhost.State, pluginCleanInvalidSeedMeta, error) {
	s := cleanSeedEmpty()
	meta := pluginCleanInvalidSeedMeta{}
	var encrypted, digest []byte
	err := q.QueryRowContext(ctx, `SELECT sealed,digest FROM plugin_clean_seed_meta WHERE instance_id=? AND generation=?`, j.instance, j.generation).Scan(&encrypted, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_clean_seed_rows WHERE instance_id=? AND generation=?`, j.instance, j.generation).Scan(&count)
		if err != nil {
			return s, meta, err
		}
		if count != 0 {
			return s, meta, seedhost.ErrUnsafe
		}
		return s, meta, nil
	}
	if err != nil {
		return s, meta, err
	}
	if err = j.unseal("meta", "main", encrypted, digest, &meta); err != nil {
		return s, meta, err
	}
	if meta.Version != 1 || meta.Instance != j.instance || meta.Generation != j.generation || len(meta.Collections) != len(pluginCleanInvalidSeedCollections) {
		return s, meta, seedhost.ErrUnsafe
	}
	s.Binding, s.Config = meta.Binding, append(json.RawMessage(nil), meta.Config...)
	rows, err := q.QueryContext(ctx, `SELECT collection,token,ordinal,sealed,digest FROM plugin_clean_seed_rows WHERE instance_id=? AND generation=? ORDER BY collection,token`, j.instance, j.generation)
	if err != nil {
		return s, meta, err
	}
	defer rows.Close()
	physical := []pluginCleanInvalidSeedPhysical{}
	history := map[int]seedhost.ConfigWrite{}
	total := len(encrypted)
	for rows.Next() {
		var p pluginCleanInvalidSeedPhysical
		if err = rows.Scan(&p.Collection, &p.Token, &p.Ordinal, &p.Sealed, &p.Digest); err != nil {
			return s, meta, err
		}
		total += len(p.Sealed)
		if len(physical) >= pluginCleanInvalidSeedJournalRows || total > pluginCleanInvalidSeedJournalLimit {
			return s, meta, seedhost.ErrUnsafe
		}
		var c pluginCleanInvalidSeedCell
		if err = j.unseal(p.Collection, p.Token, p.Sealed, p.Digest, &c); err != nil {
			return s, meta, err
		}
		if c.Version != 1 || c.Instance != j.instance || c.Generation != j.generation || c.Collection != p.Collection || c.Token != p.Token || c.Ordinal != p.Ordinal || !utf8.ValidString(c.Key) || j.token(c.Collection, c.Key, c.Ordinal) != p.Token {
			return s, meta, seedhost.ErrUnsafe
		}
		if c.Collection != "config_history" && c.Ordinal != -1 || c.Collection == "config_history" && (c.Key != "" || c.Ordinal < 0) {
			return s, meta, seedhost.ErrUnsafe
		}
		switch c.Collection {
		case "runs":
			var v seedhost.Run
			if cleanupJournalStrict(c.Data, &v) != nil {
				return s, meta, seedhost.ErrUnsafe
			}
			s.Runs[c.Key] = v
		case "effects":
			var v seedhost.Effect
			if cleanupJournalStrict(c.Data, &v) != nil {
				return s, meta, seedhost.ErrUnsafe
			}
			s.Effects[c.Key] = v
		case "triggers":
			var v seedhost.Trigger
			if cleanupJournalStrict(c.Data, &v) != nil {
				return s, meta, seedhost.ErrUnsafe
			}
			s.Triggers[c.Key] = v
		case "notices":
			var v seedhost.Notice
			if cleanupJournalStrict(c.Data, &v) != nil {
				return s, meta, seedhost.ErrUnsafe
			}
			s.Notices[c.Key] = v
		case "config_history":
			var v seedhost.ConfigWrite
			if cleanupJournalStrict(c.Data, &v) != nil {
				return s, meta, seedhost.ErrUnsafe
			}
			if _, ok := history[c.Ordinal]; ok {
				return s, meta, seedhost.ErrUnsafe
			}
			history[c.Ordinal] = v
		default:
			return s, meta, seedhost.ErrUnsafe
		}
		p.Sealed = nil
		physical = append(physical, p)
	}
	if err = rows.Err(); err != nil {
		return s, meta, err
	}
	for n := 0; n < len(history); n++ {
		v, ok := history[n]
		if !ok {
			return s, meta, seedhost.ErrUnsafe
		}
		s.ConfigHistory = append(s.ConfigHistory, v)
	}
	actual := j.stats(physical)
	for name, want := range meta.Collections {
		got, ok := actual[name]
		if !ok || got.Count != want.Count || !hmac.Equal(got.Digest, want.Digest) {
			return s, meta, seedhost.ErrUnsafe
		}
	}
	if err = j.validate(s, meta); err != nil {
		return s, meta, err
	}
	return s, meta, nil
}
func (j *pluginCleanInvalidSeedJournal) Read(ctx context.Context) (seedhost.State, error) {
	tx, err := j.p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return seedhost.State{}, err
	}
	defer tx.Rollback()
	s, _, err := j.read(ctx, tx)
	return s, err
}
func (j *pluginCleanInvalidSeedJournal) Update(ctx context.Context, fn func(*sql.Tx, *seedhost.State) error) error {
	tx, err := j.p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = j.updateTx(ctx, tx, func(s *seedhost.State, _ *pluginCleanInvalidSeedMeta) error { return fn(tx, s) }); err != nil {
		return err
	}
	return tx.Commit()
}
func (j *pluginCleanInvalidSeedJournal) updateTx(ctx context.Context, tx *sql.Tx, fn func(*seedhost.State, *pluginCleanInvalidSeedMeta) error) error {
	s, meta, err := j.read(ctx, tx)
	if err != nil {
		return err
	}
	if err = fn(&s, &meta); err != nil {
		return err
	}
	if err = j.validate(s, meta); err != nil {
		return err
	}
	cells, err := j.cells(s)
	if err != nil {
		return err
	}
	old := map[string][]byte{}
	rows, err := tx.QueryContext(ctx, `SELECT collection,token,digest FROM plugin_clean_seed_rows WHERE instance_id=? AND generation=?`, j.instance, j.generation)
	if err != nil {
		return err
	}
	for rows.Next() {
		var collection, token string
		var digest []byte
		if err = rows.Scan(&collection, &token, &digest); err != nil {
			rows.Close()
			return err
		}
		old[collection+":"+token] = digest
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	physical := make([]pluginCleanInvalidSeedPhysical, 0, len(cells))
	total := 0
	for _, c := range cells {
		encrypted, digest, err := j.seal(c.Collection, c.Token, c)
		if err != nil {
			return err
		}
		total += len(encrypted)
		if total > pluginCleanInvalidSeedJournalLimit {
			return seedhost.ErrUnsafe
		}
		key := c.Collection + ":" + c.Token
		if !hmac.Equal(old[key], digest) {
			if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_clean_seed_rows VALUES(?,?,?,?,?,?,?) ON CONFLICT(instance_id,generation,collection,token) DO UPDATE SET ordinal=excluded.ordinal,sealed=excluded.sealed,digest=excluded.digest`, j.instance, j.generation, c.Collection, c.Token, c.Ordinal, encrypted, digest); err != nil {
				return err
			}
		}
		delete(old, key)
		physical = append(physical, pluginCleanInvalidSeedPhysical{Collection: c.Collection, Token: c.Token, Ordinal: c.Ordinal, Digest: digest})
	}
	for key := range old {
		for n, b := range []byte(key) {
			if b == ':' {
				if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_clean_seed_rows WHERE instance_id=? AND generation=? AND collection=? AND token=?`, j.instance, j.generation, key[:n], key[n+1:]); err != nil {
					return err
				}
				break
			}
		}
	}
	meta.Version, meta.Instance, meta.Generation, meta.Binding, meta.Config, meta.Collections = 1, j.instance, j.generation, s.Binding, append([]byte(nil), s.Config...), j.stats(physical)
	encrypted, digest, err := j.seal("meta", "main", meta)
	if err != nil {
		return err
	}
	if total+len(encrypted) > pluginCleanInvalidSeedJournalLimit {
		return seedhost.ErrUnsafe
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_clean_seed_meta VALUES(?,?,?,?) ON CONFLICT(instance_id,generation) DO UPDATE SET sealed=excluded.sealed,digest=excluded.digest`, j.instance, j.generation, encrypted, digest)
	return err
}
