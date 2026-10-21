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

	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

// This adapter is deliberately private. A future owner must acquire the App
// lifecycle -> download -> import write leases before authorize, then reconcile
// before release (and before publishing a retirement generation). It acquires
// none of those locks itself and is not a worker RPC or a running plugin.
type pluginRemoveLinkEffects struct {
	a     *App
	j     *pluginRemoveLinkJournal
	mu    sync.Mutex
	lease *pluginRemoveLinkEffectLease
}
type pluginRemoveLinkEffectLease struct {
	ctx                  context.Context
	nonce, action, runID string
	eventID              string // actual sealed running event invocation, never a worker patch.
	binding              cleanuphost.Binding
	roots                []cleanuphost.RootStamp
	prior                map[string]bool
}
type pluginRemoveLinkCoreReceipt struct {
	Version                                  int    `json:"version"`
	Identity                                 string `json:"identity"`
	Effect                                   []byte `json:"effect"`
	Result                                   bool   `json:"result"`
	HistoryID, FileID, ExecutionKey, AssetID string
	NoticeAcceptanceSHA                      string `json:",omitempty"`
}
type pluginRemoveLinkPrivateOutbox struct {
	Version int                         `json:"version"`
	Effect  []byte                      `json:"effect"`
	State   string                      `json:"state"`
	Notice  *removeLinkNoticeAcceptance `json:",omitempty"`
}

const pluginRemoveLinkEffectsSchema = `CREATE TABLE IF NOT EXISTS plugin_cleanup_core_receipts(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,token TEXT NOT NULL,sealed BLOB NOT NULL,created_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,generation,token));
CREATE TABLE IF NOT EXISTS plugin_cleanup_private_outbox(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,token TEXT NOT NULL,sealed BLOB NOT NULL,created_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,generation,token));`

var _ cleanuphost.Effects = (*pluginRemoveLinkEffects)(nil)

func newPluginRemoveLinkEffects(ctx context.Context, a *App, j *pluginRemoveLinkJournal) (*pluginRemoveLinkEffects, error) {
	if a == nil || j == nil || a.pluginStore != j.p || a.Store != j.p.store {
		return nil, cleanuphost.ErrUnsafe
	}
	if _, err := a.Store.DB.ExecContext(ctx, pluginRemoveLinkEffectsSchema); err != nil {
		return nil, err
	}
	return &pluginRemoveLinkEffects{a: a, j: j}, nil
}
func cleanupEffectSameEntity(a, b cleanuphost.Stamp) bool {
	return a.Dev == b.Dev && a.Inode == b.Inode && a.Mode == b.Mode
}
func cleanupEffectStamp(path string) (cleanuphost.Stamp, error) {
	i, err := os.Lstat(path)
	if err != nil {
		return cleanuphost.Stamp{}, err
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || (!i.IsDir() && !i.Mode().IsRegular()) {
		return cleanuphost.Stamp{}, cleanuphost.ErrUnsafe
	}
	return cleanuphost.Stamp{Dev: uint64(s.Dev), Inode: uint64(s.Ino), Size: i.Size(), ModNS: i.ModTime().UnixNano(), Mode: uint32(i.Mode())}, nil
}
func cleanupEffectSnapshot(s cleanuphost.Stamp) importSnapshot {
	return importSnapshot{Device: s.Dev, Inode: s.Inode, Size: s.Size, ModNS: s.ModNS}
}
func cleanupEffectRoots(roots []cleanuphost.RootStamp) error {
	if len(roots) == 0 {
		return cleanuphost.ErrUnsafe
	}
	for n, r := range roots {
		if r.Root.ID == "" || !importPath(r.Root.Path) || r.Root.Path == "/" || (r.Root.Role != "source" && r.Root.Role != "media") {
			return cleanuphost.ErrUnsafe
		}
		f, err := importDir(r.Root.Path, false)
		if err != nil {
			return err
		}
		f.Close()
		actual, err := cleanupEffectStamp(r.Root.Path)
		if err != nil {
			return err
		}
		if !cleanupEffectSameEntity(actual, r.File) || !os.FileMode(actual.Mode).IsDir() {
			return cleanuphost.ErrUnsafe
		}
		for _, p := range roots[:n] {
			if p.Root.ID == r.Root.ID || within(p.Root.Path, r.Root.Path) || within(r.Root.Path, p.Root.Path) {
				return cleanuphost.ErrUnsafe
			}
		}
	}
	return nil
}
func cleanupEffectRoot(roots []cleanuphost.RootStamp, path string) (cleanuphost.RootStamp, error) {
	if !importPath(path) {
		return cleanuphost.RootStamp{}, cleanuphost.ErrUnsafe
	}
	for _, r := range roots {
		if within(r.Root.Path, path) {
			return r, nil
		}
	}
	return cleanuphost.RootStamp{}, cleanuphost.ErrUnsafe
}
func (e *pluginRemoveLinkEffects) coreRoots(roots []cleanuphost.RootStamp) error {
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
			return cleanuphost.ErrUnsafe
		}
	}
	return nil
}

