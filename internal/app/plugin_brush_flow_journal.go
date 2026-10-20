package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	bf "light-media/internal/plugins/brushflow"
)

const brushFlowSchema = `CREATE TABLE IF NOT EXISTS plugin_brush_flow_state(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));`
const brushFlowBlockedByUnknown = "BRUSH_FLOW_BLOCKED_BY_UNKNOWN"

func brushFlowBlockedResult() json.RawMessage {
	return json.RawMessage(`{"version":1,"decision":"blocked_by_unknown","effects_started":false}`)
}

type pluginBrushFlowRuntime struct {
	mu          sync.Mutex
	owners      map[string]*pluginBrushFlowOwner
	pools       *brushFlowSourcePools
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
	loopbackAPI string
	proxy       func(context.Context, string) (string, error)
	afterEffect func(string) error
	beforeRead  func(context.Context)
	beforeHost  func(context.Context, PluginRun)
	beforeLease func(context.Context, PluginRun)
	afterProof  func(context.Context, *brushFlowCall)
}
type pluginBrushFlowOwner struct {
	a                 *App
	instance          string
	generation        int64
	gate              brushFlowGate
	mu                sync.Mutex
	apiGate           chan struct{}
	live              map[string]*brushFlowLiveLease
	machine           *bf.Core
	subscriptionMu    sync.Mutex
	subscriptionNames map[string][]string
}
type brushFlowValue struct {
	Revision int64
	SHA      string
}
type brushFlowEffect struct {
	Kind, Phase  string
	Args, Result json.RawMessage
}
type brushFlowAction struct {
	RunID, Action, InputSHA, Method, Phase, ErrorCode, Decision string
	Revision, EffectiveRevision                                 int64
	Result                                                      json.RawMessage
	Effects                                                     []brushFlowEffect
	Entered                                                     bool
}
type brushFlowState struct {
	Version                     int
	Instance                    string
	Generation, AppliedRevision int64
	Config                      json.RawMessage
	AppliedConfigSHA            string
	Values                      map[string]brushFlowValue
	Runs                        map[string]brushFlowAction
	Sources                     map[string]brushFlowScanSource
	pages                       *brushFlowPageView
	ignoredRuns                 map[string]bool
}
type brushFlowCall struct {
	o                 *pluginBrushFlowOwner
	run               PluginRun
	method            string
	config            bf.Row
	revision          int64
	uncertain         atomic.Bool
	warming           bool
	staged            bf.Row
	writes            map[string]json.RawMessage
	services          []bf.Service
	queued            []brushFlowQueued
	taskID, operation string
	observations      map[string]map[string]bf.DownloadState
	candidates        map[string]brushFlowCandidate
	temporaryTags     map[string]string
	dataKeys          map[string]bool
	runCipherSHA      string
	observedTags      map[string][]string
}

func brushFlowKey(g int64) string { return "brush-flow:v1:" + strconv.FormatInt(g, 10) }
func brushFlowConfig(raw json.RawMessage) (bf.Row, error) {
	var v bf.Row
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF || v == nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "站点刷流配置必须为对象")
	}
	return v, nil
}
func (p *PluginStore) isBrushFlowRun(ctx context.Context, tx *sql.Tx, r PluginRun) bool {
	if r.Version != bf.Version {
		return false
	}
	var plugin string
	return tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, r.InstanceID).Scan(&plugin) == nil && plugin == bf.PluginID
}
func (p *PluginStore) brushFlowRead(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, g int64) (brushFlowState, []byte, error) {
	return p.brushFlowReadMode(ctx, q, instance, g, true)
}

