package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	durable "light-media/internal/plugins/autosignin/durable"
)

// Acceptance and terminal source return are separate immutable facts. Neither
// a mutable route checkpoint nor a delivered outbox item proves source return.
const pluginAutoSignInSourceSchema = `CREATE TABLE IF NOT EXISTS plugin_signin_source_acceptances(token TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_signin_source_results(token TEXT PRIMARY KEY,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_signin_source_events(seq INTEGER PRIMARY KEY AUTOINCREMENT,token TEXT NOT NULL UNIQUE,sealed BLOB NOT NULL);`

type pluginAutoSignInSourcePort struct{ b *pluginAutoSignInBackend }

func newAutoSignInSourcePort(b *pluginAutoSignInBackend) durable.SourcePort {
	return &pluginAutoSignInSourcePort{b: b}
}

type autoSignInSourceAcceptance struct {
	ConstructionErrorClass                          string `json:",omitempty"`
	EventID                                         string `json:",omitempty"`
	Version                                         int
	Request                                         durable.SourceRequest
	Token, Domain, CreatedAt, CurrentTime, Timezone string
	TimezoneOffset                                  int
	Envelope                                        json.RawMessage
	RouteID, RouteInitialSHA                        string
	HistoryID, HistoryDigest                        string
}
type autoSignInSourceProof struct {
	Failure                                 *removeLinkSourceFailure `json:",omitempty"`
	Result                                  durable.SourceResult
	AcceptanceSHA, RouteInitialSHA, LastSHA string
	Count                                   int
}

