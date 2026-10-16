package app

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"strconv"
	"unicode/utf8"

	signinjournal "light-media/internal/plugins/autosignin/durable"
)

// The optional read backend retains the actual SQL owner/run/config guard.
// It authenticates the complete small row directory, then only requested cells.
// Unrequested reply bodies are verified lazily when read, never treated as empty.
// No partial State is sent to Update, and every mutation remains on the original
// complete-state backend. This is not a worker RPC or plugin registration.
type pluginAutoSignInTypedBackend struct{ *pluginAutoSignInBackend }
type pluginAutoSignInTypedReader struct{ b *pluginAutoSignInBackend }
type pluginAutoSignInTypedCellRef struct{ Collection, Key string }
type pluginAutoSignInTypedObservation struct {
	Exists bool
	Data   []byte
}
type pluginAutoSignInTypedCost struct{ DirectoryRows, JournalDecryptedRows, JournalDecryptedBytes int }
type pluginAutoSignInTypedProjection struct {
	reader    *pluginAutoSignInTypedReader
	binding   signinjournal.Binding
	meta      pluginAutoSignInMeta
	digest    []byte
	directory map[string]pluginAutoSignInPhysical
	mask      map[pluginAutoSignInTypedCellRef]pluginAutoSignInTypedObservation
	cost      pluginAutoSignInTypedCost
}

var _ signinjournal.StateBackend = (*pluginAutoSignInTypedBackend)(nil)
var _ signinjournal.EntryReader = (*pluginAutoSignInTypedBackend)(nil)
var _ signinjournal.CacheReader = (*pluginAutoSignInTypedBackend)(nil)
var _ signinjournal.RunStartReader = (*pluginAutoSignInTypedBackend)(nil)

func newPluginAutoSignInTypedJournal(b *pluginAutoSignInBackend) (*signinjournal.StateJournal, error) {
	if b == nil || b.owner == nil {
		return nil, signinjournal.ErrStale
	}
	return signinjournal.NewStateJournal(&pluginAutoSignInTypedBackend{b})
}
func signinTypedClone(raw []byte) []byte {
	if raw == nil {
		return nil
	}
	return append([]byte{}, raw...)
}

// StateJournal.State historically JSON-round-trips the full State. Match the
// resulting RawMessage nil/null, whitespace and HTML escaping only in returned
// DTOs. Storage bytes and config-receipt authorization must remain unchanged.
func signinTypedJSONValue(raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := json.Marshal(raw)
	if err != nil {
		return nil, signinStoreError()
	}
	return normalized, nil
}
func signinTypedRef(collection, key string) pluginAutoSignInTypedCellRef {
	return pluginAutoSignInTypedCellRef{collection, key}
}
func (b *pluginAutoSignInTypedBackend) reader(binding signinjournal.Binding) (*pluginAutoSignInTypedReader, error) {
	if b == nil || b.pluginAutoSignInBackend == nil || b.owner == nil || binding != b.owner.binding {
		return nil, signinjournal.ErrStale
	}
	return &pluginAutoSignInTypedReader{b.pluginAutoSignInBackend}, nil
}
func (r *pluginAutoSignInTypedReader) read(ctx context.Context, binding signinjournal.Binding, fn func(*sql.Tx, *pluginAutoSignInTypedProjection) error) (pluginAutoSignInTypedCost, error) {
	release, err := r.b.retain(ctx, binding)
	if err != nil {
		return pluginAutoSignInTypedCost{}, err
	}
	defer release()
	o := r.b.owner
	o.a.pluginMu.Lock()
	defer o.a.pluginMu.Unlock()
	tx, err := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return pluginAutoSignInTypedCost{}, err
	}
	defer tx.Rollback()
	p, err := r.directoryTx(ctx, tx)
	if err != nil {
		return pluginAutoSignInTypedCost{}, err
	}
	if err = p.authorizeTx(ctx, tx); err != nil {
		return p.cost, err
	}
	err = fn(tx, p)
	return p.cost, err
}
func (b *pluginAutoSignInTypedBackend) ReadEntry(ctx context.Context, binding signinjournal.Binding, run, lane string, index int) (signinjournal.Entry, bool, error) {
	r, err := b.reader(binding)
	if err != nil {
		return signinjournal.Entry{}, false, err
	}
	var out signinjournal.Entry
	var exists bool
	_, err = r.read(ctx, binding, func(tx *sql.Tx, p *pluginAutoSignInTypedProjection) error {
		var e error
		out, exists, e = p.entryTx(ctx, tx, run, lane, index)
		return e
	})
	if err == nil && exists {
		out.Value, err = signinTypedJSONValue(out.Value)
	}
	return out, exists, err
}
func (b *pluginAutoSignInTypedBackend) ReadCache(ctx context.Context, binding signinjournal.Binding, key string) (signinjournal.CacheValue, error) {
	r, err := b.reader(binding)
	if err != nil {
		return signinjournal.CacheValue{}, err
	}
	var out signinjournal.CacheValue
	_, err = r.read(ctx, binding, func(tx *sql.Tx, p *pluginAutoSignInTypedProjection) error {
		cell, e := p.cellTx(ctx, tx, signinTypedRef("cache", key))
		out = signinjournal.CacheValue{Value: signinTypedClone(cell.Data), KeyExists: cell.Exists, Found: cell.Exists && cell.Data != nil}
		return e
	})
	return out, err
}