// Ordinary calls verify fixed roots, every unresolved identity and the exact
// records they consume. Cold/lifecycle/audit uses brushFlowRead's complete scan.
// Unvisited archived physical pages are not asserted to exist by a point read.
func (p *PluginStore) brushFlowReadCurrent(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, g int64) (brushFlowState, []byte, error) {
	return p.brushFlowReadMode(ctx, q, instance, g, false)
}
func (p *PluginStore) brushFlowReadMode(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, g int64, audit bool) (brushFlowState, []byte, error) {
	if db, ok := q.(*sql.DB); ok {
		tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return brushFlowState{}, nil, e
		}
		defer tx.Rollback()
		s, b, e := p.brushFlowReadMode(ctx, tx, instance, g, audit)
		if s.pages != nil {
			s.pages.tx = nil
			s.pages.db = db
		}
		if e != nil {
			return s, nil, e
		}
		if e = tx.Commit(); e != nil {
			return s, nil, e
		}
		return s, b, nil
	}
	var s brushFlowState
	var sealed []byte
	e := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_brush_flow_state WHERE instance_id=? AND generation=?`, instance, g).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return s, nil, brushFlowEvidenceError()
	}
	if e != nil {
		return s, nil, e
	}
	raw, e := p.unseal("run_result", instance, brushFlowKey(g), sealed)
	if e != nil {
		return s, nil, brushFlowEvidenceError()
	}
	s, e = p.brushFlowDecodeState(ctx, q.(*sql.Tx), instance, g, raw)
	if e != nil {
		return s, nil, brushFlowEvidenceError()
	}
	if e = p.brushFlowValidateRootChainMode(ctx, q, s, raw, audit || s.Version == 1); e != nil {
		return s, nil, e
	}
	if !audit && s.Version == 2 && s.pages.liveSourceIndex {
		var current int64
		if e = q.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&current); e != nil {
			return s, nil, e
		}
		if current == g {
			for _, r := range s.pendingRuns() {
				if e = p.brushFlowValidateAction(ctx, q, s, r); e != nil {
					return s, nil, e
				}
			}
			for range s.pendingSources(ctx, q) {
			}
			if e = s.check(); e != nil {
				return s, nil, e
			}
			return s, sealed, nil
		}
	}
	if e = p.brushFlowValidateSources(ctx, q, s); e != nil {
		return s, nil, e
	}
	if e = p.brushFlowValidateDeferred(ctx, q, s); e != nil {
		return s, nil, e
	}
	for id, r := range s.allRuns() {
		if id != r.RunID || !pluginStorageID.MatchString(id) || !pluginStorageDigest.MatchString(r.InputSHA) || r.Revision < 1 || r.EffectiveRevision < r.Revision {
			return s, nil, brushFlowEvidenceError()
		}
		if r.Phase != "started" && r.Phase != "confirmed" && r.Phase != "failed" {
			return s, nil, brushFlowEvidenceError()
		}
		for _, f := range r.Effects {
			if f.Kind == "" || (f.Phase != "started" && f.Phase != "confirmed") || !json.Valid(f.Args) {
				return s, nil, brushFlowEvidenceError()
			}
		}
	}
	for _, r := range s.allRuns() {
		for ordinal, f := range r.Effects {
			if f.Kind != "plugin_triggered" {
				continue
			}
			var proof brushFlowEventProof
			if f.Phase != "confirmed" || cleanupJournalStrict(f.Args, &proof) != nil || proof.Version != 1 {
				return s, nil, brushFlowEvidenceError()
			}
			var iid, rid string
			var generation int64
			var index int
			if q.QueryRowContext(ctx, `SELECT instance_id,generation,run_id,ordinal FROM plugin_trigger_sources WHERE id=?`, proof.ID).Scan(&iid, &generation, &rid, &index) != nil || iid != instance || generation != g || rid != r.RunID || index != ordinal {
				return s, nil, brushFlowEvidenceError()
			}
			var raw []byte
			if q.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, proof.Event.EventID).Scan(&raw) != nil || !brushFlowSameJSON(json.RawMessage(raw), proof.Event) {
				return s, nil, brushFlowEvidenceError()
			}
		}
	}
	// Only the current generation owns the standard plugin_values projection.
	var current int64
	if e = q.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&current); e != nil {
		return s, nil, e
	}
	if current == g {
		if e = p.brushFlowValidateValues(ctx, q, s); e != nil {
			return s, nil, e
		}
	}
	if e = s.check(); e != nil {
		return s, nil, e
	}
	return s, sealed, nil
}
func (p *PluginStore) brushFlowValidateValues(ctx context.Context, q pluginRemoveLinkJournalReader, s brushFlowState) error {
	rows, e := q.QueryContext(ctx, `SELECT item_key,revision,sealed FROM plugin_values WHERE instance_id=? AND kind='data'`, s.Instance)
	if e != nil {
		return e
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var key string
		var rev int64
		var sealed []byte
		if e = rows.Scan(&key, &rev, &sealed); e != nil {
			return e
		}
		proof, ok := s.lookupValue(key)
		if !ok || proof.Revision != rev || !pluginStorageDigest.MatchString(proof.SHA) {
			return brushFlowEvidenceError()
		}
		raw, e := p.unseal("data", s.Instance, key, sealed)
		if e != nil || tokenHash(string(raw)) != proof.SHA {
			return brushFlowEvidenceError()
		}
		seen[key] = true
	}
	if e = rows.Err(); e != nil {
		return e
	}
	for k := range s.allValues() {
		if !seen[k] {
			return brushFlowEvidenceError()
		}
	}
	return nil
}
func (p *PluginStore) brushFlowSave(ctx context.Context, tx *sql.Tx, s brushFlowState, previous []byte) error {
	if previous == nil {
		return brushFlowEvidenceError()
	}
	raw, e := p.brushFlowEncodeState(ctx, tx, s)
	if e != nil {
		return e
	}
	if len(raw) > pluginDataValueLimit {
		return bad("BRUSH_FLOW_JOURNAL_LIMIT", "站点刷流执行证据超出容量")
	}
	head, priorHead, e := p.brushFlowHeadRead(ctx, tx, s.Instance)
	if e != nil || head.Generation != s.Generation {
		return brushFlowEvidenceError()
	}
	root, priorRoot, e := p.brushFlowRootRead(ctx, tx, s.Instance, s.Generation)
	if e != nil {
		return e
	}
	if head.IdentitySHA != brushFlowRootIdentity(root) || head.RootSHA != tokenHash(string(mustCleanSeedJSON(root))) {
		return brushFlowEvidenceError()
	}
	old, e := p.unseal("run_result", s.Instance, brushFlowKey(s.Generation), previous)
	if e != nil || root.StateSHA != tokenHash(string(old)) {
		return brushFlowEvidenceError()
	}
	sealed, e := p.seal("run_result", s.Instance, brushFlowKey(s.Generation), raw)
	if e != nil {
		return e
	}
	changed, e := tx.ExecContext(ctx, `UPDATE plugin_brush_flow_state SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?`, sealed, s.Instance, s.Generation, previous)
	if e != nil {
		return e
	}
	if n, e := changed.RowsAffected(); e != nil || n != 1 {
		return brushFlowEvidenceError()
	}
	root.Sequence++
	root.StateSHA = tokenHash(string(raw))
	sealed, e = p.seal("run_result", s.Instance, brushFlowRootKey(s.Generation), mustCleanSeedJSON(root))
	if e != nil {
		return e
	}
	changed, e = tx.ExecContext(ctx, `UPDATE plugin_brush_flow_roots SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?`, sealed, s.Instance, s.Generation, priorRoot)
	if e != nil {
		return e
	}
	if n, e := changed.RowsAffected(); e != nil || n != 1 {
		return brushFlowEvidenceError()
	}
	return p.brushFlowHeadWriteTx(ctx, tx, root, priorHead)
}
func (o *pluginBrushFlowOwner) update(ctx context.Context, fn func(*brushFlowState) error) error {
	tx, e := o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	s, b, e := o.a.pluginStore.brushFlowReadCurrent(ctx, tx, o.instance, o.generation)
	if e == nil {
		e = fn(&s)
	}
	if e == nil {
		e = o.a.pluginStore.brushFlowSave(ctx, tx, s, b)
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
func brushFlowNoUnknown(s brushFlowState) error {
	if e := s.check(); e != nil {
		return e
	}
	for _, r := range s.pendingRuns() {
		if r.Phase == "started" {
			return bad("BRUSH_FLOW_UNCERTAIN", "站点刷流存在待核对写操作，未重复执行")
		}
		for _, e := range r.Effects {
			if e.Phase != "confirmed" {
				return bad("BRUSH_FLOW_UNCERTAIN", "站点刷流存在待核对写操作，未重复执行")
			}
		}
	}
	return s.check()
}
func (a *App) brushFlowOwner(ctx context.Context, in PluginInstallation) (*pluginBrushFlowOwner, error) {
	if in.PluginID != bf.PluginID || in.Version != bf.Version || !in.Installed || in.Generation < 1 {
		return nil, bad("BRUSH_FLOW_SCOPE_DENIED", "站点刷流安装身份无效")
	}
	r := &a.pluginBrushFlow
	a.pluginStore.brushFlowLiveMu.Lock()
	if a.pluginStore.brushFlowRuntime != nil && a.pluginStore.brushFlowRuntime != r {
		a.pluginStore.brushFlowLiveMu.Unlock()
		return nil, brushFlowEvidenceError()
	}
	a.pluginStore.brushFlowRuntime = r
	a.pluginStore.brushFlowLiveMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owners == nil {
		r.owners = map[string]*pluginBrushFlowOwner{}
	}
	key := in.InstanceID + ":" + strconv.FormatInt(in.Generation, 10)
	if o := r.owners[key]; o != nil {
		return o, nil
	}
	o := &pluginBrushFlowOwner{a: a, instance: in.InstanceID, generation: in.Generation, apiGate: make(chan struct{}, 1), live: map[string]*brushFlowLiveLease{}}
	r.owners[key] = o
	return o, nil
}
func (o *pluginBrushFlowOwner) lock(ctx context.Context) error { return o.gate.acquire(ctx, true) }
func (o *pluginBrushFlowOwner) unlock()                        { o.gate.release(true) }
