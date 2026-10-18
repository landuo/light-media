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

	iyuu "light-media/internal/plugins/iyuuautoseed"
	sr "light-media/internal/plugins/seedrescue"
)

const seedRescueSchema = `CREATE TABLE IF NOT EXISTS plugin_seed_rescue_state(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));`
const seedRescueBlockedByUnknown = "SEED_RESCUE_BLOCKED_BY_UNKNOWN"

func seedRescueBlockedResult() json.RawMessage {
	return json.RawMessage(`{"version":1,"decision":"blocked_by_unknown","effects_started":false}`)
}

type pluginSeedRescueRuntime struct {
	mu          sync.Mutex
	owners      map[string]*pluginSeedRescueOwner
	now         func() time.Time
	loopbackAPI string
	proxy       func(context.Context, string) (string, error)
	afterEffect func(string) error
	beforeRead  func(context.Context)
}
type pluginSeedRescueOwner struct {
	a          *App
	instance   string
	generation int64
	gate       chan struct{}
	machine    *sr.Machine
	iyuu       *iyuu.Helper
	active     *seedRescueCall
}
type seedRescueValue struct {
	Revision int64
	SHA      string
}
type seedRescueEffect struct {
	Kind, Phase  string
	Args, Result json.RawMessage
}
type seedRescueAction struct {
	RunID, Action, InputSHA, Method, Phase, ErrorCode, Decision string
	Revision, EffectiveRevision                                 int64
	Result                                                      json.RawMessage
	Effects                                                     []seedRescueEffect
	Entered                                                     bool
}
type seedRescueState struct {
	Version                     int
	Instance                    string
	Generation, AppliedRevision int64
	Config                      json.RawMessage
	AppliedConfigSHA            string
	Values                      map[string]seedRescueValue
	Runs                        map[string]seedRescueAction
	Sources                     map[string]seedRescueScanSource
}
type seedRescueCall struct {
	o         *pluginSeedRescueOwner
	run       PluginRun
	method    string
	config    sr.Object
	revision  int64
	uncertain atomic.Bool
	warming   bool
	staged    sr.Object
}