// authorize is a trusted Guard hook, never proof that the caller acquired the
// App locks. A comparable original context and revocable nonce prevent a worker
// or a context wrapper from borrowing another operation's file-write lease.
func (e *pluginRemoveLinkEffects) authorize(ctx context.Context, b cleanuphost.Binding, action, runID string, roots []cleanuphost.RootStamp) (func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ctx == nil || !reflect.TypeOf(ctx).Comparable() || ctx.Err() != nil || e.lease != nil || b.InstanceID != e.j.instance || b.Generation != e.j.generation {
		return nil, cleanuphost.ErrUnsafe
	}
	if err := cleanupEffectRoots(roots); err != nil {
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
	l := &pluginRemoveLinkEffectLease{ctx: ctx, nonce: id("cleanup_lease_"), binding: b, action: action, runID: runID, roots: append([]cleanuphost.RootStamp(nil), roots...), prior: map[string]bool{}}
	if _, err = e.guardTx(ctx, tx, l); err != nil {
		return nil, err
	}
	s, _, _, err := e.j.read(ctx, tx)
	if err != nil {
		return nil, err
	}
	if s.Blocked != "" {
		return nil, cleanuphost.ErrUncertain
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
func (e *pluginRemoveLinkEffects) active(ctx context.Context) (*pluginRemoveLinkEffectLease, error) {
	if ctx == nil || !reflect.TypeOf(ctx).Comparable() || e.lease == nil || e.lease.ctx != ctx || e.lease.nonce == "" || ctx.Err() != nil {
		return nil, cleanuphost.ErrUnsafe
	}
	return e.lease, nil
}
func (e *pluginRemoveLinkEffects) guardTx(ctx context.Context, tx *sql.Tx, l *pluginRemoveLinkEffectLease) (json.RawMessage, error) {
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
	if plugin != "RemoveLink" || version != "2.17" || !installed || (!enabled && !retiring) || generation != l.binding.Generation || revision != l.binding.Revision {
		return nil, cleanuphost.ErrUnsafe
	}
	if l.runID != "" {
		var action string
		var input []byte
		err = tx.QueryRowContext(ctx, `SELECT action,input FROM plugin_runs WHERE id=? AND instance_id=? AND version='2.17' AND generation=? AND config_revision=? AND state='running'`, l.runID, l.binding.InstanceID, generation, revision).Scan(&action, &input)
		if err != nil {
			return nil, err
		}
		if l.action == "apply" {
			if action != "event" {
				return nil, cleanuphost.ErrUnsafe
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
			if json.Unmarshal(clear, &envelope) != nil || envelope.Action != "event" || cleanupJournalStrict(envelope.Input, &event) != nil || event.ID == "" || l.eventID != "" && l.eventID != event.ID {
				return nil, cleanuphost.ErrUnsafe
			}
			l.eventID = event.ID
		} else if action != "init" && action != "reload" {
			return nil, cleanuphost.ErrUnsafe
		}
	} else if l.action == "apply" {
		return nil, cleanuphost.ErrUnsafe
	}
	return e.j.p.unseal("config", l.binding.InstanceID, "main", sealed)
}
func cleanupEffectEqual(a, b cleanuphost.Effect) bool {
	return a.ID == b.ID && a.Scope == b.Scope && a.Kind == b.Kind && a.Path == b.Path && a.Role == b.Role && a.Binding == b.Binding && a.Expected == b.Expected && a.Stage == b.Stage && bytes.Equal(a.Payload, b.Payload)
}
func (e *pluginRemoveLinkEffects) validate(ctx context.Context, l *pluginRemoveLinkEffectLease, s cleanuphost.Durable, x cleanuphost.Effect, raw json.RawMessage) error {
	if x.ID == "" || x.Scope == "" || x.Binding != l.binding || x.Stage != "" {
		return cleanuphost.ErrUnsafe
	}
	if err := cleanupEffectRoots(l.roots); err != nil {
		return err
	}
	if s.Blocked != "" {
		return cleanuphost.ErrUncertain
	}
	if s.Binding != l.binding {
		t := s.Transition
		if l.action != "configure" || l.runID == "" || t == nil || t.Kind != "init" || t.Phase != "published" || t.From != s.Binding || t.To != l.binding || t.RunID != l.runID {
			return cleanuphost.ErrUnsafe
		}
	}
	if len(s.Roots) != 0 {
		if len(s.Roots) != len(l.roots) {
			return cleanuphost.ErrUnsafe
		}
		for i, r := range s.Roots {
			if r.Root != l.roots[i].Root || !cleanupEffectSameEntity(r.File, l.roots[i].File) {
				return cleanuphost.ErrUnsafe
			}
		}
	}
	if s.Active == nil || s.Active.ID != x.Scope {
		return cleanuphost.ErrUnsafe
	}
	switch l.action {
	case "configure":
		if l.runID == "" {
			return cleanuphost.ErrUnsafe
		}
		if s.Active.Kind != "configure" {
			if err := cleanupEffectEventScope(l, s, x.Scope); err != nil {
				return err
			}
		}
	case "timer":
		if s.Active.Kind != "timer" {
			return cleanuphost.ErrUnsafe
		}
	case "event", "apply", "transition_abort":
		if l.action == "apply" && (l.eventID == "" || l.eventID != x.Scope) {
			return cleanuphost.ErrUnsafe
		}
		if err := cleanupEffectEventScope(l, s, x.Scope); err != nil {
			return err
		}
	default:
		if !strings.HasPrefix(l.action, "stop:") {
			return cleanuphost.ErrUnsafe
		}
		if s.Active.Kind != "stop" {
			if err := cleanupEffectEventScope(l, s, x.Scope); err != nil {
				return err
			}
		}
	}
	cfg := removelink.NewConfig()
	if len(s.Config) > 0 {
		if err := cfg.Apply(s.Config); err != nil {
			return err
		}
	}
	current, err := removelink.Decode(raw)
	if err != nil {
		return err
	}
	if l.action != "configure" && removelink.Truth(current) {
		effective, err := removelink.Decode(s.Config)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, effective) {
			return cleanuphost.ErrUnsafe
		}
	}
	if err := cfg.Apply(raw); err != nil {
		return err
	}
	switch x.Kind {
	case "history_src", "history_dest":
		if !removelink.Truth(cfg.History) || string(x.Payload) != "null" {
			return cleanuphost.ErrUnsafe
		}
	case "download_file_deleted":
		var p struct {
			Src string `json:"src"`
		}
		if !removelink.Truth(cfg.Torrents) || cleanupJournalStrict(x.Payload, &p) != nil || p.Src != x.Path {
			return cleanuphost.ErrUnsafe
		}
	case "notification":
		var p struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		}
		if !removelink.Truth(cfg.Notify) || cleanupJournalStrict(x.Payload, &p) != nil || x.Path != l.roots[0].Root.Path || x.Expected != (cleanuphost.Pin{}) {
			return cleanuphost.ErrUnsafe
		}
	default:
		return cleanuphost.ErrUnsupported
	}
	r, err := cleanupEffectRoot(l.roots, x.Path)
	if err != nil || r.Root.Role != x.Role {
		return cleanuphost.ErrUnsafe
	}
	if x.Kind == "notification" {
		return nil
	}
	if x.Expected.RootID != r.Root.ID || x.Expected.ParentPath != filepath.Dir(x.Path) || x.Expected.File.Inode == 0 {
		return cleanuphost.ErrUnsafe
	}
	p, ok := s.Frozen[x.Path]
	if !ok {
		p, ok = s.Pins[x.Path]
	}
	if !ok || p != x.Expected {
		return cleanuphost.ErrUnsafe
	}
	return nil
}
func cleanupEffectEventScope(l *pluginRemoveLinkEffectLease, s cleanuphost.Durable, scope string) error {
	r, ok := s.Events[scope]
	if !ok || r.State != "planned" || s.Active == nil || r.Event != *s.Active {
		return cleanuphost.ErrUnsafe
	}
	if r.Binding == l.binding {
		return nil
	}
	t := s.Transition
	if l.action == "configure" && t != nil && t.Kind == "init" && t.Phase == "published" && t.From == r.Binding && t.To == l.binding && t.RunID == l.runID {
		return nil
	}
	return cleanuphost.ErrUnsafe
}
func (e *pluginRemoveLinkEffects) token(id string) string {
	return hex.EncodeToString(e.j.mac("core-effect-key", []byte(id)))
}
func (e *pluginRemoveLinkEffects) aad(kind, token string) string {
	return "core-effects:v1:" + strconv.FormatInt(e.j.generation, 10) + ":" + kind + ":" + token
}
func (e *pluginRemoveLinkEffects) identity(x cleanuphost.Effect) (string, []byte, error) {
	x.State = ""
	x.Error = ""
	x.Result = false
	// Payload is an opaque byte slot in the journal, not reserialized JSON.
	payload := x.Payload
	x.Payload = nil
	raw, err := json.Marshal(struct {
		Effect  cleanuphost.Effect
		Payload []byte
	}{x, payload})
	if err != nil || len(raw) > pluginValueLimit {
		return "", nil, cleanuphost.ErrUnsafe
	}
	return hex.EncodeToString(e.j.mac("core-effect-identity", raw)), raw, nil
}
func (e *pluginRemoveLinkEffects) receipt(ctx context.Context, q pluginRemoveLinkJournalReader, x cleanuphost.Effect) (pluginRemoveLinkCoreReceipt, error) {
	var out pluginRemoveLinkCoreReceipt
	var sealed []byte
	token := e.token(x.ID)
	err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_cleanup_core_receipts WHERE instance_id=? AND generation=? AND token=?`, e.j.instance, e.j.generation, token).Scan(&sealed)
	if err != nil {
		return out, err
	}
	raw, err := e.j.p.unseal("cleanup_journal", e.j.instance, e.aad("receipt", token), sealed)
	if err != nil {
		return out, err
	}
	if err = cleanupJournalStrict(raw, &out); err != nil {
		return out, err
	}
	identity, original, err := e.identity(x)
	if err != nil {
		return out, err
	}
	if out.Version != 1 || out.Identity != identity || !bytes.Equal(out.Effect, original) {
		return out, cleanuphost.ErrUnsafe
	}
	if x.Kind == "notification" {
		var sealed []byte
		if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_cleanup_private_outbox WHERE instance_id=? AND generation=? AND token=?`, e.j.instance, e.j.generation, token).Scan(&sealed); err != nil {
			return out, err
		}
		raw, err := e.j.p.unseal("cleanup_journal", e.j.instance, e.aad("outbox", token), sealed)
		if err != nil {
			return out, err
		}
		var box pluginRemoveLinkPrivateOutbox
		if cleanupJournalStrict(raw, &box) != nil || !bytes.Equal(box.Effect, original) {
			return out, cleanuphost.ErrUnsafe
		}
		sha, err := removeLinkNoticeAcceptanceSHA(box, x)
		if err != nil || out.NoticeAcceptanceSHA != sha {
			return out, cleanuphost.ErrUnsafe
		}
	} else if out.NoticeAcceptanceSHA != "" {
		return out, cleanuphost.ErrUnsafe
	}
	return out, nil
}
func (e *pluginRemoveLinkEffects) saveReceipt(ctx context.Context, tx *sql.Tx, x cleanuphost.Effect, r pluginRemoveLinkCoreReceipt) error {
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
	sealed, err := e.j.p.seal("cleanup_journal", e.j.instance, e.aad("receipt", token), raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_cleanup_core_receipts VALUES(?,?,?,?,?)`, e.j.instance, e.j.generation, token, sealed, now())
	return err
}

func (e *pluginRemoveLinkEffects) Commit(ctx context.Context, x cleanuphost.Effect) (bool, error) {
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
	if !ok || !cleanupEffectEqual(planned, x) || (planned.State != "planned" && planned.State != "uncertain" && planned.State != "confirmed") {
		return false, cleanuphost.ErrUnsafe
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
		return false, cleanuphost.ErrUncertain
	}
	r := pluginRemoveLinkCoreReceipt{}
	if x.Kind == "history_src" || x.Kind == "history_dest" {
		r, err = e.history(ctx, tx, x)
	} else {
		r.Result = true
		var original []byte
		_, original, err = e.identity(x)
		if err == nil {
			var sealed []byte
			token := e.token(x.ID)
			box := pluginRemoveLinkPrivateOutbox{Version: 1, Effect: original, State: "pending"}
			created := now()
			if x.Kind == "notification" {
				box, err = e.acceptRemoveLinkNotice(x, original)
				if err != nil {
					return false, err
				}
				created = box.Notice.CreatedAt
				r.NoticeAcceptanceSHA, err = removeLinkNoticeAcceptanceSHA(box, x)
				if err != nil {
					return false, err
				}
			}
			data, _ := json.Marshal(box)
			sealed, err = e.j.p.seal("cleanup_journal", e.j.instance, e.aad("outbox", token), data)
			if err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO plugin_cleanup_private_outbox VALUES(?,?,?,?,?)`, e.j.instance, e.j.generation, token, sealed, created)
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
		return false, cleanuphost.ErrUncertain
	}
	return r.Result, nil
}

