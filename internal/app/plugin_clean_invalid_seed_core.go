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
	"strings"
	"sync"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

type pluginCleanInvalidSeedCore struct{ o *pluginCleanInvalidSeedOwner }
type pluginCleanInvalidSeedLease struct {
	call        *pluginCleanInvalidSeedCall
	nonce       string
	snapshot    pluginCleanInvalidSeedSnapshot
	downloaders []seedhost.Downloader
	mu          sync.Mutex
	active      bool
	grants      map[string]int
}
type pluginCleanInvalidSeedReceipt struct {
	Version          int
	Invocation       seedhost.Invocation
	ID, Kind, Digest string
	Payload          json.RawMessage
}
type pluginCleanInvalidSeedConfigReceipt struct {
	Original                seedhost.Invocation
	From, To                seedhost.Binding
	Previous, ID, ConfigSHA string
}
type pluginCleanInvalidSeedAssetProof struct{ ID, Path, RootID, MediaKey string }
type pluginCleanInvalidSeedFileProof struct {
	Pin     seedhost.Pin
	Managed *managedFile
	Asset   *pluginCleanInvalidSeedAssetProof
}

func cleanSeedEffectDigest(e seedhost.Effect) string {
	e.Phase = "confirmed"
	if e.Kind == "config_write" {
		e.Result = nil
	}
	b, _ := cleanupJournalJSON(e)
	return tokenHash(string(b))
}
func cleanSeedGrantKey(e seedhost.Effect) string {
	return tokenHash(string(mustCleanSeedJSON(struct {
		Binding, Authority                                              seedhost.Binding
		ID, Kind, Operation, DownloaderID, DownloaderSHA, ConfigReceipt string
		DownloaderRevision                                              int64
		Args, NativeSnapshot                                            json.RawMessage
		Pin                                                             *seedhost.Pin
		Manifest                                                        []seedhost.Pin
		ExpectedCore                                                    map[string]json.RawMessage
	}{e.Binding, e.Authority, e.ID, e.Kind, e.Operation, e.DownloaderID, e.DownloaderSHA, e.ConfigReceipt, e.DownloaderRevision, e.Args, e.NativeSnapshot, e.Pin, e.Manifest, e.ExpectedCore})))
}
func mustCleanSeedJSON(v any) json.RawMessage { b, _ := cleanupJournalJSON(v); return b }
func cleanSeedHasGrant(l *pluginCleanInvalidSeedLease, e seedhost.Effect) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active && l.grants[cleanSeedGrantKey(e)] > 0
}
func cleanSeedReceiptToken(j *pluginCleanInvalidSeedJournal, id string) string {
	return j.token("core", id, -1)
}
func cleanSeedSaveReceipt(ctx context.Context, tx *sql.Tx, j *pluginCleanInvalidSeedJournal, r pluginCleanInvalidSeedReceipt) error {
	if r.Version != 1 || r.ID == "" || !cleanSeedBinding(r.Invocation.Binding, j.instance, j.generation, false) {
		return seedhost.ErrUnsafe
	}
	token := cleanSeedReceiptToken(j, r.ID)
	var existing []byte
	err := tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_clean_seed_core_receipts WHERE instance_id=? AND generation=? AND token=?`, j.instance, j.generation, token).Scan(&existing)
	if err == nil {
		var old pluginCleanInvalidSeedReceipt
		b, e := j.p.unseal("clean_seed_journal", j.instance, j.key("core", token), existing)
		if e != nil || cleanupJournalStrict(b, &old) != nil {
			return seedhost.ErrUnsafe
		}
		want, _ := cleanupJournalJSON(r)
		got, _ := cleanupJournalJSON(old)
		if !bytes.Equal(want, got) {
			return seedhost.ErrUnsafe
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	b, err := cleanupJournalJSON(r)
	if err != nil || len(b) > 64<<10 {
		return seedhost.ErrUnsafe
	}
	sealed, err := j.p.seal("clean_seed_journal", j.instance, j.key("core", token), b)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_clean_seed_core_receipts VALUES(?,?,?,?)`, j.instance, j.generation, token, sealed)
	return err
}
func cleanSeedReadReceipt(ctx context.Context, q pluginRemoveLinkJournalReader, j *pluginCleanInvalidSeedJournal, id string) (pluginCleanInvalidSeedReceipt, error) {
	var r pluginCleanInvalidSeedReceipt
	token := cleanSeedReceiptToken(j, id)
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_clean_seed_core_receipts WHERE instance_id=? AND generation=? AND token=?`, j.instance, j.generation, token).Scan(&sealed); err != nil {
		return r, err
	}
	b, err := j.p.unseal("clean_seed_journal", j.instance, j.key("core", token), sealed)
	if err != nil {
		return r, err
	}
	if cleanupJournalStrict(b, &r) != nil || r.Version != 1 || r.ID != id || !cleanSeedBinding(r.Invocation.Binding, j.instance, j.generation, false) {
		return r, seedhost.ErrUnsafe
	}
	return r, nil
}
func cleanSeedReadEffectReceipt(ctx context.Context, q pluginRemoveLinkJournalReader, j *pluginCleanInvalidSeedJournal, inv seedhost.Invocation, e seedhost.Effect) error {
	r, err := cleanSeedReadReceipt(ctx, q, j, e.ID)
	if err != nil {
		return err
	}
	if r.Invocation != inv || r.Kind != e.Kind || r.Digest != cleanSeedEffectDigest(e) {
		return seedhost.ErrUnsafe
	}
	return nil
}

// active checks a native revocable lease pointer. It neither takes locks that
// a Settings writer can own nor treats a context boolean as lock ownership.
func (c *pluginCleanInvalidSeedCore) active(ctx context.Context, inv seedhost.Invocation) (*pluginCleanInvalidSeedLease, error) {
	call, ok := ctx.Value(pluginCleanInvalidSeedCallKey{}).(*pluginCleanInvalidSeedCall)
	if !ok || call == nil || call.owner != c.o || call.inv != inv || call.nonce == "" {
		return nil, seedhost.ErrUnsafe
	}
	call.mu.Lock()
	l := call.lease
	call.mu.Unlock()
	if l == nil {
		return nil, seedhost.ErrUnsafe
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.active || l.call != call || l.nonce == "" || l.nonce != call.nonce {
		return nil, seedhost.ErrUnsafe
	}
	return l, nil
}
func (c *pluginCleanInvalidSeedCore) verifyTx(ctx context.Context, tx *sql.Tx, l *pluginCleanInvalidSeedLease, inv seedhost.Invocation, e seedhost.Effect) (seedhost.State, error) {
	s, _, err := c.o.j.read(ctx, tx)
	if err != nil {
		return s, err
	}
	if e.Binding != inv.Binding || e.Authority.InstanceID != inv.Binding.InstanceID || e.Authority.Generation != inv.Binding.Generation {
		return s, seedhost.ErrUnsafe
	}
	actual, err := c.o.a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, inv.RunID))
	if err != nil {
		return s, err
	}
	got, err := cleanSeedInvocation(actual)
	if err != nil || got != inv || actual.State != "running" || !bytes.Equal(actual.Input, l.call.run.Input) {
		return s, seedhost.ErrUnsafe
	}
	var plugin, version string
	var installed, enabled bool
	var generation, revision int64
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision,v.sealed FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, inv.Binding.InstanceID).Scan(&plugin, &version, &generation, &installed, &enabled, &revision, &sealed)
	if err != nil {
		return s, err
	}
	if plugin != "CleanInvalidSeed" || version != "2.1" || !installed || !enabled || generation != inv.Binding.Generation || revision != e.Authority.Revision {
		return s, seedhost.ErrUnsafe
	}
	config, err := c.o.a.pluginStore.unseal("config", inv.Binding.InstanceID, "main", sealed)
	if err != nil {
		return s, err
	}
	d, _, err := c.o.a.pluginStore.readDeferred(ctx, tx, l.call.run)
	if err != nil || d.Phase != "started" || d.Plugin != "CleanInvalidSeed" || d.Version != "2.1" || d.Run != inv.RunID || d.Instance != inv.Binding.InstanceID || d.Generation != inv.Binding.Generation || d.Revision != inv.Binding.Revision || d.Action != inv.Action || d.InputSHA != deferredInputSHA(actual.Input) {
		return s, seedhost.ErrUnsafe
	}
	if e.Authority == inv.Binding {
		if e.ConfigReceipt != "" || tokenHash(string(config)) != l.snapshot.ConfigSHA {
			return s, seedhost.ErrUnsafe
		}
	} else {
		if e.ConfigReceipt == "" {
			return s, seedhost.ErrUnsafe
		}
		receipt, err := cleanSeedReadReceipt(ctx, tx, c.o.j, e.ConfigReceipt)
		if err != nil {
			return s, err
		}
		var chain pluginCleanInvalidSeedConfigReceipt
		if receipt.Kind != "config_write" || receipt.Invocation != inv || cleanupJournalStrict(receipt.Payload, &chain) != nil || chain.Original != inv || chain.ID != e.ConfigReceipt || chain.To != e.Authority || chain.ConfigSHA != tokenHash(string(config)) {
			return s, seedhost.ErrUnsafe
		}
		r, ok := s.Runs[inv.RunID]
		if !ok || r.Invocation != inv || r.Effective != e.Authority || r.ConfigReceipt != e.ConfigReceipt {
			return s, seedhost.ErrUnsafe
		}
	}
	if err = cleanSeedValidateRoots(c.o.a.Settings(), l.snapshot.Roots); err != nil {
		return s, err
	}
	for _, ref := range l.snapshot.Downloaders {
		found := false
		for _, current := range c.o.a.Settings().Downloaders {
			if current.ID == ref.ID && cleanSeedDownloaderSHA(c.o.j, current) == ref.ConfigSHA && current.Name == ref.Name && !downloaderEnabled(current) == ref.Inactive {
				found = true
				break
			}
		}
		if !found {
			return s, seedhost.ErrUnsafe
		}
	}
	return s, nil
}
func cleanSeedDownloaderSHA(j *pluginCleanInvalidSeedJournal, d DownloaderConfig) string {
	b, _ := cleanupJournalJSON(d)
	return hex.EncodeToString(j.mac("downloader", b))
}
func cleanSeedStamp(s seedhost.Stamp) importSnapshot {
	return importSnapshot{Device: s.Dev, Inode: s.Inode, Size: s.Size, ModNS: s.ModNS}
}
func cleanSeedSameEntity(a, b seedhost.Stamp) bool {
	return a.Dev == b.Dev && a.Inode == b.Inode && a.Mode == b.Mode
}
func cleanSeedCurrentFileProof(ctx context.Context, tx *sql.Tx, pin seedhost.Pin) (json.RawMessage, error) {
	proof := pluginCleanInvalidSeedFileProof{Pin: pin}
	var own managedFile
	err := cleanupEffectsGet(ctx, tx, "managed_file", tokenHash(pin.Path), &own)
	if errors.Is(err, sql.ErrNoRows) {
		return cleanupJournalJSON(proof)
	}
	if err != nil {
		return nil, err
	}
	if own.Path != pin.Path || own.Snapshot != cleanSeedStamp(pin.File) || own.AssetID == "" {
		return nil, seedhost.ErrUnsafe
	}
	var a Asset
	if err = cleanupEffectsGet(ctx, tx, "asset", own.AssetID, &a); err != nil {
		return nil, err
	}
	if a.ID != own.AssetID || a.RootID != own.RootID || mediaKey(a.Media) != own.MediaKey || a.State == "superseded" || a.State == "removed" {
		return nil, seedhost.ErrUnsafe
	}
	if own.Kind == "video" && a.Path != own.Path {
		return nil, seedhost.ErrUnsafe
	}
	proof.Managed = &own
	proof.Asset = &pluginCleanInvalidSeedAssetProof{a.ID, a.Path, a.RootID, mediaKey(a.Media)}
	return cleanupJournalJSON(proof)
}
func cleanSeedRoot(roots []seedhost.Root, path string) (seedhost.Root, error) {
	for _, r := range roots {
		if within(r.Path, path) {
			return r, nil
		}
	}
	return seedhost.Root{}, seedhost.ErrUnsafe
}
func cleanSeedValidateRoots(settings Settings, roots []seedhost.Root) error {
	for _, r := range roots {
		allowed := false
		if r.Role == "media" {
			for _, m := range settings.Roots {
				if m.ID == r.ID && m.Path == r.Path {
					allowed = true
					break
				}
			}
		} else if r.Role == "source" {
			for _, p := range settings.AllowedRoots {
				if within(p, r.Path) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			return seedhost.ErrUnsafe
		}
		current, err := seedhost.AuthorizeRoot(r.ID, r.Path, r.Role)
		if err != nil || !cleanSeedSameEntity(current.Stamp, r.Stamp) {
			return seedhost.ErrUnsafe
		}
	}
	return nil
}
func (c *pluginCleanInvalidSeedCore) Pin(ctx context.Context, inv seedhost.Invocation, pin seedhost.Pin) (json.RawMessage, error) {
	l, err := c.active(ctx, inv)
	if err != nil {
		return nil, err
	}
	tx, err := c.o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	s, _, err := c.o.j.read(ctx, tx)
	if err != nil {
		return nil, err
	}
	r, ok := s.Runs[inv.RunID]
	if !ok || r.Invocation != inv || r.Phase != "started" {
		return nil, seedhost.ErrUnsafe
	}
	if _, err = c.verifyTx(ctx, tx, l, inv, seedhost.Effect{Binding: inv.Binding, Authority: r.Effective, ConfigReceipt: r.ConfigReceipt}); err != nil {
		return nil, err
	}
	root, err := cleanSeedRoot(l.snapshot.Roots, pin.Path)
	if err != nil || pin.RootID != root.ID {
		return nil, seedhost.ErrUnsafe
	}
	return cleanSeedCurrentFileProof(ctx, tx, pin)
}
func (c *pluginCleanInvalidSeedCore) Guard(ctx context.Context, inv seedhost.Invocation, e seedhost.Effect) (json.RawMessage, func(), error) {
	l, err := c.active(ctx, inv)
	if err != nil {
		return nil, nil, err
	}
	if ctx.Err() != nil && e.Kind != "finalize" && e.Phase != "fs_confirmed" {
		return nil, nil, ctx.Err()
	}
	tx, err := c.o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	s, err := c.verifyTx(ctx, tx, l, inv, e)
	if err != nil {
		return nil, nil, err
	}
	switch e.Kind {
	case "begin":
		if inv.Action == "init" || inv.Action == "reload" {
			if !bytes.Equal(e.Args, l.call.config) {
				return nil, nil, seedhost.ErrUnsafe
			}
		}
	case "finalize", "schedule_stop", "config_write", "notice":
		r, ok := s.Runs[inv.RunID]
		if !ok || r.Phase != "started" || r.Invocation != inv {
			return nil, nil, seedhost.ErrUnsafe
		}
	case "native_read", "native_write":
		found := false
		for _, d := range l.snapshot.Downloaders {
			if d.ID == e.DownloaderID && d.Revision == e.DownloaderRevision && d.ConfigSHA == e.DownloaderSHA && !d.Inactive {
				found = true
				break
			}
		}
		if !found {
			return nil, nil, seedhost.ErrUnsafe
		}
		if e.Kind == "native_write" && len(e.NativeSnapshot) == 0 {
			return nil, nil, seedhost.ErrUnsafe
		}
	case "file_delete", "file_step":
		if e.Pin == nil || len(e.ExpectedCore) == 0 {
			return nil, nil, seedhost.ErrUnsafe
		}
		if e.Kind == "file_delete" {
			config, err := cleanSeedConfigMap(l.call.effective)
			if err != nil {
				return nil, nil, err
			}
			text, _ := config["download_dirs"].(string)
			selected := false
			for _, line := range strings.Split(text, "\n") {
				root, _, ok := strings.Cut(line, ":")
				if ok && strings.TrimSpace(root) == e.Pin.Parent {
					selected = true
					break
				}
			}
			if !selected || e.Pin.Path == e.Pin.Parent || filepath.Dir(e.Pin.Path) != e.Pin.Parent || len(e.Manifest) != len(e.ExpectedCore) {
				return nil, nil, seedhost.ErrUnsafe
			}
			for _, pin := range e.Manifest {
				var frozen pluginCleanInvalidSeedFileProof
				if cleanupJournalStrict(e.ExpectedCore[pin.Path], &frozen) != nil || frozen.Pin != pin {
					return nil, nil, seedhost.ErrUnsafe
				}
			}
		}
		for path, frozen := range e.ExpectedCore {
			var proof pluginCleanInvalidSeedFileProof
			if cleanupJournalStrict(frozen, &proof) != nil || proof.Pin.Path != path {
				return nil, nil, seedhost.ErrUnsafe
			}
			current, err := cleanSeedCurrentFileProof(ctx, tx, proof.Pin)
			if err != nil || !bytes.Equal(current, frozen) {
				return nil, nil, seedhost.ErrUnsafe
			}
			if _, err = cleanSeedRoot(l.snapshot.Roots, path); err != nil {
				return nil, nil, err
			}
		}
	default:
		return nil, nil, seedhost.ErrUnsafe
	}
	key := cleanSeedGrantKey(e)
	l.mu.Lock()
	if !l.active {
		l.mu.Unlock()
		return nil, nil, seedhost.ErrUnsafe
	}
	l.grants[key]++
	l.mu.Unlock()
	var once sync.Once
	proof, _ := cleanupJournalJSON(e.ExpectedCore)
	return proof, func() { once.Do(func() { l.mu.Lock(); l.grants[key]--; l.mu.Unlock() }) }, nil
}

func (c *pluginCleanInvalidSeedCore) Commit(ctx context.Context, tx *sql.Tx, inv seedhost.Invocation, e seedhost.Effect) error {
	l, err := c.active(ctx, inv)
	if err != nil {
		return err
	}
	s, err := c.verifyTx(ctx, tx, l, inv, e)
	if err != nil {
		return err
	}
	old, ok := s.Effects[e.ID]
	if !ok || old.RunID != inv.RunID || old.Binding != inv.Binding || old.Authority != e.Authority || old.ConfigReceipt != e.ConfigReceipt {
		return seedhost.ErrUnsafe
	}
	switch e.Kind {
	case "native_write":
		if old.Phase != "started" || e.Phase != "confirmed" || e.Operation != old.Operation || e.DownloaderID != old.DownloaderID || e.DownloaderSHA != old.DownloaderSHA || e.DownloaderRevision != old.DownloaderRevision || !bytes.Equal(e.Args, old.Args) || !bytes.Equal(e.NativeSnapshot, old.NativeSnapshot) {
			return seedhost.ErrUnsafe
		}
		if e.ErrorCode == "DOWNLOADER_MUTATION_NOT_STARTED" {
			if string(e.Result) != `{"protocol_not_started":true}` || !seedhost.NativeMutationNotStarted(ctx, inv, e) {
				return seedhost.ErrUnsafe
			}
		} else if e.ErrorCode != "" || string(e.Result) != `{"protocol_acknowledged":true,"observed":false}` {
			return seedhost.ErrUnsafe
		}
		grant := e
		grant.ID = ""
		if !cleanSeedHasGrant(l, grant) {
			return seedhost.ErrUnsafe
		}
	case "file_step":
		if old.Phase != "fs_confirmed" || e.Phase != "fs_confirmed" || cleanSeedEffectDigest(old) != cleanSeedEffectDigest(e) || e.Pin == nil {
			return seedhost.ErrUnsafe
		}
		if err = c.confirmFileStep(ctx, tx, l, s, e); err != nil {
			return err
		}
	default:
		return seedhost.ErrUnsafe
	}
	return cleanSeedSaveReceipt(ctx, tx, c.o.j, pluginCleanInvalidSeedReceipt{Version: 1, Invocation: inv, ID: e.ID, Kind: e.Kind, Digest: cleanSeedEffectDigest(e)})
}
func (c *pluginCleanInvalidSeedCore) confirmFileStep(ctx context.Context, tx *sql.Tx, l *pluginCleanInvalidSeedLease, s seedhost.State, e seedhost.Effect) error {
	var parent *seedhost.Effect
	for _, candidate := range s.Effects {
		if candidate.RunID == e.RunID && candidate.Kind == "file_delete" && strings.HasPrefix(e.ID, candidate.ID+":step:") {
			x := candidate
			if parent != nil {
				return seedhost.ErrUnsafe
			}
			parent = &x
		}
	}
	if parent == nil || parent.Pin == nil || parent.Stage == "" || e.Pin == nil {
		return seedhost.ErrUnsafe
	}
	if !cleanSeedHasGrant(l, *parent) {
		return seedhost.ErrUnsafe
	}
	pin := *e.Pin
	matched := false
	for _, p := range parent.Manifest {
		if p == pin {
			matched = true
			break
		}
	}
	if !matched {
		return seedhost.ErrUnsafe
	}
	actual := parent.Stage
	if pin.Path != parent.Pin.Path {
		rel, err := filepath.Rel(parent.Pin.Path, pin.Path)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return seedhost.ErrUnsafe
		}
		actual = filepath.Join(parent.Stage, rel)
	}
	if e.StageFrom != actual || filepath.Dir(e.Stage) != filepath.Dir(actual) || !strings.HasPrefix(filepath.Base(e.Stage), ".cleanup-leaf-") {
		return seedhost.ErrUnsafe
	}
	root, err := cleanSeedRoot(l.snapshot.Roots, pin.Path)
	if err != nil {
		return err
	}
	if !within(root.Path, actual) || !within(root.Path, e.Stage) {
		return seedhost.ErrUnsafe
	}
	f, err := importDir(filepath.Dir(actual), false)
	if err != nil {
		return err
	}
	f.Close()
	ps, err := cleanupEffectStamp(filepath.Dir(actual))
	if err != nil || !cleanSeedSameEntity(seedhost.Stamp{Dev: ps.Dev, Inode: ps.Inode, Size: ps.Size, ModNS: ps.ModNS, Mode: ps.Mode}, pin.ParentFile) {
		return seedhost.ErrUnsafe
	}
	for _, path := range []string{actual, e.Stage, pin.Path} {
		if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return seedhost.ErrUnsafe
		}
	}
	if os.FileMode(pin.File.Mode).IsDir() {
		if len(e.Facts) != 0 {
			return seedhost.ErrUnsafe
		}
		return nil
	}
	if len(e.Facts) != 1 || e.Facts[0].Pin != pin {
		return seedhost.ErrUnsafe
	}
	frozen, ok := e.ExpectedCore[pin.Path]
	if !ok {
		return seedhost.ErrUnsafe
	}
	current, err := cleanSeedCurrentFileProof(ctx, tx, pin)
	if err != nil || !bytes.Equal(current, frozen) {
		return seedhost.ErrUnsafe
	}
	var proof pluginCleanInvalidSeedFileProof
	if cleanupJournalStrict(frozen, &proof) != nil {
		return seedhost.ErrUnsafe
	}
	if proof.Managed == nil {
		return nil
	}
	own := proof.Managed
	if own.Kind != "video" {
		return nil
	}
	if root.Role != "media" || root.ID != own.RootID || proof.Asset == nil {
		return seedhost.ErrUnsafe
	}
	var asset Asset
	if err = cleanupEffectsGet(ctx, tx, "asset", own.AssetID, &asset); err != nil {
		return err
	}
	if asset.ID != own.AssetID || asset.Path != pin.Path || asset.RootID != own.RootID || mediaKey(asset.Media) != own.MediaKey || asset.State == "superseded" || asset.State == "removed" {
		return seedhost.ErrUnsafe
	}
	asset.State = "missing_pending"
	asset.MissingSeenAt = now()
	asset.FNOSState = "needs_attention"
	asset.Error = "清理无效做种已确认媒体文件删除，保留身份与占用，等待核对"
	return txPut(ctx, tx, "asset", asset.ID, asset)
}

func (c *pluginCleanInvalidSeedCore) Config(ctx context.Context, tx *sql.Tx, inv seedhost.Invocation, e seedhost.Effect) (seedhost.ConfigPublication, error) {
	publication := seedhost.ConfigPublication{}
	l, err := c.active(ctx, inv)
	if err != nil {
		return publication, err
	}
	s, err := c.verifyTx(ctx, tx, l, inv, e)
	if err != nil {
		return publication, err
	}
	if e.Kind != "config_write" || e.ID == "" || e.Phase != "confirmed" {
		return publication, seedhost.ErrUnsafe
	}
	if !cleanSeedHasGrant(l, e) {
		return publication, seedhost.ErrUnsafe
	}
	r, ok := s.Runs[inv.RunID]
	if !ok || r.Phase != "started" || r.Effective != e.Authority || r.ConfigReceipt != e.ConfigReceipt {
		return publication, seedhost.ErrUnsafe
	}
	if _, exists := s.Effects[e.ID]; exists {
		return publication, seedhost.ErrAlready
	}
	if _, err = cleanSeedConfigMap(e.Args); err != nil {
		return publication, err
	}
	savedConfig, err := c.o.a.pluginStore.saveValueTx(ctx, tx, inv.Binding.InstanceID, "config", "main", e.Authority.Revision, e.Args)
	if err != nil {
		return publication, err
	}
	revision := savedConfig.Revision
	next := e.Authority
	next.Revision = revision
	publication.Binding = next
	// Only this private source lineage may rebind unstarted source events. Raw
	// invocation input stays immutable: the compiled SDK ignores its config.
	for _, queued := range e.Queued {
		if queued.Binding != e.Authority || queued.RunID == inv.RunID {
			return publication, seedhost.ErrUnsafe
		}
		actual, err := c.o.a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, queued.RunID))
		if err != nil {
			return publication, err
		}
		got, err := cleanSeedInvocation(actual)
		if err != nil || got != queued || actual.State != "pending" {
			return publication, seedhost.ErrUnsafe
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_deferred_operations WHERE run_id=?`, actual.ID).Scan(&count); err != nil {
			return publication, err
		}
		if count != 0 {
			return publication, seedhost.ErrUnsafe
		}
		if _, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET config_revision=?,updated_at=? WHERE id=? AND state='pending' AND config_revision=?`, revision, now(), actual.ID, e.Authority.Revision); err != nil {
			return publication, err
		}
		queued.Binding = next
		publication.Rebound = append(publication.Rebound, queued)
	}
	// Queued manual clean actions have no Source Trigger but share the same live
	// source object. Do not cancel them merely because onlyonce reset its flag.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM plugin_runs WHERE instance_id=? AND version='2.1' AND generation=? AND config_revision=? AND state='pending' AND action='run'`, inv.Binding.InstanceID, inv.Binding.Generation, e.Authority.Revision)
	if err != nil {
		return publication, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return publication, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return publication, err
	}
	for _, id := range ids {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_deferred_operations WHERE run_id=?`, id).Scan(&count); err != nil || count != 0 {
			return publication, seedhost.ErrUnsafe
		}
		if _, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET config_revision=?,updated_at=? WHERE id=? AND state='pending' AND config_revision=?`, revision, now(), id, e.Authority.Revision); err != nil {
			return publication, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_schedules SET config_revision=? WHERE instance_id=? AND generation=? AND config_revision=? AND group_key='clean_seed'`, revision, inv.Binding.InstanceID, inv.Binding.Generation, e.Authority.Revision); err != nil {
		return publication, err
	}
	for _, queued := range e.Queued {
		if _, err = tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET config_revision=? WHERE instance_id=? AND generation=? AND config_revision=? AND state='queued' AND run_id=?`, revision, inv.Binding.InstanceID, inv.Binding.Generation, e.Authority.Revision, queued.RunID); err != nil {
			return publication, err
		}
	}
	chain := pluginCleanInvalidSeedConfigReceipt{Original: inv, From: e.Authority, To: next, Previous: e.ConfigReceipt, ID: e.ID, ConfigSHA: tokenHash(string(e.Args))}
	payload, _ := cleanupJournalJSON(chain)
	if err = cleanSeedSaveReceipt(ctx, tx, c.o.j, pluginCleanInvalidSeedReceipt{Version: 1, Invocation: inv, ID: e.ID, Kind: e.Kind, Digest: cleanSeedEffectDigest(e), Payload: payload}); err != nil {
		return publication, err
	}
	return publication, nil
}