func (s *pluginAutoSignInSourcePort) transaction(ctx context.Context, binding durable.Binding, fn func(*sql.Tx, durable.State) error) error {
	if s == nil || s.b == nil || s.b.owner == nil || s.b.run == nil {
		return durable.ErrStale
	}
	release, err := s.b.retain(ctx, binding)
	if err != nil {
		return err
	}
	defer release()
	s.b.mu.Lock()
	held := s.b.held > 0
	s.b.mu.Unlock()
	if !held {
		return durable.ErrStale
	}
	a := s.b.owner.a
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	loaded, err := s.b.owner.readTx(ctx, tx)
	if err != nil {
		return err
	}
	if err = s.b.guardTx(ctx, tx, binding, loaded.State); err != nil {
		return err
	}
	if err = fn(tx, loaded.State); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *pluginAutoSignInSourcePort) configTx(ctx context.Context, tx *sql.Tx, state durable.State) (durable.SourceConfigIdentity, error) {
	var revision uint64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, s.b.owner.binding.Instance).Scan(&revision); err != nil {
		return durable.SourceConfigIdentity{}, err
	}
	return durable.SourceConfigIdentity{Revision: revision, Digest: hex.EncodeToString(s.b.owner.mac("config-value", state.Config))}, nil
}
func (s *pluginAutoSignInSourcePort) Bind(ctx context.Context, binding durable.Binding, runID string) (out durable.SourceConfigIdentity, err error) {
	err = s.transaction(ctx, binding, func(tx *sql.Tx, state durable.State) error {
		r := state.Runs[runID]
		if r == nil || runID != s.b.run.ID || r.Scope != binding.Scope || r.Epoch != binding.Epoch || state.ActiveRun != runID {
			return durable.ErrStale
		}
		var e error
		out, e = s.configTx(ctx, tx, state)
		return e
	})
	return
}
func (s *pluginAutoSignInSourcePort) validateTx(ctx context.Context, tx *sql.Tx, state durable.State, id durable.SourceIdentity, invoke bool) error {
	b := s.b.owner.binding
	r := state.Runs[id.RunID]
	if id.Version != 1 || r == nil || id.RunID != s.b.run.ID || r.Scope != id.Scope || r.Epoch != id.Epoch || id.Scope.Instance != b.Instance || id.Scope.Generation != b.Generation || id.Index < 0 || id.Index >= len(r.Lanes[id.Lane]) || (id.Kind != "source.notify" && id.Kind != "source.emit") {
		return durable.ErrStale
	}
	if invoke && (id.Scope != b.Scope || id.Epoch != b.Epoch || state.ActiveRun != id.RunID) {
		return durable.ErrStale
	}
	e := r.Lanes[id.Lane][id.Index]
	if invoke && e.Phase != "intent" {
		return durable.ErrUncertain
	}
	if e.Kind != id.Kind || e.Digest != id.PayloadDigest || e.Guard != autoSignInSourceGuard(id) {
		return durable.ErrConflict
	}
	if e.Phase != "intent" && e.Phase != "receipt" && e.Phase != "uncertain" {
		return durable.ErrConflict
	}
	var env durable.SourceEnvelope
	if cleanupJournalStrict(e.Value, &env) != nil || env.Version != 1 || env.Request.Identity != id {
		return durable.ErrConflict
	}
	if (e.Phase == "receipt" && (env.Result == nil || durable.ValidateSourceResult(env.Request, *env.Result) != nil)) || (e.Phase != "receipt" && env.Result != nil) {
		return durable.ErrConflict
	}
	if durable.ValidateSourceRequest(env.Request) != nil {
		return durable.ErrConflict
	}
	if invoke {
		config, err := s.configTx(ctx, tx, state)
		if err != nil {
			return err
		}
		if config != id.Config {
			return durable.ErrStale
		}
	}
	return nil
}
func (p *PluginStore) autoSignInSourceToken(id durable.SourceIdentity) string {
	return p.noticeID("auto-signin-source", id)
}
func (p *PluginStore) readAutoSignInAcceptance(ctx context.Context, q pluginRemoveLinkJournalReader, id durable.SourceIdentity) (box autoSignInSourceAcceptance, err error) {
	token := p.autoSignInSourceToken(id)
	var sealed []byte
	err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_signin_source_acceptances WHERE token=? AND instance_id=? AND generation=?`, token, id.Scope.Instance, id.Scope.Generation).Scan(&sealed)
	if err != nil {
		return
	}
	err = p.noticeUnseal("auto_signin_acceptance", token, sealed, &box)
	if err == nil && (box.Version != 1 || box.Token != token || box.Request.Identity != id) {
		err = durable.ErrStore
	}
	return
}
func (p *PluginStore) insertAutoSignInProofTx(ctx context.Context, tx *sql.Tx, box autoSignInSourceAcceptance, proof autoSignInSourceProof) error {
	sealed, err := p.noticeSeal("auto_signin_result", box.Token, proof)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_source_results(token,sealed) VALUES(?,?)`, box.Token, sealed)
	return err
}
func (s *pluginAutoSignInSourcePort) Invoke(ctx context.Context, binding durable.Binding, req durable.SourceRequest) error {
	p := s.b.owner.a.pluginStore
	release, err := p.noticeLock(ctx, "auto-signin-source/"+p.autoSignInSourceToken(req.Identity))
	if err != nil {
		return err
	}
	defer release()
	var box autoSignInSourceAcceptance
	err = s.transaction(ctx, binding, func(tx *sql.Tx, state durable.State) error {
		if err := s.validateTx(ctx, tx, state, req.Identity, true); err != nil {
			return err
		}
		e := state.Runs[req.Identity.RunID].Lanes[req.Identity.Lane][req.Identity.Index]
		var recorded durable.SourceEnvelope
		if cleanupJournalStrict(e.Value, &recorded) != nil || !bytes.Equal(recorded.Request.Payload, req.Payload) {
			return durable.ErrConflict
		}
		_, err := p.readAutoSignInAcceptance(ctx, tx, req.Identity)
		// Never re-enter a source after any acceptance, including one lacking proof.
		if err == nil {
			return durable.ErrUncertain
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if req.Identity.Kind == "source.emit" {
			return s.acceptEventTx(ctx, tx, req)
		}
		box, err = s.acceptNoticeTx(ctx, tx, req)
		return err
	})
	if err != nil {
		return err
	}
	if req.Identity.Kind == "source.emit" || box.ConstructionErrorClass != "" {
		return nil
	}
	return s.b.owner.a.runNoticeRoute(context.WithValue(ctx, autoSignInSourceContextKey{}, s), box.RouteID)
}
func (s *pluginAutoSignInSourcePort) ReadResult(ctx context.Context, binding durable.Binding, id durable.SourceIdentity) (out durable.SourceResult, found bool, err error) {
	err = s.transaction(ctx, binding, func(tx *sql.Tx, state durable.State) error {
		if e := s.validateTx(ctx, tx, state, id, false); e != nil {
			return e
		}
		var e error
		out, found, e = s.b.owner.a.pluginStore.autoSignInSourceResultHistoricalTx(ctx, tx, state.Runs[id.RunID], id)
		return e
	})
	return
}

// Historical proof validation is read-only and conveys no live source/owner
// authority. The caller supplies a run from the authenticated journal snapshot.
func (p *PluginStore) autoSignInSourceResultHistoricalTx(ctx context.Context, tx *sql.Tx, run *durable.Run, id durable.SourceIdentity) (out durable.SourceResult, found bool, err error) {
	if run == nil || run.ID != id.RunID || run.Scope != id.Scope || run.Epoch != id.Epoch || id.Index < 0 || id.Index >= len(run.Lanes[id.Lane]) {
		return out, false, durable.ErrStore
	}
	entry := run.Lanes[id.Lane][id.Index]
	var recorded durable.SourceEnvelope
	if cleanupJournalStrict(entry.Value, &recorded) != nil || recorded.Version != 1 || recorded.Request.Identity != id || durable.ValidateSourceRequest(recorded.Request) != nil || entry.Kind != id.Kind || entry.Digest != id.PayloadDigest || entry.Guard != autoSignInSourceGuard(id) || (entry.Phase != "intent" && entry.Phase != "receipt" && entry.Phase != "uncertain") {
		return out, false, durable.ErrStore
	}
	box, e := p.readAutoSignInAcceptance(ctx, tx, id)
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	if !bytes.Equal(recorded.Request.Payload, box.Request.Payload) {
		return out, false, durable.ErrStore
	}
	var sealed []byte
	e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_signin_source_results WHERE token=?`, box.Token).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	var proof autoSignInSourceProof
	if e = p.noticeUnseal("auto_signin_result", box.Token, sealed, &proof); e != nil {
		return out, false, e
	}
	if proof.Result.Identity != id || proof.Result.ProofID != box.Token || proof.AcceptanceSHA != tokenHash(string(mustCleanSeedJSON(box))) || (proof.Result.Outcome != durable.SourceNormal && proof.Result.Outcome != durable.SourceException) {
		return out, false, durable.ErrStore
	}
	if id.Kind == "source.notify" && box.ConstructionErrorClass != "" {
		if e := p.validateAutoSignInConstructionProof(box, proof); e != nil {
			return out, false, e
		}
	} else if id.Kind == "source.notify" {
		r, _, e := p.readNoticeRoute(ctx, tx, box.RouteID)
		if e != nil {
			return out, false, e
		}
		if !r.Complete || proof.RouteInitialSHA != noticeInitialSHA(r) || proof.Count != r.Count || proof.LastSHA != r.LastSHA || proof.Result.ErrorClass != r.SourceError {
			return out, false, durable.ErrStore
		}
	} else if id.Kind == "source.emit" {
		if e = p.validateAutoSignInEventTx(ctx, tx, box); e != nil {
			return out, false, e
		}
	}
	if e = durable.ValidateSourceResult(box.Request, proof.Result); e != nil {
		return out, false, e
	}
	if entry.Phase == "receipt" && (recorded.Result == nil || *recorded.Result != proof.Result) {
		return out, false, durable.ErrStore
	}
	return proof.Result, true, nil
}

func autoSignInSourceGuard(id durable.SourceIdentity) string {
	raw, _ := json.Marshal(id)
	return "source:" + string(raw)
}

// Unlike the generic notice canonicalizer, source facts retain RawMessage key
// order. The durable request digest and its byte-exact payload are authoritative.
func (p *PluginStore) autoSignInSourceSeal(kind, id string, v any) ([]byte, error) {
	raw, err := signinJSON(v)
	if err != nil {
		return nil, err
	}
	if len(raw) > pluginCleanInvalidSeedJournalLimit {
		return nil, durable.ErrCapacity
	}
	return p.seal("notice_journal", id, kind, raw)
}
