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

	"light-media/internal/plugins/scrapefileclean"
	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

// This is private host storage, not a worker capability or an authorization
// guard. Namespace generations never share machine state or effect receipts.
type pluginScrapeCleanJournal struct {
	p          *PluginStore
	instance   string
	generation int64
}

var _ scrapehost.Journal = (*pluginScrapeCleanJournal)(nil)

const pluginScrapeCleanJournalVersion = 1
const pluginScrapeCleanJournalSchema = `CREATE TABLE IF NOT EXISTS plugin_scrape_journal_meta(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL CHECK(generation>0),version INTEGER NOT NULL,digest BLOB NOT NULL,sealed BLOB NOT NULL,updated_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,generation));
CREATE TABLE IF NOT EXISTS plugin_scrape_journal_rows(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL CHECK(generation>0),collection TEXT NOT NULL,row_key TEXT NOT NULL,ordinal INTEGER NOT NULL CHECK(ordinal>=-1),digest BLOB NOT NULL,sealed BLOB NOT NULL,updated_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,generation,collection,row_key));`

var pluginScrapeCleanJournalCollections = []string{"roots", "state", "queue", "watches", "pins", "frozen", "events", "effects"}

type pluginScrapeCleanJournalStats struct {
	Count  int    `json:"count"`
	Digest []byte `json:"digest"`
}
type pluginScrapeCleanJournalMeta struct {
	Version     int                                      `json:"version"`
	Instance    string                                   `json:"instance"`
	Generation  int64                                    `json:"generation"`
	Binding     scrapehost.Binding                       `json:"binding"`
	Config      []byte                                   `json:"config"`
	TimerDue    json.RawMessage                          `json:"timer_due"`
	Serial      uint64                                   `json:"serial"`
	EventSerial uint64                                   `json:"event_serial"`
	Active      *scrapehost.Event                        `json:"active"`
	Blocked     string                                   `json:"blocked"`
	Collections map[string]pluginScrapeCleanJournalStats `json:"collections"`
	Transition  *scrapehost.Transition                   `json:"transition,omitempty"`
}
type pluginScrapeCleanJournalRow struct {
	Version    int     `json:"version"`
	Instance   string  `json:"instance"`
	Generation int64   `json:"generation"`
	Collection string  `json:"collection"`
	Token      string  `json:"token"`
	Ordinal    int64   `json:"ordinal"`
	Key        *string `json:"key"`
	Value      []byte  `json:"value"`
}
type pluginScrapeCleanJournalEffect struct {
	Effect  scrapehost.Effect `json:"effect"`
	Payload []byte            `json:"payload"`
}
type pluginScrapeCleanJournalEntry struct {
	Collection, Token string
	Ordinal           int64
	Digest            []byte
}
type pluginScrapeCleanJournalReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func newPluginScrapeCleanJournal(ctx context.Context, p *PluginStore, instanceID string, generation int64) (*pluginScrapeCleanJournal, error) {
	if p == nil || p.store == nil || p.store.DB == nil || !pluginStorageID.MatchString(instanceID) || generation < 1 {
		return nil, bad("INVALID_SCRAPE_CLEAN_JOURNAL_SCOPE", "清理状态的实例或代次无效")
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, pluginScrapeCleanJournalSchema); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &pluginScrapeCleanJournal{p, instanceID, generation}, nil
}
func scrapeJournalCorrupt() error {
	return bad("SCRAPE_CLEAN_JOURNAL_CORRUPT", "清理状态无法通过格式或完整性校验")
}
func scrapeJournalScope() error {
	return bad("SCRAPE_CLEAN_JOURNAL_SCOPE_MISMATCH", "清理状态不属于当前插件实例或代次")
}
func scrapeJournalStrict(raw []byte, out any) error {
	if !utf8.Valid(raw) {
		return scrapeJournalCorrupt()
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && reflect.ValueOf(out).Elem().Kind() == reflect.Struct {
		return scrapeJournalCorrupt()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return scrapeJournalCorrupt()
	}
	return nil
}

// Canonical wire JSON preserves numeric lexemes with UseNumber. Config and
// Effect.Payload are opaque byte slots, so even their original whitespace and
// large integer text survive; they are never decoded through float64.
func scrapeJournalJSON(value any) ([]byte, error) {
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
func (j *pluginScrapeCleanJournal) mac(purpose string, raw []byte) []byte {
	mac := hmac.New(sha256.New, j.p.store.key)
	scope, _ := json.Marshal([]any{"light-media/scrape-journal/v1", j.instance, j.generation, purpose})
	mac.Write(scope)
	mac.Write([]byte{0})
	mac.Write(raw)
	return mac.Sum(nil)
}
func (j *pluginScrapeCleanJournal) rowToken(collection string, key *string, ordinal int64) string {
	raw, _ := json.Marshal(struct {
		Collection string
		Key        *string
		Ordinal    int64
	}{collection, key, ordinal})
	return hex.EncodeToString(j.mac("row-key", raw))
}
func (j *pluginScrapeCleanJournal) aad(collection, token string) string {
	return "v1:" + strconv.FormatInt(j.generation, 10) + ":" + collection + ":" + token
}
func scrapeJournalCollection(name string) (isMap, known bool) {
	switch name {
	case "roots", "state", "queue", "watches":
		return false, true
	case "pins", "frozen", "events", "effects":
		return true, true
	}
	return false, false
}
func scrapeJournalInitialize(s *scrapehost.Durable) {
	if s.Roots == nil {
		s.Roots = []scrapehost.RootStamp{}
	}
	if s.Machine.State == nil {
		s.Machine.State = []scrapefileclean.Tracked{}
	}
	if s.Machine.Queue == nil {
		s.Machine.Queue = []*scrapefileclean.Task{}
	}
	if s.Machine.Watches == nil {
		s.Machine.Watches = []string{}
	}
	if s.Pins == nil {
		s.Pins = map[string]scrapehost.Pin{}
	}
	if s.Frozen == nil {
		s.Frozen = map[string]scrapehost.Pin{}
	}
	if s.Events == nil {
		s.Events = map[string]scrapehost.EventReceipt{}
	}
	if s.Effects == nil {
		s.Effects = map[string]scrapehost.Effect{}
	}
}
func (j *pluginScrapeCleanJournal) binding(b scrapehost.Binding) error {
	if b == (scrapehost.Binding{}) {
		return nil
	}
	if b.InstanceID != j.instance || b.Generation != j.generation || b.Revision < 0 {
		return scrapeJournalScope()
	}
	return nil
}

// encoding/json replaces malformed UTF-8 strings. Reject them before writing
// typed host paths/IDs instead of silently persisting a different identity.
// Opaque byte slices remain byte slices and are not traversed or reencoded.
func scrapeJournalStringsValid(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return v.IsNil() || scrapeJournalStringsValid(v.Elem())
	case reflect.String:
		return utf8.ValidString(v.String())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath == "" && !scrapeJournalStringsValid(v.Field(i)) {
				return false
			}
		}
	case reflect.Map:
		for iter := v.MapRange(); iter.Next(); {
			if !scrapeJournalStringsValid(iter.Key()) || !scrapeJournalStringsValid(iter.Value()) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return true
		}
		for i := 0; i < v.Len(); i++ {
			if !scrapeJournalStringsValid(v.Index(i)) {
				return false
			}
		}
	}
	return true
}
func (j *pluginScrapeCleanJournal) validate(s *scrapehost.Durable) error {
	if !scrapeJournalStringsValid(reflect.ValueOf(s)) {
		return scrapeJournalCorrupt()
	}
	if err := j.binding(s.Binding); err != nil {
		return err
	}
	if t := s.Transition; t != nil {
		if err := j.binding(t.From); err != nil {
			return err
		}
		if t.From.InstanceID == "" || t.From.Revision < 0 {
			return scrapeJournalScope()
		}
		if t.To != (scrapehost.Binding{}) {
			generation := j.generation
			if t.Kind == "reset" || t.Kind == "uninstall" {
				generation++
			}
			if t.To.InstanceID != j.instance || t.To.Generation < 1 || t.To.Generation != generation || t.To.Revision < 0 {
				return scrapeJournalScope()
			}
		}
	}
	if len(s.Config) > 0 && !json.Valid(s.Config) {
		return scrapeJournalCorrupt()
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
			return scrapeJournalCorrupt()
		}
	}
	return nil
}
func (j *pluginScrapeCleanJournal) stats(entries map[string]pluginScrapeCleanJournalEntry) map[string]pluginScrapeCleanJournalStats {
	out := map[string]pluginScrapeCleanJournalStats{}
	for _, collection := range pluginScrapeCleanJournalCollections {
		items := []pluginScrapeCleanJournalEntry{}
		for _, entry := range entries {
			if entry.Collection == collection {
				items = append(items, entry)
			}
		}
		sort.Slice(items, func(a, b int) bool { return items[a].Token < items[b].Token })
		raw, _ := scrapeJournalJSON(items)
		out[collection] = pluginScrapeCleanJournalStats{Count: len(items), Digest: j.mac("collection:"+collection, raw)}
	}
	return out
}
func scrapeJournalEntryKey(collection, token string) string { return collection + ":" + token }
func (j *pluginScrapeCleanJournal) Read(ctx context.Context) (scrapehost.Durable, error) {
	tx, err := j.p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return scrapehost.Durable{}, err
	}
	defer tx.Rollback()
	state, _, _, err := j.read(ctx, tx)
	if err != nil {
		return scrapehost.Durable{}, err
	}
	if err = tx.Commit(); err != nil {
		return scrapehost.Durable{}, err
	}
	return state, nil
}
func (j *pluginScrapeCleanJournal) read(ctx context.Context, db pluginScrapeCleanJournalReader) (scrapehost.Durable, map[string]pluginScrapeCleanJournalEntry, []byte, error) {
	state := scrapehost.Durable{}
	anchor, _, anchorErr := j.root(ctx, db)
	if anchorErr != nil {
		return state, nil, nil, anchorErr
	}
	scrapeJournalInitialize(&state)
	entries := map[string]pluginScrapeCleanJournalEntry{}
	var version int
	var digest, sealed []byte
	err := db.QueryRowContext(ctx, `SELECT version,digest,sealed FROM plugin_scrape_journal_meta WHERE instance_id=? AND generation=?`, j.instance, j.generation).Scan(&version, &digest, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_scrape_journal_rows WHERE instance_id=? AND generation=?`, j.instance, j.generation).Scan(&count); err != nil {
			return state, nil, nil, err
		}
		if count != 0 || len(anchor.State) != 0 {
			return state, nil, nil, scrapeJournalCorrupt()
		}
		return state, entries, nil, nil
	}
	if err != nil {
		return state, nil, nil, err
	}
	if anchor.Sequence < 1 || !hmac.Equal(anchor.State, digest) {
		return state, nil, nil, scrapeJournalCorrupt()
	}
	if version != pluginScrapeCleanJournalVersion {
		return state, nil, nil, scrapeJournalCorrupt()
	}
	raw, err := j.p.unseal("scrape_journal", j.instance, j.aad("meta", "main"), sealed)
	if err != nil {
		return state, nil, nil, err
	}
	var meta pluginScrapeCleanJournalMeta
	if err = scrapeJournalStrict(raw, &meta); err != nil {
		return state, nil, nil, err
	}
	canonical, err := scrapeJournalJSON(meta)
	if err != nil || !hmac.Equal(digest, j.mac("value:meta", canonical)) {
		return state, nil, nil, scrapeJournalCorrupt()
	}
	if meta.Version != version || meta.Instance != j.instance || meta.Generation != j.generation {
		return state, nil, nil, scrapeJournalScope()
	}
	if len(meta.Collections) != len(pluginScrapeCleanJournalCollections) {
		return state, nil, nil, scrapeJournalCorrupt()
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
	if len(meta.TimerDue) == 0 || scrapeJournalStrict(meta.TimerDue, &state.Machine.TimerDue) != nil {
		return state, nil, nil, scrapeJournalCorrupt()
	}
	rows, err := db.QueryContext(ctx, `SELECT collection,row_key,ordinal,digest,sealed FROM plugin_scrape_journal_rows WHERE instance_id=? AND generation=? ORDER BY collection,ordinal,row_key`, j.instance, j.generation)
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
		isMap, known := scrapeJournalCollection(collection)
		if !known {
			rows.Close()
			return state, nil, nil, scrapeJournalCorrupt()
		}
		rowRaw, e := j.p.unseal("scrape_journal", j.instance, j.aad(collection, token), rowSealed)
		if e != nil {
			rows.Close()
			return state, nil, nil, e
		}
		var row pluginScrapeCleanJournalRow
		if e = scrapeJournalStrict(rowRaw, &row); e != nil {
			rows.Close()
			return state, nil, nil, e
		}
		encoded, e := scrapeJournalJSON(row)
		if e != nil || !hmac.Equal(rowDigest, j.mac("value:"+collection, encoded)) {
			rows.Close()
			return state, nil, nil, scrapeJournalCorrupt()
		}
		if row.Version != version || row.Instance != j.instance || row.Generation != j.generation || row.Collection != collection || row.Token != token || row.Ordinal != ordinal || isMap != (row.Key != nil) || (isMap && ordinal != -1) || (!isMap && ordinal < 0) || token != j.rowToken(collection, row.Key, ordinal) {
			rows.Close()
			return state, nil, nil, scrapeJournalCorrupt()
		}
		if row.Key != nil && !utf8.ValidString(*row.Key) {
			rows.Close()
			return state, nil, nil, scrapeJournalCorrupt()
		}
		if err = j.decodeRow(&state, row); err != nil {
			rows.Close()
			return state, nil, nil, err
		}
		key := scrapeJournalEntryKey(collection, token)
		entries[key] = pluginScrapeCleanJournalEntry{collection, token, ordinal, append([]byte(nil), rowDigest...)}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return state, nil, nil, err
	}
	if err = rows.Close(); err != nil {
		return state, nil, nil, err
	}
	actual := j.stats(entries)
	for _, collection := range pluginScrapeCleanJournalCollections {
		want, exists := meta.Collections[collection]
		got := actual[collection]
		if !exists || want.Count != got.Count || !hmac.Equal(want.Digest, got.Digest) {
			return state, nil, nil, scrapeJournalCorrupt()
		}
	}
	if err = j.validate(&state); err != nil {
		return state, nil, nil, err
	}
	return state, entries, append([]byte(nil), digest...), nil
}
func (j *pluginScrapeCleanJournal) decodeRow(state *scrapehost.Durable, row pluginScrapeCleanJournalRow) error {
	switch row.Collection {
	case "roots":
		if row.Ordinal != int64(len(state.Roots)) {
			return scrapeJournalCorrupt()
		}
		var v scrapehost.RootStamp
		if err := scrapeJournalStrict(row.Value, &v); err != nil {
			return err
		}
		state.Roots = append(state.Roots, v)
	case "state":
		if row.Ordinal != int64(len(state.Machine.State)) {
			return scrapeJournalCorrupt()
		}
		var v scrapefileclean.Tracked
		if err := scrapeJournalStrict(row.Value, &v); err != nil {
			return err
		}
		state.Machine.State = append(state.Machine.State, v)
	case "queue":
		if row.Ordinal != int64(len(state.Machine.Queue)) {
			return scrapeJournalCorrupt()
		}
		var v *scrapefileclean.Task
		if err := scrapeJournalStrict(row.Value, &v); err != nil {
			return err
		}
		state.Machine.Queue = append(state.Machine.Queue, v)
	case "watches":
		if row.Ordinal != int64(len(state.Machine.Watches)) {
			return scrapeJournalCorrupt()
		}
		var v string
		if err := scrapeJournalStrict(row.Value, &v); err != nil {
			return err
		}
		state.Machine.Watches = append(state.Machine.Watches, v)
	case "pins":
		var v scrapehost.Pin
		if err := scrapeJournalStrict(row.Value, &v); err != nil {
			return err
		}
		if _, exists := state.Pins[*row.Key]; exists {
			return scrapeJournalCorrupt()
		}
		state.Pins[*row.Key] = v
	case "frozen":
		var v scrapehost.Pin
		if err := scrapeJournalStrict(row.Value, &v); err != nil {
			return err
		}
		if _, exists := state.Frozen[*row.Key]; exists {
			return scrapeJournalCorrupt()
		}
		state.Frozen[*row.Key] = v
	case "events":
		var v scrapehost.EventReceipt
		if err := scrapeJournalStrict(row.Value, &v); err != nil {
			return err
		}
		if _, exists := state.Events[*row.Key]; exists {
			return scrapeJournalCorrupt()
		}
		state.Events[*row.Key] = v
	case "effects":
		var v pluginScrapeCleanJournalEffect
		if err := scrapeJournalStrict(row.Value, &v); err != nil {
			return err
		}
		// The embedded Effect is always written with Payload=nil, whose JSON
		// representation is null. RawMessage decodes that literal as bytes,
		// not a nil slice; the actual payload belongs only to the opaque slot.
		if !bytes.Equal(v.Effect.Payload, []byte("null")) {
			return scrapeJournalCorrupt()
		}
		v.Effect.Payload = append(json.RawMessage(nil), v.Payload...)
		if v.Payload != nil && len(v.Payload) == 0 {
			v.Effect.Payload = json.RawMessage{}
		}
		if _, exists := state.Effects[*row.Key]; exists {
			return scrapeJournalCorrupt()
		}
		state.Effects[*row.Key] = v.Effect
	default:
		return scrapeJournalCorrupt()
	}
	return nil
}
func (j *pluginScrapeCleanJournal) Update(ctx context.Context, fn func(*scrapehost.Durable) error) error {
	if fn == nil {
		return bad("INVALID_SCRAPE_CLEAN_JOURNAL_UPDATE", "清理状态更新函数无效")
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
func (j *pluginScrapeCleanJournal) updateTx(ctx context.Context, tx *sql.Tx, fn func(*scrapehost.Durable) error) error {
	if tx == nil || fn == nil {
		return bad("INVALID_SCRAPE_CLEAN_JOURNAL_UPDATE", "清理状态更新事务或函数无效")
	}
	anchor, rootDigest, err := j.root(ctx, tx)
	if err != nil {
		return err
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
	scrapeJournalInitialize(&state)
	if err = j.validate(&state); err != nil {
		return err
	}
	entries := map[string]pluginScrapeCleanJournalEntry{}
	syncRow := func(collection string, key *string, ordinal int64, value any) error {
		if key != nil && !utf8.ValidString(*key) {
			return scrapeJournalCorrupt()
		}
		valueRaw, e := json.Marshal(value)
		if e != nil {
			return e
		}
		token := j.rowToken(collection, key, ordinal)
		row := pluginScrapeCleanJournalRow{pluginScrapeCleanJournalVersion, j.instance, j.generation, collection, token, ordinal, key, valueRaw}
		raw, e := scrapeJournalJSON(row)
		if e != nil {
			return e
		}
		if len(raw) > pluginDataValueLimit {
			return bad("SCRAPE_CLEAN_JOURNAL_ROW_TOO_LARGE", "清理状态单项超过大小限制")
		}
		digest := j.mac("value:"+collection, raw)
		entryKey := scrapeJournalEntryKey(collection, token)
		previous, exists := old[entryKey]
		entries[entryKey] = pluginScrapeCleanJournalEntry{collection, token, ordinal, digest}
		delete(old, entryKey)
		if exists && previous.Ordinal == ordinal && hmac.Equal(previous.Digest, digest) {
			return nil
		}
		sealed, e := j.p.seal("scrape_journal", j.instance, j.aad(collection, token), raw)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO plugin_scrape_journal_rows(instance_id,generation,collection,row_key,ordinal,digest,sealed,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,generation,collection,row_key) DO UPDATE SET ordinal=excluded.ordinal,digest=excluded.digest,sealed=excluded.sealed,updated_at=excluded.updated_at`, j.instance, j.generation, collection, token, ordinal, digest, sealed, now())
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
		case map[string]scrapehost.Pin:
			for key := range values {
				out = append(out, key)
			}
		case map[string]scrapehost.EventReceipt:
			for key := range values {
				out = append(out, key)
			}
		case map[string]scrapehost.Effect:
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
		if err = syncRow("effects", &key, -1, pluginScrapeCleanJournalEffect{value, payload}); err != nil {
			return err
		}
	}
	for _, entry := range old {
		if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_scrape_journal_rows WHERE instance_id=? AND generation=? AND collection=? AND row_key=?`, j.instance, j.generation, entry.Collection, entry.Token); err != nil {
			return err
		}
	}
	timer, err := json.Marshal(state.Machine.TimerDue)
	if err != nil {
		return err
	}
	meta := pluginScrapeCleanJournalMeta{
		Version: pluginScrapeCleanJournalVersion, Instance: j.instance, Generation: j.generation,
		Binding: state.Binding, Config: []byte(state.Config), TimerDue: timer,
		Serial: state.Serial, EventSerial: state.EventSerial, Active: state.Active,
		Blocked: state.Blocked, Collections: j.stats(entries), Transition: state.Transition,
	}
	raw, err := scrapeJournalJSON(meta)
	if err != nil {
		return err
	}
	if len(raw) > pluginDataValueLimit {
		return bad("SCRAPE_CLEAN_JOURNAL_ROW_TOO_LARGE", "清理状态单项超过大小限制")
	}
	digest := j.mac("value:meta", raw)
	if !hmac.Equal(digest, oldMetaDigest) {
		sealed, e := j.p.seal("scrape_journal", j.instance, j.aad("meta", "main"), raw)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_scrape_journal_meta(instance_id,generation,version,digest,sealed,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(instance_id,generation) DO UPDATE SET version=excluded.version,digest=excluded.digest,sealed=excluded.sealed,updated_at=excluded.updated_at`, j.instance, j.generation, pluginScrapeCleanJournalVersion, digest, sealed, now()); err != nil {
			return err
		}
	}
	if !hmac.Equal(anchor.State, digest) {
		anchor.Sequence++
		anchor.Previous = rootDigest
		anchor.State = append([]byte(nil), digest...)
		if err = j.saveRootTx(ctx, tx, anchor, rootDigest); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return nil
}