// A source-internal SaveConfig changes the already running native object's
// effective binding. Keep that applied identity in the same transaction, so a
// crash cannot turn its still-ready once plan into a new cold init. An external
// published transition must finish its own init/reload before it is applied.
func cleanSeedAdvanceAppliedBindingTx(ctx context.Context, tx *sql.Tx, j *pluginCleanInvalidSeedJournal, s seedhost.State, m *pluginCleanInvalidSeedMeta, inv seedhost.Invocation) error {
	if m.Closed || m.AppliedRun == "" || m.Transition != nil && m.Transition.Phase != "applied" {
		return nil
	}
	r, ok := s.Runs[inv.RunID]
	if !ok || r.Invocation != inv || r.Effective != s.Binding || r.Effective == m.AppliedBinding || r.ConfigReceipt == "" {
		return nil
	}
	e, ok := s.Effects[r.ConfigReceipt]
	if !ok || e.Kind != "config_write" || e.Phase != "confirmed" || e.Binding != inv.Binding || e.RunID != inv.RunID || e.Authority != m.AppliedBinding {
		return nil
	}
	if err := cleanSeedReadEffectReceipt(ctx, tx, j, inv, e); err != nil {
		return err
	}
	receipt, err := cleanSeedReadReceipt(ctx, tx, j, e.ID)
	if err != nil {
		return err
	}
	var chain pluginCleanInvalidSeedConfigReceipt
	if cleanupJournalStrict(receipt.Payload, &chain) != nil || chain.Original != inv || chain.From != m.AppliedBinding || chain.To != r.Effective || chain.Previous != e.ConfigReceipt || chain.ID != e.ID || chain.ConfigSHA != tokenHash(string(s.Config)) || !bytes.Equal(s.Config, e.Args) {
		return seedhost.ErrUnsafe
	}
	m.AppliedBinding = chain.To
	return nil
}

func (c *pluginCleanInvalidSeedCore) Notice(ctx context.Context, tx *sql.Tx, inv seedhost.Invocation, n seedhost.Notice) error {
	l, err := c.active(ctx, inv)
	if err != nil {
		return err
	}
	s, _, err := c.o.j.read(ctx, tx)
	if err != nil {
		return err
	}
	r, ok := s.Runs[inv.RunID]
	if !ok {
		return seedhost.ErrUnsafe
	}
	e := seedhost.Effect{Binding: inv.Binding, Authority: r.Effective, ConfigReceipt: r.ConfigReceipt, RunID: inv.RunID, ID: n.ID, Kind: "notice", Phase: "confirmed", Args: n.Payload}
	if !cleanSeedHasGrant(l, e) {
		return seedhost.ErrUnsafe
	}
	if _, err = c.verifyTx(ctx, tx, l, inv, e); err != nil {
		return err
	}
	if n.ID == "" || n.RunID != inv.RunID || n.State != "pending" {
		return seedhost.ErrUnsafe
	}
	return c.acceptNoticeTx(ctx, tx, inv, n, e)
}

var _ seedhost.Core = (*pluginCleanInvalidSeedCore)(nil)
