package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"light-media/internal/plugins/scrapefileclean"
	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

// This adapter is deliberately private. A future owner must acquire the App
// lifecycle -> download -> import write leases before authorize, then reconcile
// before release (and before publishing a retirement generation). It acquires
// none of those locks itself and is not a worker RPC or a running plugin.
type pluginScrapeCleanEffects struct {
	a     *App
	j     *pluginScrapeCleanJournal
	mu    sync.Mutex
	lease *pluginScrapeCleanEffectLease
}
type pluginScrapeCleanEffectLease struct {
	ctx                  context.Context
	nonce, action, runID string
	eventID              string // actual sealed running event invocation, never a worker patch.
	binding              scrapehost.Binding
	roots                []scrapehost.RootStamp
	prior                map[string]bool
}
type pluginScrapeCleanCoreReceipt struct {
	Version                                  int    `json:"version"`
	Identity                                 string `json:"identity"`
	Effect                                   []byte `json:"effect"`
	Result                                   bool   `json:"result"`
	HistoryID, FileID, ExecutionKey, AssetID string
}
type pluginScrapeCleanPrivateOutbox struct {
	Version             int    `json:"version"`
	Effect              []byte `json:"effect"`
	State               string `json:"state"`
	CreatedAt, Timezone string
}

const pluginScrapeCleanEffectsSchema = `CREATE TABLE IF NOT EXISTS plugin_scrape_core_receipts(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,token TEXT NOT NULL,sealed BLOB NOT NULL,created_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,generation,token));
CREATE TABLE IF NOT EXISTS plugin_scrape_private_outbox(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,token TEXT NOT NULL,sealed BLOB NOT NULL,created_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,generation,token));`

var _ scrapehost.Effects = (*pluginScrapeCleanEffects)(nil)