func (r *pluginAutoSignInTypedReader) directoryTx(ctx context.Context, tx *sql.Tx) (*pluginAutoSignInTypedProjection, error) {
	o := r.b.owner
	var sealed, digest []byte
	if err := tx.QueryRowContext(ctx, `SELECT sealed,digest FROM plugin_signin_meta WHERE instance_id=? AND generation=?`, o.binding.Instance, o.binding.Generation).Scan(&sealed, &digest); err != nil {
		return nil, err
	}
	raw, err := o.a.pluginStore.unseal("signin_journal", o.binding.Instance, o.aad("meta", "", 0), sealed)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(o.mac("meta", raw), digest) {
		return nil, signinStoreError()
	}
	p := &pluginAutoSignInTypedProjection{reader: r, binding: o.binding, digest: signinTypedClone(digest), directory: map[string]pluginAutoSignInPhysical{}, mask: map[pluginAutoSignInTypedCellRef]pluginAutoSignInTypedObservation{}}
	if cleanupJournalStrict(raw, &p.meta) != nil || p.meta.Version != 1 || p.meta.Scope != o.binding.Scope || p.meta.Epoch != o.binding.Epoch || p.meta.Count < 0 || p.meta.Count > pluginAutoSignInRowLimit || p.meta.Bytes < 0 || p.meta.Bytes > pluginAutoSignInCapacity {
		return nil, signinStoreError()
	}
	rows, err := tx.QueryContext(ctx, `SELECT collection,token,part,digest,size FROM plugin_signin_rows WHERE instance_id=? AND generation=? ORDER BY collection,token,part`, o.binding.Instance, o.binding.Generation)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x pluginAutoSignInPhysical
		if err = rows.Scan(&x.Collection, &x.Token, &x.Part, &x.Digest, &x.Size); err != nil {
			break
		}
		if len(p.directory) >= pluginAutoSignInRowLimit || x.Part < 0 || x.Size < 1 || x.Size > pluginValueLimit {
			err = signinStoreError()
			break
		}
		p.directory[signinPhysicalKey(x)] = x
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	sum, size := o.aggregate(p.directory)
	if len(p.directory) != p.meta.Count || size != p.meta.Bytes || !hmac.Equal(sum, p.meta.Digest) {
		return nil, signinStoreError()
	}
	p.cost.DirectoryRows = len(p.directory)
	return p, nil
}

