package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strconv"
	"unicode/utf8"

	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

// This is private host storage, not a worker capability or an authorization
// guard. Namespace generations never share machine state or effect receipts.
type pluginRemoveLinkJournal struct {
	p          *PluginStore
	instance   string
	generation int64
}

var _ cleanuphost.Journal = (*pluginRemoveLinkJournal)(nil)

const pluginRemoveLinkJournalVersion = 1
const pluginRemoveLinkJournalSchema = `CREATE TABLE IF NOT EXISTS plugin_cleanup_journal_meta(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL CHECK(generation>0),version INTEGER NOT NULL,digest BLOB NOT NULL,sealed BLOB NOT NULL,updated_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,generation));
CREATE TABLE IF NOT EXISTS plugin_cleanup_journal_rows(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL CHECK(generation>0),collection TEXT NOT NULL,row_key TEXT NOT NULL,ordinal INTEGER NOT NULL CHECK(ordinal>=-1),digest BLOB NOT NULL,sealed BLOB NOT NULL,updated_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,generation,collection,row_key));`

var pluginRemoveLinkJournalCollections = []string{"roots", "state", "queue", "watches", "pins", "frozen", "events", "effects"}

type pluginRemoveLinkJournalStats struct {
	Count  int    `json:"count"`
	Digest []byte `json:"digest"`
}
type pluginRemoveLinkJournalMeta struct {
	Version     int                                     `json:"version"`
	Instance    string                                  `json:"instance"`
	Generation  int64                                   `json:"generation"`
	Binding     cleanuphost.Binding                     `json:"binding"`
	Config      []byte                                  `json:"config"`
	TimerDue    json.RawMessage                         `json:"timer_due"`
	Serial      uint64                                  `json:"serial"`
	EventSerial uint64                                  `json:"event_serial"`
	Active      *cleanuphost.Event                      `json:"active"`
	Blocked     string                                  `json:"blocked"`
	Collections map[string]pluginRemoveLinkJournalStats `json:"collections"`
	Transition  *cleanuphost.Transition                 `json:"transition,omitempty"`
}
type pluginRemoveLinkJournalRow struct {
	Version    int     `json:"version"`
	Instance   string  `json:"instance"`
	Generation int64   `json:"generation"`
	Collection string  `json:"collection"`
	Token      string  `json:"token"`
	Ordinal    int64   `json:"ordinal"`
	Key        *string `json:"key"`
	Value      []byte  `json:"value"`
}
type pluginRemoveLinkJournalEffect struct {
	Effect  cleanuphost.Effect `json:"effect"`
	Payload []byte             `json:"payload"`
}
type pluginRemoveLinkJournalEntry struct {
	Collection, Token string
	Ordinal           int64
	Digest            []byte
}
type pluginRemoveLinkJournalReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func newPluginRemoveLinkJournal(ctx context.Context, p *PluginStore, instanceID string, generation int64) (*pluginRemoveLinkJournal, error) {
	if p == nil || p.store == nil || p.store.DB == nil || !pluginStorageID.MatchString(instanceID) || generation < 1 {
		return nil, bad("INVALID_CLEANUP_JOURNAL_SCOPE", "清理状态的实例或代次无效")
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, pluginRemoveLinkJournalSchema); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &pluginRemoveLinkJournal{p, instanceID, generation}, nil
}
func cleanupJournalCorrupt() error {
	return bad("CLEANUP_JOURNAL_CORRUPT", "清理状态无法通过格式或完整性校验")
}
func cleanupJournalScope() error {
	return bad("CLEANUP_JOURNAL_SCOPE_MISMATCH", "清理状态不属于当前插件实例或代次")
}
func cleanupJournalStrict(raw []byte, out any) error {
	if !utf8.Valid(raw) {
		return cleanupJournalCorrupt()
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && reflect.ValueOf(out).Elem().Kind() == reflect.Struct {
		return cleanupJournalCorrupt()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return cleanupJournalCorrupt()
	}
	return nil
}

// Canonical wire JSON preserves numeric lexemes with UseNumber. Config and
// Effect.Payload are opaque byte slots, so even their original whitespace and
// large integer text survive; they are never decoded through float64.
func cleanupJournalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err = d.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
func (j *pluginRemoveLinkJournal) mac(purpose string, raw []byte) []byte {
	mac := hmac.New(sha256.New, j.p.store.key)
	scope, _ := json.Marshal([]any{"light-media/cleanup-journal/v1", j.instance, j.generation, purpose})
	mac.Write(scope)
	mac.Write([]byte{0})
	mac.Write(raw)
	return mac.Sum(nil)
}
func (j *pluginRemoveLinkJournal) rowToken(collection string, key *string, ordinal int64) string {
	raw, _ := json.Marshal(struct {
		Collection string
		Key        *string
		Ordinal    int64
	}{collection, key, ordinal})
	return hex.EncodeToString(j.mac("row-key", raw))
}
func (j *pluginRemoveLinkJournal) aad(collection, token string) string {
	return "v1:" + strconv.FormatInt(j.generation, 10) + ":" + collection + ":" + token
}
func cleanupJournalCollection(name string) (isMap, known bool) {
	switch name {
	case "roots", "state", "queue", "watches":
		return false, true
	case "pins", "frozen", "events", "effects":
		return true, true
	}
	return false, false
}
func cleanupJournalInitialize(s *cleanuphost.Durable) {
	if s.Roots == nil {
		s.Roots = []cleanuphost.RootStamp{}
	}
	if s.Machine.State == nil {
		s.Machine.State = []removelink.Tracked{}
	}
	if s.Machine.Queue == nil {
		s.Machine.Queue = []*removelink.Task{}
	}
	if s.Machine.Watches == nil {
		s.Machine.Watches = []removelink.Watch{}
	}
	if s.Pins == nil {
		s.Pins = map[string]cleanuphost.Pin{}
	}
	if s.Frozen == nil {
		s.Frozen = map[string]cleanuphost.Pin{}
	}
	if s.Events == nil {
		s.Events = map[string]cleanuphost.EventReceipt{}
	}
	if s.Effects == nil {
		s.Effects = map[string]cleanuphost.Effect{}
	}
}
func (j *pluginRemoveLinkJournal) binding(b cleanuphost.Binding) error {
	if b == (cleanuphost.Binding{}) {
		return nil
	}
	if b.InstanceID != j.instance || b.Generation != j.generation || b.Revision < 0 {
		return cleanupJournalScope()
	}
	return nil
}

// encoding/json replaces malformed UTF-8 strings. Reject them before writing
// typed host paths/IDs instead of silently persisting a different identity.
// Opaque byte slices remain byte slices and are not traversed or reencoded.
func cleanupJournalStringsValid(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return v.IsNil() || cleanupJournalStringsValid(v.Elem())
	case reflect.String:
		return utf8.ValidString(v.String())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath == "" && !cleanupJournalStringsValid(v.Field(i)) {
				return false
			}
		}
	case reflect.Map:
		for iter := v.MapRange(); iter.Next(); {
			if !cleanupJournalStringsValid(iter.Key()) || !cleanupJournalStringsValid(iter.Value()) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return true
		}
		for i := 0; i < v.Len(); i++ {
			if !cleanupJournalStringsValid(v.Index(i)) {
				return false
			}
		}
	}
	return true
}
func (j *pluginRemoveLinkJournal) validate(s *cleanuphost.Durable) error {
	if !cleanupJournalStringsValid(reflect.ValueOf(s)) {
		return cleanupJournalCorrupt()
	}
	if err := j.binding(s.Binding); err != nil {
		return err
	}
	if t := s.Transition; t != nil {
		if err := j.binding(t.From); err != nil {
			return err
		}
		if t.From.InstanceID == "" || t.From.Revision < 1 {
			return cleanupJournalScope()
		}
		if t.To != (cleanuphost.Binding{}) {
			generation := j.generation
			if t.Kind == "reset" || t.Kind == "uninstall" {
				generation++
			}
			if t.To.InstanceID != j.instance || t.To.Generation < 1 || t.To.Generation != generation || t.To.Revision < 0 {
				return cleanupJournalScope()
			}
		}
	}
	if len(s.Config) > 0 && !json.Valid(s.Config) {
		return cleanupJournalCorrupt()
	}
	for _, value := range s.Events {
		if err := j.binding(value.Binding); err != nil {
			return err
		}
	}
	for _, value := range s.Effects {
		if err := j.binding(value.Binding); err != nil {
			return err
		}
		if len(value.Payload) > 0 && !json.Valid(value.Payload) {
			return cleanupJournalCorrupt()
		}
	}
	return nil
}
func (j *pluginRemoveLinkJournal) stats(entries map[string]pluginRemoveLinkJournalEntry) map[string]pluginRemoveLinkJournalStats {
	out := map[string]pluginRemoveLinkJournalStats{}
	for _, collection := range pluginRemoveLinkJournalCollections {
		items := []pluginRemoveLinkJournalEntry{}
		for _, entry := range entries {
			if entry.Collection == collection {
				items = append(items, entry)
			}
		}
		sort.Slice(items, func(a, b int) bool { return items[a].Token < items[b].Token })
		raw, _ := cleanupJournalJSON(items)
		out[collection] = pluginRemoveLinkJournalStats{Count: len(items), Digest: j.mac("collection:"+collection, raw)}
	}
	return out
}
func cleanupJournalEntryKey(collection, token string) string { return collection + ":" + token }
func (j *pluginRemoveLinkJournal) Read(ctx context.Context) (cleanuphost.Durable, error) {
	tx, err := j.p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return cleanuphost.Durable{}, err
	}
	defer tx.Rollback()
	state, _, _, err := j.read(ctx, tx)
	if err != nil {
		return cleanuphost.Durable{}, err
	}
	if err = tx.Commit(); err != nil {
		return cleanuphost.Durable{}, err
	}
	return state, nil
}
func (j *pluginRemoveLinkJournal) read(ctx context.Context, db pluginRemoveLinkJournalReader) (cleanuphost.Durable, map[string]pluginRemoveLinkJournalEntry, []byte, error) {
	state := cleanuphost.Durable{}
	cleanupJournalInitialize(&state)
	entries := map[string]pluginRemoveLinkJournalEntry{}
	var version int
	var digest, sealed []byte
	err := db.QueryRowContext(ctx, `SELECT version,digest,sealed FROM plugin_cleanup_journal_meta WHERE instance_id=? AND generation=?`, j.instance, j.generation).Scan(&version, &digest, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_cleanup_journal_rows WHERE instance_id=? AND generation=?`, j.instance, j.generation).Scan(&count); err != nil {
			return state, nil, nil, err
		}
		if count != 0 {
			return state, nil, nil, cleanupJournalCorrupt()
		}
		return state, entries, nil, nil
	}
	if err != nil {
		return state, nil, nil, err
	}
	if version != pluginRemoveLinkJournalVersion {
		return state, nil, nil, cleanupJournalCorrupt()
	}
	raw, err := j.p.unseal("cleanup_journal", j.instance, j.aad("meta", "main"), sealed)
	if err != nil {
		return state, nil, nil, err
	}
	var meta pluginRemoveLinkJournalMeta
	if err = cleanupJournalStrict(raw, &meta); err != nil {
		return state, nil, nil, err
	}
	canonical, err := cleanupJournalJSON(meta)
	if err != nil || !hmac.Equal(digest, j.mac("value:meta", canonical)) {
		return state, nil, nil, cleanupJournalCorrupt()
	}
	if meta.Version != version || meta.Instance != j.instance || meta.Generation != j.generation {
		return state, nil, nil, cleanupJournalScope()
	}
	if len(meta.Collections) != len(pluginRemoveLinkJournalCollections) {
		return state, nil, nil, cleanupJournalCorrupt()
	}
	state.Binding = meta.Binding
	state.Config = append(json.RawMessage(nil), meta.Config...)
	if meta.Config != nil && len(meta.Config) == 0 {
		state.Config = json.RawMessage{}
	}
	state.Serial = meta.Serial
	state.EventSerial = meta.EventSerial
	state.Active = meta.Active
	state.Blocked = meta.Blocked
	state.Transition = meta.Transition
	if len(meta.TimerDue) == 0 || cleanupJournalStrict(meta.TimerDue, &state.Machine.TimerDue) != nil {
		return state, nil, nil, cleanupJournalCorrupt()
	}
	rows, err := db.QueryContext(ctx, `SELECT collection,row_key,ordinal,digest,sealed FROM plugin_cleanup_journal_rows WHERE instance_id=? AND generation=? ORDER BY collection,ordinal,row_key`, j.instance, j.generation)
	if err != nil {
		return state, nil, nil, err
	}
	for rows.Next() {
		var collection, token string
		var ordinal int64
		var rowDigest, rowSealed []byte
		if err = rows.Scan(&collection, &token, &ordinal, &rowDigest, &rowSealed); err != nil {
			rows.Close()
			return state, nil, nil, err
		}
		isMap, known := cleanupJournalCollection(collection)
		if !known {
			rows.Close()
			return state, nil, nil, cleanupJournalCorrupt()
		}
		rowRaw, e := j.p.unseal("cleanup_journal", j.instance, j.aad(collection, token), rowSealed)
		if e != nil {
			rows.Close()
			return state, nil, nil, e
		}
		var row pluginRemoveLinkJournalRow
		if e = cleanupJournalStrict(rowRaw, &row); e != nil {
			rows.Close()
			return state, nil, nil, e
		}
		encoded, e := cleanupJournalJSON(row)
		if e != nil || !hmac.Equal(rowDigest, j.mac("value:"+collection, encoded)) {
			rows.Close()
			return state, nil, nil, cleanupJournalCorrupt()
		}
		if row.Version != version || row.Instance != j.instance || row.Generation != j.generation || row.Collection != collection || row.Token != token || row.Ordinal != ordinal || isMap != (row.Key != nil) || (isMap && ordinal != -1) || (!isMap && ordinal < 0) || token != j.rowToken(collection, row.Key, ordinal) {
			rows.Close()
			return state, nil, nil, cleanupJournalCorrupt()
		}
		if row.Key != nil && !utf8.ValidString(*row.Key) {
			rows.Close()
			return state, nil, nil, cleanupJournalCorrupt()
		}
		if err = j.decodeRow(&state, row); err != nil {
			rows.Close()
			return state, nil, nil, err
		}
		key := cleanupJournalEntryKey(collection, token)
		entries[key] = pluginRemoveLinkJournalEntry{collection, token, ordinal, append([]byte(nil), rowDigest...)}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return state, nil, nil, err
	}
	if err = rows.Close(); err != nil {
		return state, nil, nil, err
	}
	actual := j.stats(entries)
	for _, collection := range pluginRemoveLinkJournalCollections {
		want, exists := meta.Collections[collection]
		got := actual[collection]
		if !exists || want.Count != got.Count || !hmac.Equal(want.Digest, got.Digest) {
			return state, nil, nil, cleanupJournalCorrupt()
		}
	}
	if err = j.validate(&state); err != nil {
		return state, nil, nil, err
	}
	return state, entries, append([]byte(nil), digest...), nil
}
func (j *pluginRemoveLinkJournal) decodeRow(state *cleanuphost.Durable, row pluginRemoveLinkJournalRow) error {
	switch row.Collection {
	case "roots":
		if row.Ordinal != int64(len(state.Roots)) {
			return cleanupJournalCorrupt()
		}
		var v cleanuphost.RootStamp
		if err := cleanupJournalStrict(row.Value, &v); err != nil {
			return err
		}
		state.Roots = append(state.Roots, v)
	case "state":
		if row.Ordinal != int64(len(state.Machine.State)) {
			return cleanupJournalCorrupt()
		}
		var v removelink.Tracked
		if err := cleanupJournalStrict(row.Value, &v); err != nil {
			return err
		}
		state.Machine.State = append(state.Machine.State, v)
	case "queue":
		if row.Ordinal != int64(len(state.Machine.Queue)) {
			return cleanupJournalCorrupt()
		}
		var v *removelink.Task
		if err := cleanupJournalStrict(row.Value, &v); err != nil {
			return err
		}
		state.Machine.Queue = append(state.Machine.Queue, v)
	case "watches":
		if row.Ordinal != int64(len(state.Machine.Watches)) {
			return cleanupJournalCorrupt()
		}
		var v removelink.Watch
		if err := cleanupJournalStrict(row.Value, &v); err != nil {
			return err
		}
		state.Machine.Watches = append(state.Machine.Watches, v)
	case "pins":
		var v cleanuphost.Pin
		if err := cleanupJournalStrict(row.Value, &v); err != nil {
			return err
		}
		if _, exists := state.Pins[*row.Key]; exists {
			return cleanupJournalCorrupt()
		}
		state.Pins[*row.Key] = v
	case "frozen":
		var v cleanuphost.Pin
		if err := cleanupJournalStrict(row.Value, &v); err != nil {
			return err
		}
		if _, exists := state.Frozen[*row.Key]; exists {
			return cleanupJournalCorrupt()
		}
		state.Frozen[*row.Key] = v
	case "events":
		var v cleanuphost.EventReceipt
		if err := cleanupJournalStrict(row.Value, &v); err != nil {
			return err
		}
		if _, exists := state.Events[*row.Key]; exists {
			return cleanupJournalCorrupt()
		}
		state.Events[*row.Key] = v
	case "effects":
		var v pluginRemoveLinkJournalEffect
		if err := cleanupJournalStrict(row.Value, &v); err != nil {
			return err
		}
		// The embedded Effect is always written with Payload=nil, whose JSON
		// representation is null. RawMessage decodes that literal as bytes,
		// not a nil slice; the actual payload belongs only to the opaque slot.
		if !bytes.Equal(v.Effect.Payload, []byte("null")) {
			return cleanupJournalCorrupt()
		}
		v.Effect.Payload = append(json.RawMessage(nil), v.Payload...)
		if v.Payload != nil && len(v.Payload) == 0 {
			v.Effect.Payload = json.RawMessage{}
		}
		if _, exists := state.Effects[*row.Key]; exists {
			return cleanupJournalCorrupt()
		}
		state.Effects[*row.Key] = v.Effect
	default:
		return cleanupJournalCorrupt()
	}
	return nil
}
func (j *pluginRemoveLinkJournal) Update(ctx context.Context, fn func(*cleanuphost.Durable) error) error {
	if fn == nil {
		return bad("INVALID_CLEANUP_JOURNAL_UPDATE", "清理状态更新函数无效")
	}
	tx, err := j.p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = j.updateTx(ctx, tx, fn); err != nil {
		return err
	}
	return tx.Commit()
}

// updateTx joins a host transaction; it never acquires pluginMu or owns the
// transaction lifecycle. The caller must roll back the entire transaction on
// any error and may perform configuration/run CAS in this same transaction.
// fn is trusted host code and must only mutate memory, without external I/O.
func (j *pluginRemoveLinkJournal) updateTx(ctx context.Context, tx *sql.Tx, fn func(*cleanuphost.Durable) error) error {
	if tx == nil || fn == nil {
		return bad("INVALID_CLEANUP_JOURNAL_UPDATE", "清理状态更新事务或函数无效")
	}
	state, old, oldMetaDigest, err := j.read(ctx, tx)
	if err != nil {
		return err
	}
	if err = fn(&state); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	cleanupJournalInitialize(&state)
	if err = j.validate(&state); err != nil {
		return err
	}
	entries := map[string]pluginRemoveLinkJournalEntry{}
	syncRow := func(collection string, key *string, ordinal int64, value any) error {
		if key != nil && !utf8.ValidString(*key) {
			return cleanupJournalCorrupt()
		}
		valueRaw, e := json.Marshal(value)
		if e != nil {
			return e
		}
		token := j.rowToken(collection, key, ordinal)
		row := pluginRemoveLinkJournalRow{pluginRemoveLinkJournalVersion, j.instance, j.generation, collection, token, ordinal, key, valueRaw}
		raw, e := cleanupJournalJSON(row)
		if e != nil {
			return e
		}
		if len(raw) > pluginDataValueLimit {
			return bad("CLEANUP_JOURNAL_ROW_TOO_LARGE", "清理状态单项超过大小限制")
		}
		digest := j.mac("value:"+collection, raw)
		entryKey := cleanupJournalEntryKey(collection, token)
		previous, exists := old[entryKey]
		entries[entryKey] = pluginRemoveLinkJournalEntry{collection, token, ordinal, digest}
		delete(old, entryKey)
		if exists && previous.Ordinal == ordinal && hmac.Equal(previous.Digest, digest) {
			return nil
		}
		sealed, e := j.p.seal("cleanup_journal", j.instance, j.aad(collection, token), raw)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO plugin_cleanup_journal_rows(instance_id,generation,collection,row_key,ordinal,digest,sealed,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,generation,collection,row_key) DO UPDATE SET ordinal=excluded.ordinal,digest=excluded.digest,sealed=excluded.sealed,updated_at=excluded.updated_at`, j.instance, j.generation, collection, token, ordinal, digest, sealed, now())
		return e
	}
	for i, value := range state.Roots {
		if err = syncRow("roots", nil, int64(i), value); err != nil {
			return err
		}
	}
	for i, value := range state.Machine.State {
		if err = syncRow("state", nil, int64(i), value); err != nil {
			return err
		}
	}
	for i, value := range state.Machine.Queue {
		if err = syncRow("queue", nil, int64(i), value); err != nil {
			return err
		}
	}
	for i, value := range state.Machine.Watches {
		if err = syncRow("watches", nil, int64(i), value); err != nil {
			return err
		}
	}
	keys := func(v any) []string {
		out := []string{}
		switch values := v.(type) {
		case map[string]cleanuphost.Pin:
			for key := range values {
				out = append(out, key)
			}
		case map[string]cleanuphost.EventReceipt:
			for key := range values {
				out = append(out, key)
			}
		case map[string]cleanuphost.Effect:
			for key := range values {
				out = append(out, key)
			}
		}
		sort.Strings(out)
		return out
	}
	for _, key := range keys(state.Pins) {
		if err = syncRow("pins", &key, -1, state.Pins[key]); err != nil {
			return err
		}
	}
	for _, key := range keys(state.Frozen) {
		if err = syncRow("frozen", &key, -1, state.Frozen[key]); err != nil {
			return err
		}
	}
	for _, key := range keys(state.Events) {
		if err = syncRow("events", &key, -1, state.Events[key]); err != nil {
			return err
		}
	}
	for _, key := range keys(state.Effects) {
		value := state.Effects[key]
		payload := []byte(value.Payload)
		value.Payload = nil
		if err = syncRow("effects", &key, -1, pluginRemoveLinkJournalEffect{value, payload}); err != nil {
			return err
		}
	}
	for _, entry := range old {
		if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_cleanup_journal_rows WHERE instance_id=? AND generation=? AND collection=? AND row_key=?`, j.instance, j.generation, entry.Collection, entry.Token); err != nil {
			return err
		}
	}
	timer, err := json.Marshal(state.Machine.TimerDue)
	if err != nil {
		return err
	}
	meta := pluginRemoveLinkJournalMeta{
		Version: pluginRemoveLinkJournalVersion, Instance: j.instance, Generation: j.generation,
		Binding: state.Binding, Config: []byte(state.Config), TimerDue: timer,
		Serial: state.Serial, EventSerial: state.EventSerial, Active: state.Active,
		Blocked: state.Blocked, Collections: j.stats(entries), Transition: state.Transition,
	}
	raw, err := cleanupJournalJSON(meta)
	if err != nil {
		return err
	}
	if len(raw) > pluginDataValueLimit {
		return bad("CLEANUP_JOURNAL_ROW_TOO_LARGE", "清理状态单项超过大小限制")
	}
	digest := j.mac("value:meta", raw)
	if !hmac.Equal(digest, oldMetaDigest) {
		sealed, e := j.p.seal("cleanup_journal", j.instance, j.aad("meta", "main"), raw)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_cleanup_journal_meta(instance_id,generation,version,digest,sealed,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(instance_id,generation) DO UPDATE SET version=excluded.version,digest=excluded.digest,sealed=excluded.sealed,updated_at=excluded.updated_at`, j.instance, j.generation, pluginRemoveLinkJournalVersion, digest, sealed, now()); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return nil
}