func cleanupEffectsGet(ctx context.Context, tx *sql.Tx, kind, key string, out any) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, kind, key).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
func (e *pluginRemoveLinkEffects) currentVideo(ctx context.Context, tx *sql.Tx, plan ImportPlan, f PlanFile) (Asset, managedFile, error) {
	var asset Asset
	var own managedFile
	if err := cleanupEffectsGet(ctx, tx, "asset", f.AssetID, &asset); err != nil {
		return asset, own, err
	}
	if err := cleanupEffectsGet(ctx, tx, "managed_file", tokenHash(f.Target), &own); err != nil {
		return asset, own, err
	}
	if asset.ID != f.AssetID || asset.RootID != plan.RootID || asset.Path != f.Target || asset.State == "removed" || asset.State == "superseded" || own.Kind != "video" || own.Path != f.Target || own.AssetID != asset.ID || own.RootID != asset.RootID || own.MediaKey != mediaKey(asset.Media) || own.SourceSnapshot != planSnapshot(f) {
		return asset, own, cleanuphost.ErrUnsafe
	}
	return asset, own, nil
}
func (e *pluginRemoveLinkEffects) history(ctx context.Context, tx *sql.Tx, x cleanuphost.Effect) (pluginRemoveLinkCoreReceipt, error) {
	var out pluginRemoveLinkCoreReceipt
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
		return out, cleanuphost.ErrUnsafe
	}
	h := items[0]
	var plan ImportPlan
	if err = cleanupEffectsGet(ctx, tx, "import_plan", h.PlanID, &plan); err != nil {
		return out, err
	}
	var file *PlanFile
	for i := range plan.Files {
		if plan.Files[i].ID == h.executionFileID() {
			if file != nil {
				return out, cleanuphost.ErrUnsafe
			}
			file = &plan.Files[i]
		}
	}
	if file == nil || file.Source != h.Source || file.Target != h.Target {
		return out, cleanuphost.ErrUnsafe
	}
	var own managedFile
	if x.Kind == "history_dest" || file.Source != x.Path {
		_, own, err = e.currentVideo(ctx, tx, plan, *file)
		if err != nil {
			return out, err
		}
	}
	if x.Kind == "history_dest" {
		if x.Role != "media" || own.Snapshot != cleanupEffectSnapshot(x.Expected.File) {
			return out, cleanuphost.ErrUnsafe
		}
	} else if err = e.sourceProof(ctx, tx, plan, *file, x.Path, cleanupEffectSnapshot(x.Expected.File), h.ExecutionKey, h.ResourceSource, map[string]bool{}); err != nil {
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
func (e *pluginRemoveLinkEffects) sourceProof(ctx context.Context, tx *sql.Tx, plan ImportPlan, f PlanFile, path string, expected importSnapshot, fence, resource string, seen map[string]bool) error {
	proof := &cleanupSourceProofBudget{remaining: 128, memo: map[cleanupSourceProofKey]bool{}}
	return e.sourceProofBranch(ctx, tx, plan, f, path, expected, fence, resource, seen, proof)
}

type cleanupSourceProofKey struct {
	PlanID, FileID, Path, Fence, Resource string
	Expected                              importSnapshot
}
type cleanupSourceProofBudget struct {
	remaining int
	memo      map[cleanupSourceProofKey]bool
}

func (e *pluginRemoveLinkEffects) sourceProofBranch(ctx context.Context, tx *sql.Tx, plan ImportPlan, f PlanFile, path string, expected importSnapshot, fence, resource string, seen map[string]bool, proof *cleanupSourceProofBudget) error {
	key := plan.ID + "/" + f.ID
	if seen[key] || proof.remaining <= 0 {
		return cleanuphost.ErrUnsafe
	}
	proof.remaining--
	memo := cleanupSourceProofKey{PlanID: plan.ID, FileID: f.ID, Path: path, Fence: fence, Resource: resource, Expected: expected}
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
		return cleanuphost.ErrUnsafe
	}
	var meta importMetadata
	if err := cleanupEffectsGet(ctx, tx, "import_meta", plan.ID, &meta); err != nil {
		return err
	}
	r := meta.Reorganize
	if r == nil || f.Source != r.Asset.Path || planSnapshot(f) != r.Video.Snapshot || r.Video.Kind != "video" || r.Video.AssetID != r.Asset.ID || r.Video.Path != r.Asset.Path || r.Video.RootID != r.Asset.RootID || r.Video.MediaKey != mediaKey(r.Asset.Media) {
		return cleanuphost.ErrUnsafe
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
			return cleanuphost.ErrUnsafe
		}
		// Repeated already_imported submissions legitimately reuse AssetID.
		// Filter by the exact frozen owner, source fingerprint and execution
		// fence before deciding whether the remaining branches are equivalent.
		if c.execution == "" || fence == "" || c.execution >= fence || c.resource != resource || c.f.AssetID != r.Asset.ID || c.f.Target != r.Asset.Path || c.p.RootID != r.Asset.RootID || planSnapshot(c.f) != r.Video.SourceSnapshot || mediaKey(c.p.Media) != mediaKey(r.Asset.Media) || (c.f.Status != "imported" && c.f.Status != "already_imported") {
			continue
		}
		if len(candidates) >= proof.remaining {
			rows.Close()
			return cleanuphost.ErrUnsafe
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return cleanuphost.ErrUnsafe
	}
	for _, old := range candidates {
		if old.f.Source != candidates[0].f.Source || planSnapshot(old.f) != planSnapshot(candidates[0].f) {
			return cleanuphost.ErrUnsafe
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
func cleanupEffectFileFact(x cleanuphost.Effect) bool {
	return x.State == "confirmed" && (x.Kind == "unlink" || x.Kind == "observed_delete") && os.FileMode(x.Expected.File.Mode).IsRegular()
}

// ReconcileConfirmedFiles is required even when every optional source side
// effect is disabled. It performs no filesystem deletion and no reacquisition.
func (e *pluginRemoveLinkEffects) ReconcileConfirmedFiles(ctx context.Context) error {
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
func (e *pluginRemoveLinkEffects) afterAction(ctx context.Context, b cleanuphost.Binding, action string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, err := e.active(ctx)
	if err != nil {
		return err
	}
	if l.binding != b || l.action != action {
		return cleanuphost.ErrUnsafe
	}
	return e.reconcileLocked(ctx, l)
}
func (e *pluginRemoveLinkEffects) reconcileLocked(ctx context.Context, l *pluginRemoveLinkEffectLease) error {
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
		return cleanuphost.ErrUncertain
	}
	return nil
}
func (e *pluginRemoveLinkEffects) reconcileTx(ctx context.Context, tx *sql.Tx, l *pluginRemoveLinkEffectLease, s cleanuphost.Durable) error {
	if err := cleanupEffectRoots(l.roots); err != nil {
		return err
	}
	if s.Binding != l.binding && s.Binding != (cleanuphost.Binding{}) {
		t := s.Transition
		if l.action != "configure" || t == nil || t.Kind != "init" || t.Phase != "published" || t.From != s.Binding || t.To != l.binding || t.RunID != l.runID {
			return cleanuphost.ErrUnsafe
		}
	}
	if len(s.Roots) > 0 {
		if len(s.Roots) != len(l.roots) {
			return cleanuphost.ErrUnsafe
		}
		for i, r := range s.Roots {
			if r.Root != l.roots[i].Root || !cleanupEffectSameEntity(r.File, l.roots[i].File) {
				return cleanuphost.ErrUnsafe
			}
		}
	}
	for key, fact := range s.Effects {
		if !cleanupEffectFileFact(fact) || fact.Role != "media" {
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
			return cleanuphost.ErrUncertain
		}
		if fact.ID != key || fact.Binding != l.binding || fact.Expected.ParentPath != filepath.Dir(fact.Path) {
			return cleanuphost.ErrUnsafe
		}
		r, err := cleanupEffectRoot(l.roots, fact.Path)
		if err != nil || r.Root.Role != "media" || r.Root.ID != fact.Expected.RootID {
			return cleanuphost.ErrUnsafe
		}
		if err = e.confirmedParent(l, s, r, fact.Expected.ParentPath, fact.Expected.Parent, map[string]bool{}); err != nil {
			return err
		}
		_, err = os.Lstat(fact.Path)
		if err == nil {
			return cleanuphost.ErrUnsafe
		}
		if !os.IsNotExist(err) {
			return err
		}
		var own managedFile
		out := pluginRemoveLinkCoreReceipt{}
		err = cleanupEffectsGet(ctx, tx, "managed_file", tokenHash(fact.Path), &own)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && own.Kind == "video" && own.Path == fact.Path && own.RootID == r.Root.ID && own.Snapshot == cleanupEffectSnapshot(fact.Expected.File) {
			var asset Asset
			if err = cleanupEffectsGet(ctx, tx, "asset", own.AssetID, &asset); err != nil {
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
func (e *pluginRemoveLinkEffects) confirmedParent(l *pluginRemoveLinkEffectLease, s cleanuphost.Durable, root cleanuphost.RootStamp, path string, expected cleanuphost.Stamp, seen map[string]bool) error {
	if seen[path] || len(seen) > 128 || !within(root.Root.Path, path) {
		return cleanuphost.ErrUnsafe
	}
	seen[path] = true
	f, err := importDir(path, false)
	if err == nil {
		f.Close()
		actual, err := cleanupEffectStamp(path)
		if err != nil {
			return err
		}
		if !cleanupEffectSameEntity(actual, expected) {
			return cleanuphost.ErrUnsafe
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if path == root.Root.Path {
		return cleanuphost.ErrUnsafe
	}
	var matched *cleanuphost.Effect
	for key, x := range s.Effects {
		if x.Kind != "rmdir" || x.State != "confirmed" || x.Path != path {
			continue
		}
		if l.prior[key] {
			return cleanuphost.ErrUncertain
		}
		if matched != nil || x.ID != key || x.Binding != l.binding || x.Role != root.Root.Role || x.Expected.RootID != root.Root.ID || x.Expected.ParentPath != filepath.Dir(path) || !os.FileMode(x.Expected.File.Mode).IsDir() || !cleanupEffectSameEntity(x.Expected.File, expected) {
			return cleanuphost.ErrUnsafe
		}
		copy := x
		matched = &copy
	}
	if matched == nil {
		return cleanuphost.ErrUncertain
	}
	return e.confirmedParent(l, s, root, matched.Expected.ParentPath, matched.Expected.Parent, seen)
}