func (p *pluginAutoSignInTypedProjection) cellTx(ctx context.Context, tx *sql.Tx, key pluginAutoSignInTypedCellRef) (pluginAutoSignInTypedObservation, error) {
	if old, ok := p.mask[key]; ok {
		return old, nil
	}
	if !utf8.ValidString(key.Collection) || !utf8.ValidString(key.Key) {
		return pluginAutoSignInTypedObservation{}, signinStoreError()
	}
	o := p.reader.b.owner
	token := o.token(key.Collection, key.Key)
	firstKey := signinPhysicalKey(pluginAutoSignInPhysical{Collection: key.Collection, Token: token, Part: 0})
	if _, ok := p.directory[firstKey]; !ok {
		// A forged nonzero first part must not be treated as verified absence.
		for _, part := range p.directory {
			if part.Collection == key.Collection && part.Token == token {
				return pluginAutoSignInTypedObservation{}, signinStoreError()
			}
		}
		absent := pluginAutoSignInTypedObservation{}
		p.mask[key] = absent
		return absent, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT part,sealed FROM plugin_signin_rows WHERE instance_id=? AND generation=? AND collection=? AND token=? ORDER BY part`, o.binding.Instance, o.binding.Generation, key.Collection, token)
	if err != nil {
		return pluginAutoSignInTypedObservation{}, err
	}
	var out pluginAutoSignInTypedObservation
	out.Exists = true
	parts, expected, nilFlag := 0, 0, false
	for rows.Next() {
		var i int
		var sealed []byte
		if err = rows.Scan(&i, &sealed); err != nil {
			break
		}
		info, ok := p.directory[signinPhysicalKey(pluginAutoSignInPhysical{Collection: key.Collection, Token: token, Part: i})]
		if !ok || i != parts || len(sealed) > pluginValueLimit+64 {
			err = signinStoreError()
			break
		}
		raw, e := o.a.pluginStore.unseal("signin_journal", o.binding.Instance, o.aad(key.Collection, token, i), sealed)
		if e != nil {
			err = e
			break
		}
		p.cost.JournalDecryptedRows++
		p.cost.JournalDecryptedBytes += len(raw)
		if len(raw) != info.Size || !hmac.Equal(o.mac("row", raw), info.Digest) {
			err = signinStoreError()
			break
		}
		var row pluginAutoSignInRow
		if cleanupJournalStrict(raw, &row) != nil || row.Version != 1 || row.Instance != o.binding.Instance || row.Generation != o.binding.Generation || row.Collection != key.Collection || row.Token != token || row.Key != key.Key || row.Part != i || row.Parts < 1 || row.Parts > pluginAutoSignInRowLimit || len(row.Data) > pluginAutoSignInChunk {
			err = signinStoreError()
			break
		}
		if parts == 0 {
			expected = row.Parts
			nilFlag = row.Nil
			if !nilFlag {
				out.Data = []byte{}
			}
		}
		if row.Parts != expected || row.Nil != nilFlag || nilFlag && (expected != 1 || len(row.Data) != 0) || parts < expected-1 && len(row.Data) != pluginAutoSignInChunk {
			err = signinStoreError()
			break
		}
		out.Data = append(out.Data, row.Data...)
		parts++
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return pluginAutoSignInTypedObservation{}, err
	}
	if parts != expected {
		return pluginAutoSignInTypedObservation{}, signinStoreError()
	}
	// All physical rows for this cell must participate; no omitted tail chunks.
	count := 0
	for _, part := range p.directory {
		if part.Collection == key.Collection && part.Token == token {
			count++
		}
	}
	if count != parts {
		return pluginAutoSignInTypedObservation{}, signinStoreError()
	}
	p.mask[key] = out
	return out, nil
}

func (p *pluginAutoSignInTypedProjection) objectTx(ctx context.Context, tx *sql.Tx, key pluginAutoSignInTypedCellRef, out any) error {
	v, err := p.cellTx(ctx, tx, key)
	if err != nil {
		return err
	}
	if !v.Exists {
		return signinjournal.ErrConflict
	}
	return cleanupJournalStrict(v.Data, out)
}

func (p *pluginAutoSignInTypedProjection) authorizeTx(ctx context.Context, tx *sql.Tx) error {
	r := p.reader
	o := r.b.owner
	var header pluginAutoSignInHeader
	if err := p.objectTx(ctx, tx, signinTypedRef("head", ""), &header); err != nil {
		return err
	}
	if header.Schema != 1 || header.Scope != p.meta.Scope || header.Epoch != p.meta.Epoch || header.ActiveRun != p.meta.ActiveRun || header.RuntimeEpoch != p.meta.RuntimeEpoch {
		return signinStoreError()
	}
	config, err := p.cellTx(ctx, tx, signinTypedRef("config", ""))
	if err != nil || !config.Exists {
		return signinStoreError()
	}
	auth := signinjournal.State{Scope: o.binding.Scope, Epoch: o.binding.Epoch, Config: config.Data, Runs: map[string]*signinjournal.Run{}}
	rows, err := tx.QueryContext(ctx, `SELECT to_revision,sealed FROM plugin_signin_config_receipts WHERE instance_id=? AND generation=? ORDER BY to_revision`, o.binding.Instance, o.binding.Generation)
	if err != nil {
		return err
	}
	proofs := []pluginAutoSignInConfigReceipt{}
	for rows.Next() {
		var revision uint64
		var sealed []byte
		if err = rows.Scan(&revision, &sealed); err != nil {
			break
		}
		if len(proofs) >= pluginAutoSignInRowLimit {
			err = signinjournal.ErrCapacity
			break
		}
		raw, e := o.a.pluginStore.unseal("signin_config", o.binding.Instance, o.aad("config", strconv.FormatUint(revision, 10), 0), sealed)
		if e != nil {
			err = e
			break
		}
		var proof pluginAutoSignInConfigReceipt
		if cleanupJournalStrict(raw, &proof) != nil || proof.To != revision || proof.Index < 0 || proof.Index >= pluginAutoSignInRowLimit {
			err = signinStoreError()
			break
		}
		proofs = append(proofs, proof)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	allocated := 0
	for _, proof := range proofs {
		var head pluginAutoSignInRunHead
		if err = p.objectTx(ctx, tx, signinTypedRef("run.head", signinKey(proof.RunID)), &head); err != nil {
			return err
		}
		if head.ID != proof.RunID {
			return signinStoreError()
		}
		run := auth.Runs[proof.RunID]
		if run == nil {
			run = &signinjournal.Run{ID: head.ID, Scope: head.Scope, Lanes: map[string][]signinjournal.Entry{}}
			auth.Runs[proof.RunID] = run
		}
		var n int
		if err = p.objectTx(ctx, tx, signinTypedRef("lane", signinKey(proof.RunID, proof.Lane)), &n); err != nil {
			return err
		}
		if n <= proof.Index || n > pluginAutoSignInRowLimit {
			return signinStoreError()
		}
		if len(run.Lanes[proof.Lane]) <= proof.Index {
			extra := proof.Index + 1 - len(run.Lanes[proof.Lane])
			allocated += extra
			if allocated > p.meta.Count {
				return signinStoreError()
			}
			run.Lanes[proof.Lane] = append(run.Lanes[proof.Lane], make([]signinjournal.Entry, extra)...)
		}
		entry, exists, e := p.entryTx(ctx, tx, proof.RunID, proof.Lane, proof.Index)
		if e != nil {
			return e
		}
		if !exists {
			return signinStoreError()
		}
		run.Lanes[proof.Lane][proof.Index] = entry
	}
	return r.b.guardTx(ctx, tx, o.binding, auth)
}

func (p *pluginAutoSignInTypedProjection) entryTx(ctx context.Context, tx *sql.Tx, run, lane string, index int) (signinjournal.Entry, bool, error) {
	var out signinjournal.Entry
	if index < 0 {
		return out, false, signinjournal.ErrConflict
	}
	var h pluginAutoSignInRunHead
	if err := p.objectTx(ctx, tx, signinTypedRef("run.head", signinKey(run)), &h); err != nil {
		return out, false, err
	}
	if h.ID != run || h.Scope != p.binding.Scope {
		return out, false, signinjournal.ErrStale
	}
	var length int
	laneCell, err := p.cellTx(ctx, tx, signinTypedRef("lane", signinKey(run, lane)))
	if err != nil {
		return out, false, err
	}
	if laneCell.Exists {
		if cleanupJournalStrict(laneCell.Data, &length) != nil || length < 0 || length > pluginAutoSignInRowLimit {
			return out, false, signinStoreError()
		}
	}
	if index > length {
		return out, false, signinjournal.ErrConflict
	}
	if index == length {
		return out, false, nil
	}
	key := signinKey(run, lane, strconv.Itoa(index))
	var head pluginAutoSignInEntryHead
	if err = p.objectTx(ctx, tx, signinTypedRef("entry.head", key), &head); err != nil {
		return out, false, err
	}
	value, err := p.cellTx(ctx, tx, signinTypedRef("entry.value", key))
	if err != nil {
		return out, false, err
	}
	if !value.Exists {
		return out, false, signinStoreError()
	}
	out = signinjournal.Entry{Kind: head.Kind, Digest: head.Digest, Phase: head.Phase, Error: head.Error, ErrorClass: head.ErrorClass, Guard: head.Guard, Value: signinTypedClone(value.Data)}
	return out, true, nil
}

func (b *pluginAutoSignInTypedBackend) ReadRunStartHead(ctx context.Context, binding signinjournal.Binding, id string) (signinjournal.RunStartHead, error) {
	r, err := b.reader(binding)
	if err != nil {
		return signinjournal.RunStartHead{}, err
	}
	var out signinjournal.RunStartHead
	_, err = r.read(ctx, binding, func(tx *sql.Tx, p *pluginAutoSignInTypedProjection) error {
		out.RuntimeEpoch, out.Scope = p.meta.RuntimeEpoch, p.binding.Scope
		cell, e := p.cellTx(ctx, tx, signinTypedRef("run.head", signinKey(id)))
		if e != nil {
			return e
		}
		if !cell.Exists {
			for _, pair := range []struct {
				name string
				dst  *json.RawMessage
			}{{"runtime", &out.Baseline.Runtime}, {"config", &out.Baseline.Config}} {
				v, e := p.cellTx(ctx, tx, signinTypedRef(pair.name, ""))
				if e != nil || !v.Exists {
					return signinStoreError()
				}
				*pair.dst = signinTypedClone(v.Data)
			}
			if e = p.objectTx(ctx, tx, signinTypedRef("keys", ""), &out.Baseline.Keys); e != nil {
				return e
			}
			out.Baseline.Keys = append([]string{}, out.Baseline.Keys...)
			keys, e := p.collectionKeysTx(ctx, tx, "history")
			if e != nil {
				return e
			}
			history := map[string]json.RawMessage{}
			for _, key := range keys {
				v, e := p.cellTx(ctx, tx, signinTypedRef("history", key))
				if e != nil || !v.Exists {
					return signinStoreError()
				}
				history[key] = signinTypedClone(v.Data)
			}
			out.Baseline.History, e = signinJSON(history)
			return e
		}
		var head pluginAutoSignInRunHead
		if cleanupJournalStrict(cell.Data, &head) != nil || head.ID != id || head.Scope != binding.Scope {
			return signinStoreError()
		}
		out.Exists, out.ID, out.Action, out.Epoch, out.Phase = true, head.ID, head.Action, head.Epoch, head.Phase
		out.Scope, out.Baseline.Timezone = head.Scope, head.Timezone
		for _, pair := range []struct {
			key pluginAutoSignInTypedCellRef
			dst *json.RawMessage
		}{{signinTypedRef("run.plan", signinKey(id)), &out.Plan}, {signinTypedRef("run.baseline", signinKey(id, "runtime")), &out.Baseline.Runtime}, {signinTypedRef("run.baseline", signinKey(id, "history")), &out.Baseline.History}, {signinTypedRef("run.baseline", signinKey(id, "inventory")), &out.Baseline.Inventory}, {signinTypedRef("run.baseline", signinKey(id, "config")), &out.Baseline.Config}, {signinTypedRef("run.final", signinKey(id, "runtime")), &out.FinalRuntime}, {signinTypedRef("run.final", signinKey(id, "result")), &out.Result}} {
			v, e := p.cellTx(ctx, tx, pair.key)
			if e != nil || !v.Exists {
				return signinStoreError()
			}
			*pair.dst = signinTypedClone(v.Data)
		}
		return p.objectTx(ctx, tx, signinTypedRef("run.keys", signinKey(id, "keys")), &out.Baseline.Keys)
	})
	if err != nil {
		return out, err
	}
	fields := []*json.RawMessage{&out.Baseline.Runtime, &out.Baseline.History, &out.Baseline.Config}
	if out.Exists {
		fields = append(fields, &out.Plan, &out.Baseline.Inventory, &out.FinalRuntime, &out.Result)
	}
	for _, field := range fields {
		*field, err = signinTypedJSONValue(*field)
		if err != nil {
			return out, err
		}
	}
	return out, err
}

func (p *pluginAutoSignInTypedProjection) collectionKeysTx(ctx context.Context, tx *sql.Tx, collection string) ([]string, error) {
	o := p.reader.b.owner
	tokens := map[string]bool{}
	for _, row := range p.directory {
		if row.Collection == collection {
			tokens[row.Token] = tokens[row.Token] || row.Part == 0
		}
	}
	keys := make([]string, 0, len(tokens))
	seen := map[string]bool{}
	for token, hasFirst := range tokens {
		if !hasFirst {
			return nil, signinStoreError()
		}
		var sealed []byte
		if err := tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_signin_rows WHERE instance_id=? AND generation=? AND collection=? AND token=? AND part=0`, o.binding.Instance, o.binding.Generation, collection, token).Scan(&sealed); err != nil {
			return nil, err
		}
		raw, err := o.a.pluginStore.unseal("signin_journal", o.binding.Instance, o.aad(collection, token, 0), sealed)
		if err != nil {
			return nil, err
		}
		p.cost.JournalDecryptedRows++
		p.cost.JournalDecryptedBytes += len(raw)
		physical := p.directory[signinPhysicalKey(pluginAutoSignInPhysical{Collection: collection, Token: token})]
		var row pluginAutoSignInRow
		if len(raw) != physical.Size || !hmac.Equal(o.mac("row", raw), physical.Digest) || cleanupJournalStrict(raw, &row) != nil || row.Collection != collection || row.Token != token || row.Part != 0 || o.token(collection, row.Key) != token || seen[row.Key] {
			return nil, signinStoreError()
		}
		seen[row.Key] = true
		keys = append(keys, row.Key)
	}
	return keys, nil
}
