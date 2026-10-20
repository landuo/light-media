package app

// This is the BrushFlow owner's storage projection. Workers never supply roots.
// The sealed lifecycle root authenticates this fixed descriptor; immutable trie
// pages authenticate membership, absence, exact source bytes and effect ordinals.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	bl "light-media/internal/plugins/brushflow/ledger"
)

type brushFlowDescriptor struct {
	Version                     int
	Instance                    string
	Generation, AppliedRevision int64
	Config                      json.RawMessage
	AppliedConfigSHA            string
	Roots                       bl.Roots
	LiveSourceIndex             bool
}
type brushFlowRunPage struct {
	Action  brushFlowAction
	Effects int
}
type brushFlowPageView struct {
	p               *PluginStore
	ctx             context.Context
	tx              *sql.Tx
	db              *sql.DB
	store           *bl.Store
	roots           bl.Roots
	deletedValues   map[string]bool
	closedSources   map[string]bool
	liveSourceIndex bool
	err             error
}

func (v *brushFlowPageView) fail(err error) {
	if err != nil && v.err == nil {
		if errors.Is(err, bl.ErrIntegrity) || errors.Is(err, bl.ErrInput) {
			err = brushFlowEvidenceError()
		}
		v.err = err
	}
}
func (v *brushFlowPageView) transact(fn func(*sql.Tx) error) error {
	if v.err != nil {
		return v.err
	}
	if v.tx != nil {
		return fn(v.tx)
	}
	tx, e := v.db.BeginTx(v.ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit()
}
func (v *brushFlowPageView) get(c bl.Collection, root bl.Root, key string, out any) (bool, error) {
	var exists bool
	e := v.transact(func(tx *sql.Tx) error {
		raw, ok, e := v.store.Lookup(v.ctx, tx, c, root, key)
		exists = ok
		if e != nil || !ok {
			return e
		}
		if cleanupJournalStrict(raw, out) != nil {
			return brushFlowEvidenceError()
		}
		return nil
	})
	v.fail(e)
	return exists, e
}
func (s brushFlowState) check() error {
	if s.pages != nil {
		return s.pages.err
	}
	return nil
}
func (s brushFlowState) inTx(tx *sql.Tx) brushFlowState {
	if s.pages != nil {
		v := *s.pages
		v.tx = tx
		v.db = nil
		s.pages = &v
	}
	return s
}
func (s brushFlowState) lookupRun(key string) (brushFlowAction, bool) {
	if s.pages != nil && s.pages.tx == nil {
		var r brushFlowAction
		var ok bool
		e := s.pages.transact(func(tx *sql.Tx) error { bound := s.inTx(tx); r, ok = bound.lookupRun(key); return bound.check() })
		s.pages.fail(e)
		return r, ok && e == nil
	}

	if r, ok := s.Runs[key]; ok {
		return r, true
	}
	if s.pages == nil {
		return brushFlowAction{}, false
	}
	v := s.pages
	var page brushFlowRunPage
	ok, e := v.get(bl.Runs, v.roots.Runs, key, &page)
	if e != nil || !ok {
		return brushFlowAction{}, false
	}
	if page.Action.RunID != key || page.Action.Effects != nil || page.Effects < 0 {
		v.fail(brushFlowEvidenceError())
		return brushFlowAction{}, false
	}
	r := page.Action
	// Only the selected run is materialized. Complete history is streamed one run
	// at a time; unrelated historical effects never accumulate in a state map.
	for i := 0; i < page.Effects; i++ {
		var f brushFlowEffect
		ok, e := v.get(bl.Effects, v.roots.Effects, brushFlowEffectKey(key, i), &f)
		if e != nil || !ok {
			v.fail(brushFlowEvidenceError())
			return brushFlowAction{}, false
		}
		r.Effects = append(r.Effects, f)
	}
	return r, true
}
func (s brushFlowState) run(key string) brushFlowAction        { r, _ := s.lookupRun(key); return r }
func (s *brushFlowState) setRun(key string, r brushFlowAction) { s.Runs[key] = r }
func (s brushFlowState) lookupSource(key string) (brushFlowScanSource, bool) {
	if r, ok := s.Sources[key]; ok {
		return r, true
	}
	if s.pages == nil {
		return brushFlowScanSource{}, false
	}
	var r brushFlowScanSource
	ok, e := s.pages.get(bl.Sources, s.pages.roots.Sources, key, &r)
	return r, ok && e == nil
}
func (s brushFlowState) lookupValue(key string) (brushFlowValue, bool) {
	if s.pages != nil && s.pages.deletedValues[key] {
		return brushFlowValue{}, false
	}
	if r, ok := s.Values[key]; ok {
		return r, true
	}
	if s.pages == nil {
		return brushFlowValue{}, false
	}
	var r brushFlowValue
	ok, e := s.pages.get(bl.Values, s.pages.roots.Values, key, &r)
	return r, ok && e == nil
}
func (s brushFlowState) value(key string) brushFlowValue { r, _ := s.lookupValue(key); return r }
func (s *brushFlowState) deleteValue(key string) {
	delete(s.Values, key)
	if s.pages != nil {
		s.pages.deletedValues[key] = true
	}
}
func (s *brushFlowState) setValue(key string, v brushFlowValue) {
	s.Values[key] = v
	if s.pages != nil {
		delete(s.pages.deletedValues, key)
	}
}
func (s *brushFlowState) setSource(key string, v brushFlowScanSource) { s.Sources[key] = v }
func brushFlowEffectKey(run string, ordinal int) string               { return run + ":" + strconv.Itoa(ordinal) }
func (s brushFlowState) allRuns() func(func(string, brushFlowAction) bool) {
	return func(yield func(string, brushFlowAction) bool) {
		if s.pages != nil {
			v := s.pages
			e := v.iterate(bl.Runs, v.roots.Runs, func(key string, _ []byte) error {
				if _, ok := s.Runs[key]; ok {
					return nil
				}
				r, ok := s.lookupRun(key)
				if !ok {
					return brushFlowEvidenceError()
				}
				if !yield(key, r) {
					return errBrushFlowPageStop
				}
				return nil
			})
			if !errors.Is(e, errBrushFlowPageStop) {
				v.fail(e)
			}
			if e != nil {
				return
			}
		}
		for k, r := range s.Runs {
			if !yield(k, r) {
				return
			}
		}
	}
}

var errBrushFlowPageStop = errors.New("private BrushFlow page iteration stopped")

func (s brushFlowState) allSources() func(func(string, brushFlowScanSource) bool) {
	return func(yield func(string, brushFlowScanSource) bool) {
		if s.pages != nil {
			v := s.pages
			e := v.iterate(bl.Sources, v.roots.Sources, func(key string, raw []byte) error {
				if _, ok := s.Sources[key]; ok {
					return nil
				}
				var r brushFlowScanSource
				if cleanupJournalStrict(raw, &r) != nil {
					return brushFlowEvidenceError()
				}
				if !yield(key, r) {
					return errBrushFlowPageStop
				}
				return nil
			})
			if !errors.Is(e, errBrushFlowPageStop) {
				v.fail(e)
			}
			if e != nil {
				return
			}
		}
		for k, r := range s.Sources {
			if !yield(k, r) {
				return
			}
		}
	}
}
func (s brushFlowState) allValues() func(func(string, brushFlowValue) bool) {
	return func(yield func(string, brushFlowValue) bool) {
		if s.pages != nil {
			v := s.pages
			e := v.iterate(bl.Values, v.roots.Values, func(key string, raw []byte) error {
				if _, ok := s.Values[key]; ok || v.deletedValues[key] {
					return nil
				}
				var r brushFlowValue
				if cleanupJournalStrict(raw, &r) != nil {
					return brushFlowEvidenceError()
				}
				if !yield(key, r) {
					return errBrushFlowPageStop
				}
				return nil
			})
			if !errors.Is(e, errBrushFlowPageStop) {
				v.fail(e)
			}
			if e != nil {
				return
			}
		}
		for k, r := range s.Values {
			if !yield(k, r) {
				return
			}
		}
	}
}
func brushFlowPending(r brushFlowAction) bool {
	if r.Phase == "started" {
		return true
	}
	for _, f := range r.Effects {
		if f.Phase != "confirmed" {
			return true
		}
	}
	return false
}
func (s brushFlowState) pendingRuns() func(func(string, brushFlowAction) bool) {
	return func(yield func(string, brushFlowAction) bool) {
		if s.pages != nil {
			v := s.pages
			e := v.iterate(bl.Unresolved, v.roots.Unresolved, func(key string, raw []byte) error {
				if _, ok := s.Runs[key]; ok {
					return nil
				}
				if strings.HasPrefix(key, brushFlowPendingSourcePrefix) {
					return nil
				}
				var page brushFlowRunPage
				if cleanupJournalStrict(raw, &page) != nil {
					return brushFlowEvidenceError()
				}
				var actual brushFlowRunPage
				ok, e := v.get(bl.Runs, v.roots.Runs, key, &actual)
				if e != nil || !ok || !bytes.Equal(mustCleanSeedJSON(page), mustCleanSeedJSON(actual)) {
					return brushFlowEvidenceError()
				}
				r, _, ok := s.lookupRunSummary(key)
				if !ok || r.Phase != "started" {
					return brushFlowEvidenceError()
				}
				// The selected unresolved tail can carry an unknown mutation;
				// validate it without rebuilding all confirmed historical effects.
				if page.Effects > 0 {
					f, exists := s.lookupEffect(key, page.Effects-1)
					if !exists || f.Kind == "" || (f.Phase != "started" && f.Phase != "confirmed") || !json.Valid(f.Args) {
						return brushFlowEvidenceError()
					}
				}
				if !s.ignoredRuns[key] && !yield(key, r) {
					return errBrushFlowPageStop
				}
				return nil
			})
			if !errors.Is(e, errBrushFlowPageStop) {
				v.fail(e)
			}
			if e != nil {
				return
			}
		}
		for k, r := range s.Runs {
			if !s.ignoredRuns[k] && brushFlowPending(r) && !yield(k, r) {
				return
			}
		}
	}
}
func (p *PluginStore) brushFlowDecodeState(ctx context.Context, tx *sql.Tx, instance string, g int64, raw []byte) (brushFlowState, error) {
	var probe struct{ Version int }
	if json.Unmarshal(raw, &probe) != nil {
		return brushFlowState{}, brushFlowEvidenceError()
	}
	var s brushFlowState
	if probe.Version == 1 {
		if cleanupJournalStrict(raw, &s) != nil || s.Instance != instance || s.Generation != g || s.Runs == nil || s.Values == nil || s.Sources == nil {
			return s, brushFlowEvidenceError()
		}
		return s, nil
	}
	var d brushFlowDescriptor
	if probe.Version != 2 || cleanupJournalStrict(raw, &d) != nil || d.Instance != instance || d.Generation != g {
		return s, brushFlowEvidenceError()
	}
	store, e := bl.New(p.store.key, instance, g)
	if e != nil {
		return s, e
	}
	// These exact roots are authenticated by the App's separate lifecycle seal.
	// Validate all root spellings even if this read does not touch a collection.
	for _, r := range []bl.Root{d.Roots.Runs, d.Roots.Effects, d.Roots.Sources, d.Roots.Unresolved, d.Roots.Values} {
		if !pluginStorageDigest.MatchString(string(r)) {
			return s, brushFlowEvidenceError()
		}
	}
	s = brushFlowState{Version: 2, Instance: instance, Generation: g, AppliedRevision: d.AppliedRevision, Config: d.Config, AppliedConfigSHA: d.AppliedConfigSHA, Runs: map[string]brushFlowAction{}, Sources: map[string]brushFlowScanSource{}, Values: map[string]brushFlowValue{}, pages: &brushFlowPageView{p: p, ctx: ctx, tx: tx, store: store, roots: d.Roots, deletedValues: map[string]bool{}, closedSources: map[string]bool{}, liveSourceIndex: d.LiveSourceIndex}}
	return s, nil
}
func (p *PluginStore) brushFlowEncodeState(ctx context.Context, tx *sql.Tx, s brushFlowState) ([]byte, error) {
	if e := s.check(); e != nil {
		return nil, e
	}
	store, e := bl.New(p.store.key, s.Instance, s.Generation)
	if e != nil {
		return nil, e
	}
	if e = bl.CreateSchema(ctx, tx); e != nil {
		return nil, e
	}
	roots := store.EmptyRoots()
	if s.pages != nil {
		roots = s.pages.roots
	}
	put := func(c bl.Collection, root bl.Root, key string, value any) (bl.Root, error) {
		raw, e := json.Marshal(value)
		if e != nil {
			return "", e
		}
		return store.Put(ctx, tx, c, root, key, raw)
	}
	for key, r := range s.Runs {
		if s.pages != nil {
			base := s
			base.Runs = nil
			old, exists := base.lookupRun(key)
			if e := s.check(); e != nil {
				return nil, e
			}
			if exists {
				if len(old.Effects) > len(r.Effects) {
					return nil, brushFlowEvidenceError()
				}
				for i, f := range old.Effects {
					next := r.Effects[i]
					if f.Kind != next.Kind || !bytes.Equal(f.Args, next.Args) || (f.Phase == "confirmed" && !bytes.Equal(mustCleanSeedJSON(f), mustCleanSeedJSON(next))) {
						return nil, brushFlowEvidenceError()
					}
				}
			}
		}
		page := brushFlowRunPage{Action: r, Effects: len(r.Effects)}
		page.Action.Effects = nil
		for i, f := range r.Effects {
			roots.Effects, e = put(bl.Effects, roots.Effects, brushFlowEffectKey(key, i), f)
			if e != nil {
				return nil, e
			}
		}
		raw, e := json.Marshal(page)
		if e != nil {
			return nil, e
		}
		roots.Runs, e = store.Put(ctx, tx, bl.Runs, roots.Runs, key, raw)
		if e != nil {
			return nil, e
		}
		if brushFlowPending(r) {
			roots.Unresolved, e = store.Put(ctx, tx, bl.Unresolved, roots.Unresolved, key, raw)
		} else {
			roots.Unresolved, e = store.Delete(ctx, tx, bl.Unresolved, roots.Unresolved, key)
		}
		if e != nil {
			return nil, e
		}
	}
	for key, r := range s.Sources {
		roots.Sources, e = put(bl.Sources, roots.Sources, key, r)
		if e != nil {
			return nil, e
		}
	}
	pending := s.Sources
	if s.pages != nil && !s.pages.liveSourceIndex {
		pending = map[string]brushFlowScanSource{}
		for key, source := range s.allSources() {
			pending[key] = source
		}
		if e = s.check(); e != nil {
			return nil, e
		}
	}
	for key, source := range pending {
		var phase string
		if e = tx.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, source.Run).Scan(&phase); e != nil {
			return nil, brushFlowEvidenceError()
		}
		if phase == "pending" || phase == "running" || phase == "reconcile_pending" {
			roots.Unresolved, e = put(bl.Unresolved, roots.Unresolved, brushFlowPendingSourcePrefix+key, source)
			if e != nil {
				return nil, e
			}
		}
	}
	// The native terminal owner receipt is published before worker ACK. Closing
	// its exact source in this same root transaction never relies on SQL absence.
	for _, r := range s.Runs {
		if r.Action != "task" || brushFlowPending(r) {
			continue
		}
		actual, err := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, r.RunID))
		if err != nil {
			return nil, err
		}
		token, err := brushFlowSourceToken(actual)
		if err != nil {
			return nil, err
		}
		source, ok := s.lookupSource(token)
		if !ok || source.Run != r.RunID || source.InputSHA != r.InputSHA {
			return nil, brushFlowEvidenceError()
		}
		roots.Unresolved, e = store.Delete(ctx, tx, bl.Unresolved, roots.Unresolved, brushFlowPendingSourcePrefix+token)
		if e != nil {
			return nil, e
		}
	}
	if s.pages != nil {
		for key := range s.pages.closedSources {
			roots.Unresolved, e = store.Delete(ctx, tx, bl.Unresolved, roots.Unresolved, brushFlowPendingSourcePrefix+key)
			if e != nil {
				return nil, e
			}
		}
	}

	for key, r := range s.Values {
		roots.Values, e = put(bl.Values, roots.Values, key, r)
		if e != nil {
			return nil, e
		}
	}
	if s.pages != nil {
		for key := range s.pages.deletedValues {
			roots.Values, e = store.Delete(ctx, tx, bl.Values, roots.Values, key)
			if e != nil {
				return nil, e
			}
		}
	}
	return json.Marshal(brushFlowDescriptor{2, s.Instance, s.Generation, s.AppliedRevision, s.Config, s.AppliedConfigSHA, roots, true})
}