func seedRescueKey(g int64) string { return "seed-rescue:v1:" + strconv.FormatInt(g, 10) }
func seedRescueConfig(raw json.RawMessage) (sr.Object, error) {
	var v sr.Object
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF || v == nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "稀缺保种配置必须为对象")
	}
	return v, nil
}
func (p *PluginStore) isSeedRescueRun(ctx context.Context, tx *sql.Tx, r PluginRun) bool {
	if r.Version != sr.Version {
		return false
	}
	var plugin string
	return tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, r.InstanceID).Scan(&plugin) == nil && plugin == sr.PluginID
}
func (p *PluginStore) seedRescueRead(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, g int64) (seedRescueState, []byte, error) {
	if db, ok := q.(*sql.DB); ok {
		tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return seedRescueState{}, nil, e
		}
		defer tx.Rollback()
		s, b, e := p.seedRescueRead(ctx, tx, instance, g)
		if e != nil {
			return s, nil, e
		}
		if e = tx.Commit(); e != nil {
			return s, nil, e
		}
		return s, b, nil
	}
	var s seedRescueState
	var sealed []byte
	e := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_seed_rescue_state WHERE instance_id=? AND generation=?`, instance, g).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return s, nil, seedRescueEvidenceError()
	}
	if e != nil {
		return s, nil, e
	}
	raw, e := p.unseal("run_result", instance, seedRescueKey(g), sealed)
	if e != nil {
		return s, nil, seedRescueEvidenceError()
	}
	if cleanupJournalStrict(raw, &s) != nil || s.Version != 1 || s.Instance != instance || s.Generation != g || s.Runs == nil || s.Values == nil {
		return s, nil, seedRescueEvidenceError()
	}
	if e = p.seedRescueValidateRootChain(ctx, q, s, raw); e != nil {
		return s, nil, e
	}
	if e = p.seedRescueValidateSources(ctx, q, s); e != nil {
		return s, nil, e
	}
	if e = p.seedRescueValidateDeferred(ctx, q, s); e != nil {
		return s, nil, e
	}
	for id, r := range s.Runs {
		if id != r.RunID || !pluginStorageID.MatchString(id) || !pluginStorageDigest.MatchString(r.InputSHA) || r.Revision < 1 || r.EffectiveRevision < r.Revision {
			return s, nil, seedRescueEvidenceError()
		}
		if r.Phase != "started" && r.Phase != "confirmed" && r.Phase != "failed" {
			return s, nil, seedRescueEvidenceError()
		}
		for _, f := range r.Effects {
			if f.Kind == "" || (f.Phase != "started" && f.Phase != "confirmed") || !json.Valid(f.Args) {
				return s, nil, seedRescueEvidenceError()
			}
		}
	}
	// Only the current generation owns the standard plugin_values projection.
	var current int64
	if e = q.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&current); e != nil {
		return s, nil, e
	}
	if current == g {
		if e = p.seedRescueValidateValues(ctx, q, s); e != nil {
			return s, nil, e
		}
	}
	return s, sealed, nil
}
func (p *PluginStore) seedRescueValidateValues(ctx context.Context, q pluginRemoveLinkJournalReader, s seedRescueState) error {
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
		proof, ok := s.Values[key]
		if !ok || proof.Revision != rev || !pluginStorageDigest.MatchString(proof.SHA) {
			return seedRescueEvidenceError()
		}
		raw, e := p.unseal("data", s.Instance, key, sealed)
		if e != nil || tokenHash(string(raw)) != proof.SHA {
			return seedRescueEvidenceError()
		}
		seen[key] = true
	}
	if e = rows.Err(); e != nil {
		return e
	}
	for k := range s.Values {
		if !seen[k] {
			return seedRescueEvidenceError()
		}
	}
	return nil
}
func (p *PluginStore) seedRescueSave(ctx context.Context, tx *sql.Tx, s seedRescueState, previous []byte) error {
	if previous == nil {
		return seedRescueEvidenceError()
	}
	raw, e := json.Marshal(s)
	if e != nil || len(raw) > pluginDataValueLimit {
		return bad("SEED_RESCUE_JOURNAL_LIMIT", "稀缺保种执行证据超出容量")
	}
	root, priorRoot, e := p.seedRescueRootRead(ctx, tx, s.Instance, s.Generation)
	if e != nil {
		return e
	}
	old, e := p.unseal("run_result", s.Instance, seedRescueKey(s.Generation), previous)
	if e != nil || root.StateSHA != tokenHash(string(old)) {
		return seedRescueEvidenceError()
	}
	sealed, e := p.seal("run_result", s.Instance, seedRescueKey(s.Generation), raw)
	if e != nil {
		return e
	}
	changed, e := tx.ExecContext(ctx, `UPDATE plugin_seed_rescue_state SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?`, sealed, s.Instance, s.Generation, previous)
	if e != nil {
		return e
	}
	if n, e := changed.RowsAffected(); e != nil || n != 1 {
		return seedRescueEvidenceError()
	}
	root.Sequence++
	root.StateSHA = tokenHash(string(raw))
	sealed, e = p.seal("run_result", s.Instance, seedRescueRootKey(s.Generation), mustCleanSeedJSON(root))
	if e != nil {
		return e
	}
	changed, e = tx.ExecContext(ctx, `UPDATE plugin_seed_rescue_roots SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?`, sealed, s.Instance, s.Generation, priorRoot)
	if e != nil {
		return e
	}
	if n, e := changed.RowsAffected(); e != nil || n != 1 {
		return seedRescueEvidenceError()
	}
	return nil
}
func (o *pluginSeedRescueOwner) update(ctx context.Context, fn func(*seedRescueState) error) error {
	tx, e := o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	s, b, e := o.a.pluginStore.seedRescueRead(ctx, tx, o.instance, o.generation)
	if e == nil {
		e = fn(&s)
	}
	if e == nil {
		e = o.a.pluginStore.seedRescueSave(ctx, tx, s, b)
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
func seedRescueNoUnknown(s seedRescueState) error {
	for _, r := range s.Runs {
		if r.Phase == "started" {
			return bad("SEED_RESCUE_UNCERTAIN", "稀缺保种存在待核对写操作，未重复执行")
		}
		for _, e := range r.Effects {
			if e.Phase != "confirmed" {
				return bad("SEED_RESCUE_UNCERTAIN", "稀缺保种存在待核对写操作，未重复执行")
			}
		}
	}
	return nil
}
func (a *App) seedRescueOwner(ctx context.Context, in PluginInstallation) (*pluginSeedRescueOwner, error) {
	if in.PluginID != sr.PluginID || in.Version != sr.Version || !in.Installed || in.Generation < 1 {
		return nil, bad("SEED_RESCUE_SCOPE_DENIED", "稀缺保种安装身份无效")
	}
	r := &a.pluginSeedRescue
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owners == nil {
		r.owners = map[string]*pluginSeedRescueOwner{}
	}
	key := in.InstanceID + ":" + strconv.FormatInt(in.Generation, 10)
	if o := r.owners[key]; o != nil {
		return o, nil
	}
	o := &pluginSeedRescueOwner{a: a, instance: in.InstanceID, generation: in.Generation, gate: make(chan struct{}, 1)}
	r.owners[key] = o
	return o, nil
}
func (o *pluginSeedRescueOwner) lock(ctx context.Context) error {
	select {
	case o.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *pluginSeedRescueOwner) unlock() { <-o.gate }
func (o *pluginSeedRescueOwner) tryLock() bool {
	select {
	case o.gate <- struct{}{}:
		return true
	default:
		return false
	}
}