func newPluginScrapeCleanEffects(ctx context.Context, a *App, j *pluginScrapeCleanJournal) (*pluginScrapeCleanEffects, error) {
	if a == nil || j == nil || a.pluginStore != j.p || a.Store != j.p.store {
		return nil, scrapehost.ErrUnsafe
	}
	if _, err := a.Store.DB.ExecContext(ctx, pluginScrapeCleanEffectsSchema); err != nil {
		return nil, err
	}
	return &pluginScrapeCleanEffects{a: a, j: j}, nil
}
func scrapeEffectSameEntity(a, b scrapehost.Stamp) bool {
	return a.Dev == b.Dev && a.Inode == b.Inode && a.Mode == b.Mode
}
func scrapeEffectStamp(path string) (scrapehost.Stamp, error) {
	i, err := os.Lstat(path)
	if err != nil {
		return scrapehost.Stamp{}, err
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || (!i.IsDir() && !i.Mode().IsRegular()) {
		return scrapehost.Stamp{}, scrapehost.ErrUnsafe
	}
	return scrapehost.Stamp{Dev: uint64(s.Dev), Inode: uint64(s.Ino), Size: i.Size(), ModNS: i.ModTime().UnixNano(), Mode: uint32(i.Mode())}, nil
}
func scrapeEffectSnapshot(s scrapehost.Stamp) importSnapshot {
	return importSnapshot{Device: s.Dev, Inode: s.Inode, Size: s.Size, ModNS: s.ModNS}
}
func scrapeEffectRoots(roots []scrapehost.RootStamp) error {
	if len(roots) == 0 {
		return scrapehost.ErrUnsafe
	}
	for n, r := range roots {
		if r.Root.ID == "" || !importPath(r.Root.Path) || r.Root.Path == "/" || (r.Root.Role != "source" && r.Root.Role != "media") {
			return scrapehost.ErrUnsafe
		}
		f, err := importDir(r.Root.Path, false)
		if err != nil {
			return err
		}
		f.Close()
		actual, err := scrapeEffectStamp(r.Root.Path)
		if err != nil {
			return err
		}
		if !scrapeEffectSameEntity(actual, r.File) || !os.FileMode(actual.Mode).IsDir() {
			return scrapehost.ErrUnsafe
		}
		for _, p := range roots[:n] {
			if p.Root.ID == r.Root.ID || within(p.Root.Path, r.Root.Path) || within(r.Root.Path, p.Root.Path) {
				return scrapehost.ErrUnsafe
			}
		}
	}
	return nil
}
func scrapeEffectRoot(roots []scrapehost.RootStamp, path string) (scrapehost.RootStamp, error) {
	if !importPath(path) {
		return scrapehost.RootStamp{}, scrapehost.ErrUnsafe
	}
	for _, r := range roots {
		if within(r.Root.Path, path) {
			return r, nil
		}
	}
	return scrapehost.RootStamp{}, scrapehost.ErrUnsafe
}
func (e *pluginScrapeCleanEffects) coreRoots(roots []scrapehost.RootStamp) error {
	settings := e.a.Settings()
	for _, r := range roots {
		allowed := false
		if r.Root.Role == "source" {
			for _, p := range settings.AllowedRoots {
				if within(p, r.Root.Path) {
					allowed = true
					break
				}
			}
		} else {
			for _, p := range settings.Roots {
				if p.ID == r.Root.ID && p.Path == r.Root.Path {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			return scrapehost.ErrUnsafe
		}
	}
	return nil
}

// authorize is a trusted Guard hook, never proof that the caller acquired the
// App locks. A comparable original context and revocable nonce prevent a worker
// or a context wrapper from borrowing another operation's file-write lease.
func (e *pluginScrapeCleanEffects) authorize(ctx context.Context, b scrapehost.Binding, action, runID string, roots []scrapehost.RootStamp) (func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ctx == nil || !reflect.TypeOf(ctx).Comparable() || ctx.Err() != nil || e.lease != nil || b.InstanceID != e.j.instance || b.Generation != e.j.generation {
		return nil, scrapehost.ErrUnsafe
	}
	if err := scrapeEffectRoots(roots); err != nil {
		return nil, err
	}
	if err := e.coreRoots(roots); err != nil {
		return nil, err
	}
	tx, err := e.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	l := &pluginScrapeCleanEffectLease{ctx: ctx, nonce: id("scrape_lease_"), binding: b, action: action, runID: runID, roots: append([]scrapehost.RootStamp(nil), roots...), prior: map[string]bool{}}
	if _, err = e.guardTx(ctx, tx, l); err != nil {
		return nil, err
	}
	s, _, _, err := e.j.read(ctx, tx)
	if err != nil {
		return nil, err
	}
	if s.Blocked != "" {
		return nil, scrapehost.ErrUncertain
	}
	for key, x := range s.Effects {
		if x.State == "confirmed" && (x.Kind == "unlink" || x.Kind == "observed_delete" || x.Kind == "rmdir") {
			l.prior[key] = true
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	e.lease = l
	return func() {
		e.mu.Lock()
		if e.lease == l {
			e.lease = nil
			l.nonce = ""
		}
		e.mu.Unlock()
	}, nil
}
func (e *pluginScrapeCleanEffects) active(ctx context.Context) (*pluginScrapeCleanEffectLease, error) {
	if ctx == nil || !reflect.TypeOf(ctx).Comparable() || e.lease == nil || e.lease.ctx != ctx || e.lease.nonce == "" || ctx.Err() != nil {
		return nil, scrapehost.ErrUnsafe
	}
	return e.lease, nil
}
func (e *pluginScrapeCleanEffects) guardTx(ctx context.Context, tx *sql.Tx, l *pluginScrapeCleanEffectLease) (json.RawMessage, error) {
	if err := e.coreRoots(l.roots); err != nil {
		return nil, err
	}
	var plugin, version string
	var installed, enabled bool
	var generation, revision int64
	var sealed []byte
	err := tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.installed,i.desired_enabled,i.generation,v.revision,v.sealed FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, l.binding.InstanceID).Scan(&plugin, &version, &installed, &enabled, &generation, &revision, &sealed)
	if err != nil {
		return nil, err
	}
	retiring := strings.HasPrefix(l.action, "stop:") || strings.HasPrefix(l.action, "transition_retire:")
	if plugin != "ScrapeFileClean" || version != "1.0.7" || !installed || (!enabled && !retiring) || generation != l.binding.Generation || revision != l.binding.Revision {
		return nil, scrapehost.ErrUnsafe
	}
	if l.runID != "" {
		var action string
		var input []byte
		err = tx.QueryRowContext(ctx, `SELECT action,input FROM plugin_runs WHERE id=? AND instance_id=? AND version='1.0.7' AND generation=? AND config_revision=? AND state='running'`, l.runID, l.binding.InstanceID, generation, revision).Scan(&action, &input)
		if err != nil {
			return nil, err
		}
		if l.action == "apply" {
			if action != "event" {
				return nil, scrapehost.ErrUnsafe
			}
			clear, err := e.j.p.unseal("run", l.binding.InstanceID, l.runID, input)
			if err != nil {
				return nil, err
			}
			var envelope struct {
				Action string          `json:"action"`
				Input  json.RawMessage `json:"input"`
			}
			var event struct {
				ID string `json:"event_id"`
			}
			if json.Unmarshal(clear, &envelope) != nil || envelope.Action != "event" || scrapeJournalStrict(envelope.Input, &event) != nil || event.ID == "" || l.eventID != "" && l.eventID != event.ID {
				return nil, scrapehost.ErrUnsafe
			}
			l.eventID = event.ID
		} else if action != "init" && action != "reload" {
			return nil, scrapehost.ErrUnsafe
		}
	} else if l.action == "apply" {
		return nil, scrapehost.ErrUnsafe
	}
	return e.j.p.unseal("config", l.binding.InstanceID, "main", sealed)
}
func scrapeEffectEqual(a, b scrapehost.Effect) bool {
	return a.ID == b.ID && a.Scope == b.Scope && a.Kind == b.Kind && a.Path == b.Path && a.Role == b.Role && a.Binding == b.Binding && a.Expected == b.Expected && a.Stage == b.Stage && bytes.Equal(a.Payload, b.Payload)
}
func (e *pluginScrapeCleanEffects) validate(ctx context.Context, l *pluginScrapeCleanEffectLease, s scrapehost.Durable, x scrapehost.Effect, raw json.RawMessage) error {
	if x.ID == "" || x.Scope == "" || x.Binding != l.binding || x.Stage != "" {
		return scrapehost.ErrUnsafe
	}
	if err := scrapeEffectRoots(l.roots); err != nil {
		return err
	}
	if s.Blocked != "" {
		return scrapehost.ErrUncertain
	}
	if s.Binding != l.binding {
		t := s.Transition
		if l.action != "configure" || l.runID == "" || t == nil || t.Kind != "init" || t.Phase != "published" || t.From != s.Binding || t.To != l.binding || t.RunID != l.runID {
			return scrapehost.ErrUnsafe
		}
	}
	if len(s.Roots) != 0 {
		if len(s.Roots) != len(l.roots) {
			return scrapehost.ErrUnsafe
		}
		for i, r := range s.Roots {
			if r.Root != l.roots[i].Root || !scrapeEffectSameEntity(r.File, l.roots[i].File) {
				return scrapehost.ErrUnsafe
			}
		}
	}
	if s.Active == nil || s.Active.ID != x.Scope {
		return scrapehost.ErrUnsafe
	}
	switch l.action {
	case "configure":
		if l.runID == "" {
			return scrapehost.ErrUnsafe
		}
		if s.Active.Kind != "configure" {
			if err := scrapeEffectEventScope(l, s, x.Scope); err != nil {
				return err
			}
		}
	case "timer":
		if s.Active.Kind != "timer" {
			return scrapehost.ErrUnsafe
		}
	case "event", "apply", "transition_abort":
		if l.action == "apply" && (l.eventID == "" || l.eventID != x.Scope) {
			return scrapehost.ErrUnsafe
		}
		if err := scrapeEffectEventScope(l, s, x.Scope); err != nil {
			return err
		}
	default:
		if !strings.HasPrefix(l.action, "stop:") {
			return scrapehost.ErrUnsafe
		}
		if s.Active.Kind != "stop" {
			if err := scrapeEffectEventScope(l, s, x.Scope); err != nil {
				return err
			}
		}
	}
	cfg := scrapefileclean.NewConfig()
	if len(s.Config) > 0 {
		if err := cfg.Apply(s.Config); err != nil {
			return err
		}
	}
	current, err := scrapefileclean.DecodeConfiguration(raw)
	if err != nil {
		return err
	}
	if l.action != "configure" && scrapefileclean.Truth(current) {
		effective, err := scrapefileclean.DecodeConfiguration(s.Config)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, effective) {
			return scrapehost.ErrUnsafe
		}
	}
	if err := cfg.Apply(raw); err != nil {
		return err
	}
	switch x.Kind {
	case "history_src", "history_dest":
		if !scrapefileclean.Truth(cfg.History) || string(x.Payload) != "null" {
			return scrapehost.ErrUnsafe
		}
	case "download_file_deleted":
		var p struct {
			Src string `json:"src"`
		}
		if !scrapefileclean.Truth(cfg.Torrents) || scrapeJournalStrict(x.Payload, &p) != nil || p.Src != x.Path {
			return scrapehost.ErrUnsafe
		}
	case "notification":
		var p struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		}
		if !scrapefileclean.Truth(cfg.Notify) || scrapeJournalStrict(x.Payload, &p) != nil || x.Path != l.roots[0].Root.Path || x.Expected != (scrapehost.Pin{}) {
			return scrapehost.ErrUnsafe
		}
	default:
		return scrapehost.ErrUnsupported
	}
	r, err := scrapeEffectRoot(l.roots, x.Path)
	if err != nil || r.Root.Role != x.Role {
		return scrapehost.ErrUnsafe
	}
	if x.Kind == "notification" {
		return nil
	}
	if x.Expected.RootID != r.Root.ID || x.Expected.ParentPath != filepath.Dir(x.Path) || x.Expected.File.Inode == 0 {
		return scrapehost.ErrUnsafe
	}
	p, ok := s.Frozen[x.Path]
	if !ok {
		p, ok = s.Pins[x.Path]
	}
	if !ok || p != x.Expected {
		return scrapehost.ErrUnsafe
	}
	return nil
}
func scrapeEffectEventScope(l *pluginScrapeCleanEffectLease, s scrapehost.Durable, scope string) error {
	r, ok := s.Events[scope]
	if !ok || r.State != "planned" || s.Active == nil || r.Event != *s.Active {
		return scrapehost.ErrUnsafe
	}
	if r.Binding == l.binding {
		return nil
	}
	t := s.Transition
	if l.action == "configure" && t != nil && t.Kind == "init" && t.Phase == "published" && t.From == r.Binding && t.To == l.binding && t.RunID == l.runID {
		return nil
	}
	return scrapehost.ErrUnsafe
}
func (e *pluginScrapeCleanEffects) token(id string) string {
	return hex.EncodeToString(e.j.mac("core-effect-key", []byte(id)))
}
func (e *pluginScrapeCleanEffects) aad(kind, token string) string {
	return "core-effects:v1:" + strconv.FormatInt(e.j.generation, 10) + ":" + kind + ":" + token
}
func (e *pluginScrapeCleanEffects) identity(x scrapehost.Effect) (string, []byte, error) {
	x.State = ""
	x.Error = ""
	x.Result = false
	// Payload is an opaque byte slot in the journal, not reserialized JSON.
	payload := x.Payload
	x.Payload = nil
	raw, err := json.Marshal(struct {
		Effect  scrapehost.Effect
		Payload []byte
	}{x, payload})
	if err != nil || len(raw) > pluginValueLimit {
		return "", nil, scrapehost.ErrUnsafe
	}
	return hex.EncodeToString(e.j.mac("core-effect-identity", raw)), raw, nil
}
func (e *pluginScrapeCleanEffects) receipt(ctx context.Context, q pluginScrapeCleanJournalReader, x scrapehost.Effect) (pluginScrapeCleanCoreReceipt, error) {
	var out pluginScrapeCleanCoreReceipt
	var sealed []byte
	token := e.token(x.ID)
	err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_scrape_core_receipts WHERE instance_id=? AND generation=? AND token=?`, e.j.instance, e.j.generation, token).Scan(&sealed)
	if err != nil {
		return out, err
	}
	raw, err := e.j.p.unseal("scrape_journal", e.j.instance, e.aad("receipt", token), sealed)
	if err != nil {
		return out, err
	}
	if err = scrapeJournalStrict(raw, &out); err != nil {
		return out, err
	}
	identity, original, err := e.identity(x)
	if err != nil {
		return out, err
	}
	if out.Version != 1 || out.Identity != identity || !bytes.Equal(out.Effect, original) {
		return out, scrapehost.ErrUnsafe
	}
	return out, nil
}
func (e *pluginScrapeCleanEffects) saveReceipt(ctx context.Context, tx *sql.Tx, x scrapehost.Effect, r pluginScrapeCleanCoreReceipt) error {
	identity, original, err := e.identity(x)
	if err != nil {
		return err
	}
	r.Version = 1
	r.Identity = identity
	r.Effect = original
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	token := e.token(x.ID)
	sealed, err := e.j.p.seal("scrape_journal", e.j.instance, e.aad("receipt", token), raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_scrape_core_receipts VALUES(?,?,?,?,?)`, e.j.instance, e.j.generation, token, sealed, now())
	return err
}

func (e *pluginScrapeCleanEffects) Commit(ctx context.Context, x scrapehost.Effect) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, err := e.active(ctx)
	if err != nil {
		return false, err
	}
	tx, err := e.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	raw, err := e.guardTx(ctx, tx, l)
	if err != nil {
		return false, err
	}
	s, _, _, err := e.j.read(ctx, tx)
	if err != nil {
		return false, err
	}
	planned, ok := s.Effects[x.ID]
	if !ok || !scrapeEffectEqual(planned, x) || (planned.State != "planned" && planned.State != "uncertain" && planned.State != "confirmed") {
		return false, scrapehost.ErrUnsafe
	}
	if err = e.validate(ctx, l, s, x, raw); err != nil {
		return false, err
	}
	prior, err := e.receipt(ctx, tx, x)
	if err == nil {
		return prior.Result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if planned.State != "planned" {
		return false, scrapehost.ErrUncertain
	}
	r := pluginScrapeCleanCoreReceipt{}
	if x.Kind == "history_src" || x.Kind == "history_dest" {
		r, err = e.history(ctx, tx, x)
	} else {
		r.Result = true
		var original []byte
		_, original, err = e.identity(x)
		if err == nil {
			var sealed []byte
			token := e.token(x.ID)
			zone, zoneErr := autoBackupHostTimezone()
			if zoneErr != nil {
				return false, zoneErr
			}
			created := now()
			data, _ := json.Marshal(pluginScrapeCleanPrivateOutbox{Version: 1, Effect: original, State: "pending", CreatedAt: created, Timezone: zone})
			sealed, err = e.j.p.seal("scrape_journal", e.j.instance, e.aad("outbox", token), data)
			if err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO plugin_scrape_private_outbox VALUES(?,?,?,?,?)`, e.j.instance, e.j.generation, token, sealed, created)
			}
		}
	}
	if err != nil {
		return false, err
	}
	if err = e.reconcileTx(ctx, tx, l, s); err != nil {
		return false, err
	}
	if err = e.saveReceipt(ctx, tx, x, r); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, scrapehost.ErrUncertain
	}
	return r.Result, nil
}

func scrapeEffectsGet(ctx context.Context, tx *sql.Tx, kind, key string, out any) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, kind, key).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
func (e *pluginScrapeCleanEffects) currentVideo(ctx context.Context, tx *sql.Tx, plan ImportPlan, f PlanFile) (Asset, managedFile, error) {
	var asset Asset
	var own managedFile
	if err := scrapeEffectsGet(ctx, tx, "asset", f.AssetID, &asset); err != nil {
		return asset, own, err
	}
	if err := scrapeEffectsGet(ctx, tx, "managed_file", tokenHash(f.Target), &own); err != nil {
		return asset, own, err
	}
	if asset.ID != f.AssetID || asset.RootID != plan.RootID || asset.Path != f.Target || asset.State == "removed" || asset.State == "superseded" || own.Kind != "video" || own.Path != f.Target || own.AssetID != asset.ID || own.RootID != asset.RootID || own.MediaKey != mediaKey(asset.Media) || own.SourceSnapshot != planSnapshot(f) {
		return asset, own, scrapehost.ErrUnsafe
	}
	return asset, own, nil
}
func (e *pluginScrapeCleanEffects) history(ctx context.Context, tx *sql.Tx, x scrapehost.Effect) (pluginScrapeCleanCoreReceipt, error) {
	var out pluginScrapeCleanCoreReceipt
	where := `json_extract(file,'$.source')=? OR resource_source=?`
	if x.Kind == "history_dest" {
		where = `json_extract(file,'$.target')=? OR 0=?`
	}
	second := any(x.Path)
	if x.Kind == "history_dest" {
		second = 1
	}
	rows, err := tx.QueryContext(ctx, transferHistoryCTE+`SELECT `+transferHistoryColumns+` FROM history WHERE (`+where+`)`, x.Path, second)
	if err != nil {
		return out, err
	}
	items := []transferHistoryItem{}
	for rows.Next() {
		item, readErr := scanTransferHistory(rows)
		if readErr != nil {
			rows.Close()
			return out, readErr
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(items) == 0 {
		return out, nil
	}
	if len(items) != 1 {
		return out, scrapehost.ErrUnsafe
	}
	h := items[0]
	var plan ImportPlan
	if err = scrapeEffectsGet(ctx, tx, "import_plan", h.PlanID, &plan); err != nil {
		return out, err
	}
	var file *PlanFile
	for i := range plan.Files {
		if plan.Files[i].ID == h.executionFileID() {
			if file != nil {
				return out, scrapehost.ErrUnsafe
			}
			file = &plan.Files[i]
		}
	}
	if file == nil || file.Source != h.Source || file.Target != h.Target {
		return out, scrapehost.ErrUnsafe
	}
	var own managedFile
	if x.Kind == "history_dest" || file.Source != x.Path {
		_, own, err = e.currentVideo(ctx, tx, plan, *file)
		if err != nil {
			return out, err
		}
	}
	if x.Kind == "history_dest" {
		if x.Role != "media" || own.Snapshot != scrapeEffectSnapshot(x.Expected.File) {
			return out, scrapehost.ErrUnsafe
		}
	} else if err = e.sourceProof(ctx, tx, plan, *file, x.Path, scrapeEffectSnapshot(x.Expected.File), h.ExecutionKey, h.ResourceSource, map[string]bool{}); err != nil {
		return out, err
	}
	if err = txPut(ctx, tx, "transfer_history_hidden", h.ID, map[string]any{"at": now(), "plan_id": h.PlanID, "file_id": h.executionFileID(), "execution_key": h.ExecutionKey, "delete_source": false, "delete_target": false}); err != nil {
		return out, err
	}
	out.Result = true
	out.HistoryID = h.ID
	out.FileID = h.executionFileID()
	out.ExecutionKey = h.ExecutionKey
	return out, nil
}

// Walk only frozen reorganization ancestry, never arbitrary rows sharing a
// source string or the stable display history ID. Imported files in partially
// failed multi-file jobs remain legitimate committed executions.
func (e *pluginScrapeCleanEffects) sourceProof(ctx context.Context, tx *sql.Tx, plan ImportPlan, f PlanFile, path string, expected importSnapshot, fence, resource string, seen map[string]bool) error {
	proof := &scrapeSourceProofBudget{remaining: 128, memo: map[scrapeSourceProofKey]bool{}}
	return e.sourceProofBranch(ctx, tx, plan, f, path, expected, fence, resource, seen, proof)
}

type scrapeSourceProofKey struct {
	PlanID, FileID, Path, Fence, Resource string
	Expected                              importSnapshot
}
type scrapeSourceProofBudget struct {
	remaining int
	memo      map[scrapeSourceProofKey]bool
}

func (e *pluginScrapeCleanEffects) sourceProofBranch(ctx context.Context, tx *sql.Tx, plan ImportPlan, f PlanFile, path string, expected importSnapshot, fence, resource string, seen map[string]bool, proof *scrapeSourceProofBudget) error {
	key := plan.ID + "/" + f.ID
	if seen[key] || proof.remaining <= 0 {
		return scrapehost.ErrUnsafe
	}
	proof.remaining--
	memo := scrapeSourceProofKey{PlanID: plan.ID, FileID: f.ID, Path: path, Fence: fence, Resource: resource, Expected: expected}
	if proof.memo[memo] {
		return nil
	}
	seen[key] = true
	defer delete(seen, key)
	if f.Source == path {
		if planSnapshot(f) == expected {
			proof.memo[memo] = true
			return nil
		}
		return scrapehost.ErrUnsafe
	}
	var meta importMetadata
	if err := scrapeEffectsGet(ctx, tx, "import_meta", plan.ID, &meta); err != nil {
		return err
	}
	r := meta.Reorganize
	if r == nil || f.Source != r.Asset.Path || planSnapshot(f) != r.Video.Snapshot || r.Video.Kind != "video" || r.Video.AssetID != r.Asset.ID || r.Video.Path != r.Asset.Path || r.Video.RootID != r.Asset.RootID || r.Video.MediaKey != mediaKey(r.Asset.Media) {
		return scrapehost.ErrUnsafe
	}
	rows, err := tx.QueryContext(ctx, transferHistoryBaseCTE+`SELECT plan,file,execution_key,resource_source FROM attempts WHERE json_extract(file,'$.asset_id')=?`, r.Asset.ID)
	if err != nil {
		return err
	}
	type candidate struct {
		p                   ImportPlan
		f                   PlanFile
		execution, resource string
	}
	var candidates []candidate
	for rows.Next() {
		var pRaw, fRaw []byte
		var execution, source string
		if err = rows.Scan(&pRaw, &fRaw, &execution, &source); err != nil {
			rows.Close()
			return err
		}
		var c candidate
		c.execution = execution
		c.resource = source
		if json.Unmarshal(pRaw, &c.p) != nil || json.Unmarshal(fRaw, &c.f) != nil {
			rows.Close()
			return scrapehost.ErrUnsafe
		}
		// Repeated already_imported submissions legitimately reuse AssetID.
		// Filter by the exact frozen owner, source fingerprint and execution
		// fence before deciding whether the remaining branches are equivalent.
		if c.execution == "" || fence == "" || c.execution >= fence || c.resource != resource || c.f.AssetID != r.Asset.ID || c.f.Target != r.Asset.Path || c.p.RootID != r.Asset.RootID || planSnapshot(c.f) != r.Video.SourceSnapshot || mediaKey(c.p.Media) != mediaKey(r.Asset.Media) || (c.f.Status != "imported" && c.f.Status != "already_imported") {
			continue
		}
		if len(candidates) >= proof.remaining {
			rows.Close()
			return scrapehost.ErrUnsafe
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return scrapehost.ErrUnsafe
	}
	for _, old := range candidates {
		if old.f.Source != candidates[0].f.Source || planSnapshot(old.f) != planSnapshot(candidates[0].f) {
			return scrapehost.ErrUnsafe
		}
		// Every eligible branch must prove the same requested origin. Choosing
		// one successful old row would conceal corrupt or conflicting ancestry.
		if err := e.sourceProofBranch(ctx, tx, old.p, old.f, path, expected, old.execution, resource, seen, proof); err != nil {
			return err
		}
	}
	proof.memo[memo] = true
	return nil
}
func scrapeEffectFileFact(x scrapehost.Effect) bool {
	return x.State == "confirmed" && (x.Kind == "unlink" || x.Kind == "observed_delete") && os.FileMode(x.Expected.File.Mode).IsRegular()
}

// ReconcileConfirmedFiles is required even when every optional source side
// effect is disabled. It performs no filesystem deletion and no reacquisition.
func (e *pluginScrapeCleanEffects) ReconcileConfirmedFiles(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, err := e.active(ctx)
	if err != nil {
		return err
	}
	return e.reconcileLocked(ctx, l)
}

// afterAction is the actual mandatory App callback. The host must propagate
// its error before clearing Active or publishing a lifecycle completion.
func (e *pluginScrapeCleanEffects) afterAction(ctx context.Context, b scrapehost.Binding, action string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, err := e.active(ctx)
	if err != nil {
		return err
	}
	if l.binding != b || l.action != action {
		return scrapehost.ErrUnsafe
	}
	return e.reconcileLocked(ctx, l)
}
func (e *pluginScrapeCleanEffects) reconcileLocked(ctx context.Context, l *pluginScrapeCleanEffectLease) error {
	tx, err := e.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = e.guardTx(ctx, tx, l); err != nil {
		return err
	}
	s, _, _, err := e.j.read(ctx, tx)
	if err != nil {
		return err
	}
	if err = e.reconcileTx(ctx, tx, l, s); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return scrapehost.ErrUncertain
	}
	return nil
}
func (e *pluginScrapeCleanEffects) reconcileTx(ctx context.Context, tx *sql.Tx, l *pluginScrapeCleanEffectLease, s scrapehost.Durable) error {
	if err := scrapeEffectRoots(l.roots); err != nil {
		return err
	}
	if s.Binding != l.binding && s.Binding != (scrapehost.Binding{}) {
		t := s.Transition
		if l.action != "configure" || t == nil || t.Kind != "init" || t.Phase != "published" || t.From != s.Binding || t.To != l.binding || t.RunID != l.runID {
			return scrapehost.ErrUnsafe
		}
	}
	if len(s.Roots) > 0 {
		if len(s.Roots) != len(l.roots) {
			return scrapehost.ErrUnsafe
		}
		for i, r := range s.Roots {
			if r.Root != l.roots[i].Root || !scrapeEffectSameEntity(r.File, l.roots[i].File) {
				return scrapehost.ErrUnsafe
			}
		}
	}
	for key, fact := range s.Effects {
		if !scrapeEffectFileFact(fact) || fact.Role != "media" {
			continue
		}
		x := fact
		x.ID = "file-index:" + fact.ID
		if _, err := e.receipt(ctx, tx, x); err == nil {
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if l.prior[key] {
			return scrapehost.ErrUncertain
		}
		if fact.ID != key || fact.Binding != l.binding || fact.Expected.ParentPath != filepath.Dir(fact.Path) {
			return scrapehost.ErrUnsafe
		}
		r, err := scrapeEffectRoot(l.roots, fact.Path)
		if err != nil || r.Root.Role != "media" || r.Root.ID != fact.Expected.RootID {
			return scrapehost.ErrUnsafe
		}
		if err = e.confirmedParent(l, s, r, fact.Expected.ParentPath, fact.Expected.Parent, map[string]bool{}); err != nil {
			return err
		}
		_, err = os.Lstat(fact.Path)
		if err == nil {
			return scrapehost.ErrUnsafe
		}
		if !os.IsNotExist(err) {
			return err
		}
		var own managedFile
		out := pluginScrapeCleanCoreReceipt{}
		err = scrapeEffectsGet(ctx, tx, "managed_file", tokenHash(fact.Path), &own)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && own.Kind == "video" && own.Path == fact.Path && own.RootID == r.Root.ID && own.Snapshot == scrapeEffectSnapshot(fact.Expected.File) {
			var asset Asset
			if err = scrapeEffectsGet(ctx, tx, "asset", own.AssetID, &asset); err != nil {
				return err
			}
			if asset.ID == own.AssetID && asset.Path == own.Path && asset.RootID == own.RootID && own.MediaKey == mediaKey(asset.Media) && asset.State != "superseded" && asset.State != "removed" {
				asset.State = "missing_pending"
				asset.FNOSState = "needs_attention"
				asset.MissingSeenAt = now()
				asset.Error = "清理操作已确认媒体文件删除，保留身份与占用，等待核对"
				if err = txPut(ctx, tx, "asset", asset.ID, asset); err != nil {
					return err
				}
				out.Result = true
				out.AssetID = asset.ID
			}
		}
		if err = e.saveReceipt(ctx, tx, x, out); err != nil {
			return err
		}
	}
	return nil
}

// Empty-directory cleanup can follow unlink before the owner's final hook.
// Only a new confirmed rmdir chain can replace the canonical-parent check;
// arbitrary missing/inaccessible parents are not proof of this file's removal.
func (e *pluginScrapeCleanEffects) confirmedParent(l *pluginScrapeCleanEffectLease, s scrapehost.Durable, root scrapehost.RootStamp, path string, expected scrapehost.Stamp, seen map[string]bool) error {
	if seen[path] || len(seen) > 128 || !within(root.Root.Path, path) {
		return scrapehost.ErrUnsafe
	}
	seen[path] = true
	f, err := importDir(path, false)
	if err == nil {
		f.Close()
		actual, err := scrapeEffectStamp(path)
		if err != nil {
			return err
		}
		if !scrapeEffectSameEntity(actual, expected) {
			return scrapehost.ErrUnsafe
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if path == root.Root.Path {
		return scrapehost.ErrUnsafe
	}
	var matched *scrapehost.Effect
	for key, x := range s.Effects {
		if x.Kind != "rmdir" || x.State != "confirmed" || x.Path != path {
			continue
		}
		if l.prior[key] {
			return scrapehost.ErrUncertain
		}
		if matched != nil || x.ID != key || x.Binding != l.binding || x.Role != root.Root.Role || x.Expected.RootID != root.Root.ID || x.Expected.ParentPath != filepath.Dir(path) || !os.FileMode(x.Expected.File.Mode).IsDir() || !scrapeEffectSameEntity(x.Expected.File, expected) {
			return scrapehost.ErrUnsafe
		}
		copy := x
		matched = &copy
	}
	if matched == nil {
		return scrapehost.ErrUncertain
	}
	return e.confirmedParent(l, s, root, matched.Expected.ParentPath, matched.Expected.Parent, seen)
}