func (v *brushFlowPageView) iterate(c bl.Collection, root bl.Root, fn func(string, []byte) error) error {
	if v.tx != nil {
		return v.store.Iterate(v.ctx, v.tx, c, root, fn)
	}
	after := ""
	for {
		tx, e := v.db.BeginTx(v.ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return e
		}
		key, raw, next, ok, e := v.store.Next(v.ctx, tx, c, root, after)
		if e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		if !ok {
			return nil
		}
		if e = fn(key, raw); e != nil {
			return e
		}
		after = next
	}
}

// Summary and effect point access keep historical notice/event/source proofs
// bounded by the selected record, rather than its entire originating run.
func (s brushFlowState) lookupRunSummary(key string) (brushFlowAction, int, bool) {
	if r, ok := s.Runs[key]; ok {
		n := len(r.Effects)
		r.Effects = nil
		return r, n, true
	}
	if s.pages == nil {
		return brushFlowAction{}, 0, false
	}
	var page brushFlowRunPage
	ok, e := s.pages.get(bl.Runs, s.pages.roots.Runs, key, &page)
	if e != nil || !ok {
		return brushFlowAction{}, 0, false
	}
	if page.Action.RunID != key || page.Action.Effects != nil || page.Effects < 0 {
		s.pages.fail(brushFlowEvidenceError())
		return brushFlowAction{}, 0, false
	}
	return page.Action, page.Effects, true
}
func (s brushFlowState) lookupEffect(run string, ordinal int) (brushFlowEffect, bool) {
	if ordinal < 0 {
		return brushFlowEffect{}, false
	}
	if r, ok := s.Runs[run]; ok {
		if ordinal >= len(r.Effects) {
			return brushFlowEffect{}, false
		}
		return r.Effects[ordinal], true
	}
	if s.pages == nil {
		return brushFlowEffect{}, false
	}
	_, count, ok := s.lookupRunSummary(run)
	if !ok || ordinal >= count {
		return brushFlowEffect{}, false
	}
	var f brushFlowEffect
	ok, e := s.pages.get(bl.Effects, s.pages.roots.Effects, brushFlowEffectKey(run, ordinal), &f)
	if e != nil || !ok {
		s.pages.fail(brushFlowEvidenceError())
		return brushFlowEffect{}, false
	}
	return f, true
}
