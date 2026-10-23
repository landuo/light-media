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
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins/autosignin"
	signinjournal "light-media/internal/plugins/autosignin/durable"
)

// This schema stores source state and receipts, not another execution queue.
// A real plugin_runs row and the native exclusive owner authorize every update.
const pluginAutoSignInJournalSchema = `CREATE TABLE IF NOT EXISTS plugin_signin_meta(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,digest BLOB NOT NULL,
 PRIMARY KEY(instance_id,generation));
CREATE TABLE IF NOT EXISTS plugin_signin_rows(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,collection TEXT NOT NULL,token TEXT NOT NULL,part INTEGER NOT NULL,
 sealed BLOB NOT NULL,digest BLOB NOT NULL,size INTEGER NOT NULL,
 PRIMARY KEY(instance_id,generation,collection,token,part));
CREATE TABLE IF NOT EXISTS plugin_signin_owners(
 instance_id TEXT PRIMARY KEY,generation INTEGER NOT NULL,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_signin_config_receipts(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,to_revision INTEGER NOT NULL,sealed BLOB NOT NULL,
 PRIMARY KEY(instance_id,generation,to_revision));`

const (
	pluginAutoSignInChunk    = 128 << 10
	pluginAutoSignInCapacity = 256 << 20
	// The receipt includes escaped text, binary, cookies and error metadata.
	// All external kinds use this conservative common admission allowance.
	pluginAutoSignInResponseReserve = 24 << 20
	pluginAutoSignInResponseLimit   = 16 << 20
	pluginAutoSignInRowLimit        = 200000
)

type pluginAutoSignInOwner struct {
	originalEngine            *autosignin.Engine
	a                         *App
	binding                   signinjournal.Binding
	mu                        sync.Mutex
	file                      *os.File
	active                    int
	closed                    bool
	execution                 chan struct{}
	testCapacity, testReserve int // private owned-fixture seams; never protocol/config fields.
}

type pluginAutoSignInBackend struct {
	owner *pluginAutoSignInOwner
	run   *PluginRun // immutable, host-created snapshot; nil is outbox maintenance only.
	mu    sync.Mutex
	held  int
	// Only the native host may bind a source entrypoint after verifying its sealed event origin.
	sourcePlan json.RawMessage
}

var _ signinjournal.StateBackend = (*pluginAutoSignInBackend)(nil)

type pluginAutoSignInMeta struct {
	Version                        int
	Scope                          signinjournal.Scope
	Epoch, ActiveRun, RuntimeEpoch string
	Count, Bytes                   int
	Digest                         []byte
}
type pluginAutoSignInCell struct {
	Collection, Key string
	Data            []byte
}
type pluginAutoSignInRow struct {
	Version                int
	Instance               string
	Generation             uint64
	Collection, Token, Key string
	Part, Parts            int
	Nil                    bool
	Data                   []byte
}
type pluginAutoSignInPhysical struct {
	Collection, Token string
	Part, Size        int
	Digest            []byte
	Sealed            []byte // populated only for a changed row awaiting publication.
}
type pluginAutoSignInRead struct {
	State      signinjournal.State
	Cells      map[string]pluginAutoSignInCell
	Rows       map[string]pluginAutoSignInPhysical
	MetaDigest []byte
}
type pluginAutoSignInConfigReceipt struct {
	Scope                                     signinjournal.Scope
	RunID, Lane, Digest, ConfigHash, Previous string
	Index                                     int
	Admission, From, To                       uint64
}

type pluginAutoSignInRunHead struct {
	ID, Action, Epoch, Phase string
	Scope                    signinjournal.Scope
	Timezone                 string
}
type pluginAutoSignInHeader struct {
	Schema                         int
	Scope                          signinjournal.Scope
	Epoch, ActiveRun, RuntimeEpoch string
}
type pluginAutoSignInEntryHead struct{ Kind, Digest, Phase, Error, ErrorClass, Guard string }
type pluginAutoSignInOutboxHead struct {
	ID, Kind, Phase, RunID, Lane string
	Index                        int
}

func signinStoreError() error { return signinjournal.ErrStore }
func signinJSON(v any) ([]byte, error) {
	if !cleanupJournalStringsValid(reflect.ValueOf(v)) {
		return nil, signinStoreError()
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, signinStoreError()
	}
	return raw, nil
}
func signinKey(parts ...string) string { raw, _ := json.Marshal(parts); return string(raw) }
func signinParts(key string, n int) ([]string, error) {
	var parts []string
	if cleanupJournalStrict([]byte(key), &parts) != nil || len(parts) != n {
		return nil, signinStoreError()
	}
	return parts, nil
}
func (o *pluginAutoSignInOwner) mac(kind string, raw []byte) []byte {
	m := hmac.New(sha256.New, o.a.Store.key)
	scope, _ := json.Marshal([]any{"light-media/autosignin/sql/v1", o.binding.Instance, o.binding.Generation, kind})
	m.Write(scope)
	m.Write([]byte{0})
	m.Write(raw)
	return m.Sum(nil)
}
func (o *pluginAutoSignInOwner) token(collection, key string) string {
	return hex.EncodeToString(o.mac("cell", []byte(signinKey(collection, key))))
}
func (o *pluginAutoSignInOwner) aad(collection, token string, part int) string {
	return "signin:v1:" + strconv.FormatUint(o.binding.Generation, 10) + ":" + collection + ":" + token + ":" + strconv.Itoa(part)
}
func signinPhysicalKey(r pluginAutoSignInPhysical) string {
	return signinKey(r.Collection, r.Token, strconv.Itoa(r.Part))
}

// claimPluginAutoSignInOwner is native host lifecycle work. The caller must not
// retain lifecycle/process/pluginMu while subsequently waiting for execution.
// The flock is acquired before the short lifecycle transaction and retained
// until Close; it is not a bearer token that a worker can construct.
func claimPluginAutoSignInOwner(ctx context.Context, a *App, instance string, implementation string, timezone string) (*pluginAutoSignInOwner, error) {
	if a == nil || a.Store == nil || a.Store.lock == nil || a.pluginStore == nil || !pluginStorageID.MatchString(instance) || implementation == "" || !utf8.ValidString(implementation) {
		return nil, signinjournal.ErrStale
	}
	name := sha256.Sum256([]byte(instance))
	path := filepath.Join(filepath.Dir(a.Store.lock.Name()), ".signin-owner-"+hex.EncodeToString(name[:])+".lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, signinStoreError()
	}
	f := os.NewFile(uintptr(fd), path)
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, signinjournal.ErrBusy
	}
	ok := false
	defer func() {
		if !ok {
			unix.Flock(fd, unix.LOCK_UN)
			f.Close()
		}
	}()
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, pluginAutoSignInJournalSchema); err != nil {
		return nil, err
	}
	var plugin, version string
	var generation, revision int64
	var installed, enabled bool
	var configSealed []byte
	err = tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision,v.sealed FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, instance).Scan(&plugin, &version, &generation, &installed, &enabled, &revision, &configSealed)
	if err != nil {
		return nil, err
	}
	if plugin != "AutoSignIn" || version != autosignin.Version || !installed || !enabled || generation < 1 || revision < 1 {
		return nil, signinjournal.ErrStale
	}
	o := &pluginAutoSignInOwner{a: a, binding: signinjournal.Binding{Scope: signinjournal.Scope{Instance: instance, Implementation: implementation, Generation: uint64(generation), Revision: uint64(revision)}, Epoch: id("signin_owner_")}, file: f, execution: make(chan struct{}, 1)}
	loaded, err := o.readTx(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) {
		runtime, e := signinjournal.NewRuntime(timezone)
		if e != nil {
			return nil, e
		}
		loaded.State, e = signinjournal.NewState(o.binding.Scope, runtime)
		if e != nil {
			return nil, e
		}
		loaded.State.Config, e = a.pluginStore.unseal("config", instance, "main", configSealed)
		if e != nil {
			return nil, e
		}
		loaded.Cells = map[string]pluginAutoSignInCell{}
		loaded.Rows = map[string]pluginAutoSignInPhysical{}
	} else if err != nil {
		return nil, err
	} else {
		if loaded.State.Scope.Instance != instance || loaded.State.Scope.Generation != uint64(generation) || loaded.State.Scope.Implementation != implementation {
			return nil, signinjournal.ErrStale
		}
		o.binding.Scope = loaded.State.Scope
		configRaw, e := a.pluginStore.unseal("config", instance, "main", configSealed)
		if e != nil {
			return nil, e
		}
		if _, e = o.configChainTx(ctx, tx, uint64(revision), configRaw, loaded.State); e != nil {
			return nil, e
		}
	}
	if err = signinjournal.RestoreOwnerState(&loaded.State, o.binding); err != nil {
		return nil, err
	}
	if err = o.writeTx(ctx, tx, loaded, loaded.State); err != nil {
		return nil, err
	}
	proof, err := signinJSON(o.binding)
	if err != nil {
		return nil, err
	}
	sealed, err := a.pluginStore.seal("signin_owner", instance, "current", proof)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_owners(instance_id,generation,sealed) VALUES(?,?,?) ON CONFLICT(instance_id) DO UPDATE SET generation=excluded.generation,sealed=excluded.sealed`, instance, generation, sealed); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	ok = true
	return o, nil
}

func (o *pluginAutoSignInOwner) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	if o.active != 0 {
		return signinjournal.ErrBusy
	}
	o.closed = true
	if o.file != nil {
		err := unix.Flock(int(o.file.Fd()), unix.LOCK_UN)
		e := o.file.Close()
		o.file = nil
		if err != nil {
			return err
		}
		return e
	}
	return nil
}
func (o *pluginAutoSignInOwner) backend(run *PluginRun) (*pluginAutoSignInBackend, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.file == nil {
		return nil, signinjournal.ErrStale
	}
	b := &pluginAutoSignInBackend{owner: o}
	if run != nil {
		v := *run
		v.Input = append(json.RawMessage(nil), run.Input...)
		v.Result = nil
		b.run = &v
	}
	return b, nil
}
func (b *pluginAutoSignInBackend) retain(ctx context.Context, binding signinjournal.Binding) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o := b.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.file == nil || binding != o.binding {
		return nil, signinjournal.ErrStale
	}
	o.active++
	var once sync.Once
	return func() { once.Do(func() { o.mu.Lock(); o.active--; o.mu.Unlock() }) }, nil
}
func (b *pluginAutoSignInBackend) AcquireExecution(ctx context.Context, binding signinjournal.Binding) (func(), error) {
	if b.run == nil {
		return nil, signinjournal.ErrStale
	}
	release, err := b.retain(ctx, binding)
	if err != nil {
		return nil, err
	}
	select {
	case b.owner.execution <- struct{}{}:
	default:
		release()
		return nil, signinjournal.ErrBusy
	}
	b.mu.Lock()
	b.held++
	b.mu.Unlock()
	if err = b.View(ctx, binding, func(*signinjournal.State) error { return nil }); err != nil {
		b.mu.Lock()
		b.held--
		b.mu.Unlock()
		<-b.owner.execution
		release()
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { b.mu.Lock(); b.held--; b.mu.Unlock(); <-b.owner.execution; release() }) }, nil
}
func (b *pluginAutoSignInBackend) Hold(ctx context.Context, binding signinjournal.Binding) (func(), error) {
	if b.run != nil {
		return nil, signinjournal.ErrStale
	}
	release, err := b.retain(ctx, binding)
	if err != nil {
		return nil, err
	}
	select {
	case b.owner.execution <- struct{}{}:
	default:
		release()
		return nil, signinjournal.ErrBusy
	}
	b.mu.Lock()
	b.held++
	b.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { b.mu.Lock(); b.held--; b.mu.Unlock(); <-b.owner.execution; release() }) }, nil
}
func (b *pluginAutoSignInBackend) guardTx(ctx context.Context, tx *sql.Tx, binding signinjournal.Binding, state signinjournal.State) error {
	o := b.owner
	o.mu.Lock()
	live := !o.closed && o.file != nil && o.binding == binding
	o.mu.Unlock()
	if !live {
		return signinjournal.ErrStale
	}
	var generation, revision int64
	var plugin, version string
	var installed, enabled bool
	var configSealed []byte
	err := tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.installed,i.desired_enabled,i.generation,v.revision,v.sealed FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, binding.Instance).Scan(&plugin, &version, &installed, &enabled, &generation, &revision, &configSealed)
	if err != nil {
		return err
	}
	if plugin != "AutoSignIn" || version != autosignin.Version || !installed || !enabled || generation < 1 || revision < 1 || uint64(generation) != binding.Generation {
		return signinjournal.ErrStale
	}
	var ownerSealed []byte
	var ownerGeneration uint64
	if err = tx.QueryRowContext(ctx, `SELECT generation,sealed FROM plugin_signin_owners WHERE instance_id=?`, binding.Instance).Scan(&ownerGeneration, &ownerSealed); err != nil {
		return err
	}
	raw, err := o.a.pluginStore.unseal("signin_owner", binding.Instance, "current", ownerSealed)
	if err != nil {
		return err
	}
	var proof signinjournal.Binding
	if cleanupJournalStrict(raw, &proof) != nil || proof != binding || ownerGeneration != binding.Generation {
		return signinjournal.ErrStale
	}
	configRaw, e := o.a.pluginStore.unseal("config", binding.Instance, "main", configSealed)
	if e != nil {
		return e
	}
	admissions, e := o.configChainTx(ctx, tx, uint64(revision), configRaw, state)
	if e != nil {
		return e
	}
	if b.run != nil {
		var actual PluginRun
		var sealed []byte
		err = tx.QueryRowContext(ctx, `SELECT id,instance_id,version,generation,config_revision,action,state,input FROM plugin_runs WHERE id=?`, b.run.ID).Scan(&actual.ID, &actual.InstanceID, &actual.Version, &actual.Generation, &actual.ConfigRevision, &actual.Action, &actual.State, &sealed)
		if err != nil {
			return err
		}
		if actual.State != "running" || actual.ID != b.run.ID || actual.InstanceID != binding.Instance || actual.Version != autosignin.Version || actual.Generation != generation || actual.ConfigRevision != b.run.ConfigRevision || !admissions[uint64(actual.ConfigRevision)] || actual.Action != b.run.Action {
			return signinjournal.ErrStale
		}
		input, e := o.a.pluginStore.unseal("run", binding.Instance, actual.ID, sealed)
		if e != nil {
			return e
		}
		if !bytes.Equal(input, b.run.Input) {
			return signinjournal.ErrStale
		}
	}
	return nil
}
func (b *pluginAutoSignInBackend) View(ctx context.Context, binding signinjournal.Binding, fn func(*signinjournal.State) error) error {
	if fn == nil {
		return signinStoreError()
	}
	release, err := b.retain(ctx, binding)
	if err != nil {
		return err
	}
	defer release()
	b.owner.a.pluginMu.Lock()
	defer b.owner.a.pluginMu.Unlock()
	tx, err := b.owner.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	loaded, err := b.owner.readTx(ctx, tx)
	if err != nil {
		return err
	}
	if err = b.guardTx(ctx, tx, binding, loaded.State); err != nil {
		return err
	}
	return fn(&loaded.State)
}
func (b *pluginAutoSignInBackend) Update(ctx context.Context, binding signinjournal.Binding, fn func(*signinjournal.State) error) error {
	if fn == nil {
		return signinStoreError()
	}
	release, err := b.retain(ctx, binding)
	if err != nil {
		return err
	}
	defer release()
	b.mu.Lock()
	held := b.held > 0
	b.mu.Unlock()
	if !held {
		return signinjournal.ErrStale
	}
	o := b.owner
	o.a.pluginMu.Lock()
	defer o.a.pluginMu.Unlock()
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	loaded, err := o.readTx(ctx, tx)
	if err != nil {
		return err
	}
	if err = b.guardTx(ctx, tx, binding, loaded.State); err != nil {
		return err
	}
	before, err := signinCells(loaded.State)
	if err != nil {
		return err
	}
	if err = fn(&loaded.State); errors.Is(err, signinjournal.ErrNoChange) {
		return nil
	} else if err != nil {
		return err
	}
	if loaded.State.Scope != binding.Scope || loaded.State.Epoch != binding.Epoch {
		return signinjournal.ErrStale
	}
	if err = b.allowedDelta(before, loaded.State); err != nil {
		return err
	}
	if err = o.writeTx(ctx, tx, loaded, loaded.State); err != nil {
		return err
	}
	if err = b.projectConfigTx(ctx, tx, before, loaded.State); err != nil {
		return err
	}
	if err = b.projectSchedulesTx(ctx, tx, before, loaded.State); err != nil {
		return err
	}
	if err = b.projectSiteDeletedSubscriptionTx(ctx, tx, before, loaded.State); err != nil {
		return err
	}

	return tx.Commit()
}

// Maintenance can only settle ordered outbox phases; it cannot fabricate a run,
// mutate config/history, add a request or rewrite a response. A run backend can
// change only the exact trusted running PluginRun's journal and source state.
func (b *pluginAutoSignInBackend) allowedDelta(before map[string]pluginAutoSignInCell, after signinjournal.State) error {
	var header pluginAutoSignInHeader
	if cleanupJournalStrict(before[signinKey("head", "")].Data, &header) != nil {
		return signinStoreError()
	}
	if b.run != nil {
		if after.ActiveRun != "" && after.ActiveRun != b.run.ID {
			return signinjournal.ErrStale
		}
		if header.ActiveRun != "" && header.ActiveRun != b.run.ID {
			return signinjournal.ErrStale
		}
		if after.RuntimeEpoch != header.RuntimeEpoch && after.RuntimeEpoch != b.owner.binding.Epoch {
			return signinjournal.ErrStale
		}
		if run := after.Runs[b.run.ID]; run != nil {
			matches := run.Action == b.run.Action || run.Action == "cold_init" && b.run.Action == "init"
			if !matches && b.run.Action == "event" && run.Action == "site_deleted" && len(b.sourcePlan) > 0 {
				matches = bytes.Equal(run.Plan, b.sourcePlan)
			}
			if !matches {
				return signinjournal.ErrStale
			}
		}
	}
	cells, err := signinCells(after)
	if err != nil {
		return err
	}

	if b.run == nil {
		for key, old := range before {
			if old.Collection != "outbox.head" {
				continue
			}
			next, ok := cells[key]
			if !ok {
				return signinjournal.ErrStale
			}
			if bytes.Equal(old.Data, next.Data) {
				continue
			}
			var a, c pluginAutoSignInOutboxHead
			if cleanupJournalStrict(old.Data, &a) != nil || cleanupJournalStrict(next.Data, &c) != nil {
				return signinStoreError()
			}
			priorPhase, nextPhase := a.Phase, c.Phase
			a.Phase = ""
			c.Phase = ""
			if a != c {
				return signinjournal.ErrStale
			}
			valid := priorPhase == "pending" && nextPhase == "intent" || priorPhase == "intent" && (nextPhase == "uncertain" || nextPhase == "delivered") || priorPhase == "uncertain" && nextPhase == "delivered"
			if !valid {
				return signinjournal.ErrStale
			}
		}
		for key, next := range cells {
			if next.Collection == "outbox.head" {
				if _, ok := before[key]; !ok {
					return signinjournal.ErrStale
				}
			}
		}
	}
	for key, old := range before {
		next, ok := cells[key]
		if ok && bytes.Equal(old.Data, next.Data) && ((old.Data == nil) == (next.Data == nil)) {
			continue
		}
		if err = b.allowedCell(old.Collection, old.Key); err != nil {
			return err
		}
	}
	for key, next := range cells {
		if _, ok := before[key]; !ok {
			if err = b.allowedCell(next.Collection, next.Key); err != nil {
				return err
			}
		}
	}
	return nil
}
func (b *pluginAutoSignInBackend) allowedCell(collection, key string) error {
	if b.run == nil {
		if collection != "outbox.head" {
			return signinjournal.ErrStale
		}
		return nil
	}
	if collection == "run.head" || collection == "run.plan" || collection == "run.baseline" || collection == "run.final" || collection == "run.keys" || collection == "lane" || collection == "entry.head" || collection == "entry.value" {
		parts, n := []string(nil), 2
		if collection == "run.head" || collection == "run.plan" {
			n = 1
		}
		if collection == "entry.head" || collection == "entry.value" {
			n = 3
		}
		var err error
		parts, err = signinParts(key, n)
		if err != nil {
			return err
		}
		if parts[0] != b.run.ID {
			return signinjournal.ErrStale
		}
	}
	return nil
}

// Public config revisions remain monotonic. This private, authenticated chain
// proves that a later public revision was produced by an exact committed source
// effect under the immutable admission binding, rather than by an unrelated
// administrator or a worker-supplied effective-revision number.
func (o *pluginAutoSignInOwner) configChainTx(ctx context.Context, tx *sql.Tx, revision uint64, config []byte, state signinjournal.State) (map[uint64]bool, error) {
	admissions := map[uint64]bool{o.binding.Revision: true}
	if revision < o.binding.Revision {
		return nil, signinjournal.ErrStale
	}
	rows, err := tx.QueryContext(ctx, `SELECT to_revision,sealed FROM plugin_signin_config_receipts WHERE instance_id=? AND generation=? ORDER BY to_revision`, o.binding.Instance, o.binding.Generation)
	if err != nil {
		return nil, err
	}
	from := o.binding.Revision
	previous := ""
	lastHash := ""
	count := 0
	for rows.Next() {
		var to uint64
		var sealed []byte
		if err = rows.Scan(&to, &sealed); err != nil {
			break
		}
		count++
		if count > pluginAutoSignInRowLimit {
			err = signinjournal.ErrCapacity
			break
		}
		raw, e := o.a.pluginStore.unseal("signin_config", o.binding.Instance, o.aad("config", strconv.FormatUint(to, 10), 0), sealed)
		if e != nil {
			err = e
			break
		}
		var r pluginAutoSignInConfigReceipt
		if cleanupJournalStrict(raw, &r) != nil || r.Scope != o.binding.Scope || r.From != from || r.To != to || to != from+1 || r.Previous != previous || !admissions[r.Admission] {
			err = signinjournal.ErrStale
			break
		}
		run := state.Runs[r.RunID]
		if run == nil || run.Scope != o.binding.Scope || r.Index < 0 || r.Index >= len(run.Lanes[r.Lane]) {
			err = signinStoreError()
			break
		}
		entry := run.Lanes[r.Lane][r.Index]
		if entry.Kind != "config" || entry.Phase != "applied" || entry.Digest != r.Digest || r.ConfigHash != hex.EncodeToString(o.mac("config-value", entry.Value)) {
			err = signinStoreError()
			break
		}
		from = to
		admissions[to] = true
		lastHash = r.ConfigHash
		previous = hex.EncodeToString(o.mac("config-receipt", raw))
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if from != revision || !bytes.Equal(config, state.Config) || lastHash != "" && lastHash != hex.EncodeToString(o.mac("config-value", config)) {
		return nil, signinjournal.ErrStale
	}
	return admissions, nil
}
func (b *pluginAutoSignInBackend) projectConfigTx(ctx context.Context, tx *sql.Tx, before map[string]pluginAutoSignInCell, state signinjournal.State) error {
	if b.run == nil {
		return nil
	}
	o := b.owner
	run := state.Runs[b.run.ID]
	if run == nil {
		return nil
	}
	var found *pluginAutoSignInConfigReceipt
	for lane, entries := range run.Lanes {
		for i, e := range entries {
			if e.Kind != "config" || e.Phase != "applied" {
				continue
			}
			key := signinKey("entry.head", signinKey(run.ID, lane, strconv.Itoa(i)))
			if _, exists := before[key]; exists {
				continue
			}
			if found != nil || !bytes.Equal(e.Value, state.Config) {
				return signinStoreError()
			}
			found = &pluginAutoSignInConfigReceipt{Scope: o.binding.Scope, RunID: run.ID, Lane: lane, Index: i, Digest: e.Digest, Admission: uint64(b.run.ConfigRevision), ConfigHash: hex.EncodeToString(o.mac("config-value", state.Config))}
		}
	}
	if found == nil {
		return nil
	}
	if len(state.Config) > pluginValueLimit || !json.Valid(state.Config) {
		return signinjournal.ErrCapacity
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(state.Config, &fields) != nil || fields == nil {
		return signinStoreError()
	}
	var revision uint64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, o.binding.Instance).Scan(&revision); err != nil {
		return err
	}
	found.From, found.To = revision, revision+1
	if revision > o.binding.Revision {
		var sealed []byte
		if err := tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_signin_config_receipts WHERE instance_id=? AND generation=? AND to_revision=?`, o.binding.Instance, o.binding.Generation, revision).Scan(&sealed); err != nil {
			return err
		}
		raw, err := o.a.pluginStore.unseal("signin_config", o.binding.Instance, o.aad("config", strconv.FormatUint(revision, 10), 0), sealed)
		if err != nil {
			return err
		}
		found.Previous = hex.EncodeToString(o.mac("config-receipt", raw))
	}
	raw, err := signinJSON(found)
	if err != nil {
		return err
	}
	if len(raw) > 16000 {
		return signinjournal.ErrCapacity
	}
	sealed, err := o.a.pluginStore.seal("signin_config", o.binding.Instance, o.aad("config", strconv.FormatUint(found.To, 10), 0), raw)
	if err != nil {
		return err
	}
	if _, err = o.a.pluginStore.saveValueTx(ctx, tx, o.binding.Instance, "config", "main", int64(revision), state.Config); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_config_receipts(instance_id,generation,to_revision,sealed) VALUES(?,?,?,?)`, o.binding.Instance, o.binding.Generation, found.To, sealed)
	return err
}

// Splitting the State interface is deliberately explicit. RawMessage fields are
// opaque bytes (not reencoded), large responses are independent cells, and only
// changed chunks are sealed/written. StateBackend still reconstructs the current
// instance for the pure algorithms; this is bounded, not constant-memory IO.
func signinCells(s signinjournal.State) (map[string]pluginAutoSignInCell, error) {
	if s.Schema != 1 || s.Runs == nil || s.History == nil || s.Cache == nil || s.CacheReady == nil || s.Stats == nil || s.Blocks == nil || s.Jobs == nil {
		return nil, signinStoreError()
	}
	out := map[string]pluginAutoSignInCell{}
	put := func(c, k string, v []byte) { out[signinKey(c, k)] = pluginAutoSignInCell{c, k, v} }
	obj := func(c, k string, v any) error {
		raw, e := signinJSON(v)
		if e != nil {
			return e
		}
		put(c, k, raw)
		return nil
	}
	if err := obj("head", "", pluginAutoSignInHeader{s.Schema, s.Scope, s.Epoch, s.ActiveRun, s.RuntimeEpoch}); err != nil {
		return nil, err
	}
	put("config", "", s.Config)
	put("runtime", "", s.Runtime)
	put("services", "", s.Services)
	if err := obj("keys", "", s.Keys); err != nil {
		return nil, err
	}
	for k, v := range s.History {
		put("history", k, v)
	}
	for k, v := range s.Cache {
		put("cache", k, v)
	}
	for k, v := range s.CacheReady {
		if err := obj("cache_ready", k, v); err != nil {
			return nil, err
		}
	}
	for k, v := range s.Stats {
		if err := obj("stats", k, v); err != nil {
			return nil, err
		}
	}
	for k, v := range s.Blocks {
		if err := obj("blocks", k, v); err != nil {
			return nil, err
		}
	}
	for k, v := range s.Jobs {
		put("jobs", k, v)
	}
	for id, r := range s.Runs {
		if r == nil || r.ID != id || r.Lanes == nil {
			return nil, signinStoreError()
		}
		if err := obj("run.head", signinKey(id), pluginAutoSignInRunHead{r.ID, r.Action, r.Epoch, r.Phase, r.Scope, r.Baseline.Timezone}); err != nil {
			return nil, err
		}
		put("run.plan", signinKey(id), r.Plan)
		for _, v := range []struct {
			k   string
			raw []byte
		}{{"runtime", r.Baseline.Runtime}, {"history", r.Baseline.History}, {"inventory", r.Baseline.Inventory}, {"config", r.Baseline.Config}} {
			put("run.baseline", signinKey(id, v.k), v.raw)
		}
		if err := obj("run.keys", signinKey(id, "keys"), r.Baseline.Keys); err != nil {
			return nil, err
		}
		put("run.final", signinKey(id, "runtime"), r.FinalRuntime)
		put("run.final", signinKey(id, "result"), r.Result)
		for lane, entries := range r.Lanes {
			if err := obj("lane", signinKey(id, lane), len(entries)); err != nil {
				return nil, err
			}
			for i, e := range entries {
				key := signinKey(id, lane, strconv.Itoa(i))
				if err := obj("entry.head", key, pluginAutoSignInEntryHead{e.Kind, e.Digest, e.Phase, e.Error, e.ErrorClass, e.Guard}); err != nil {
					return nil, err
				}
				put("entry.value", key, e.Value)
			}
		}
	}
	for i, v := range s.Outbox {
		key := strconv.Itoa(i)
		if err := obj("outbox.head", key, pluginAutoSignInOutboxHead{v.ID, v.Kind, v.Phase, v.RunID, v.Lane, v.Index}); err != nil {
			return nil, err
		}
		put("outbox.payload", key, v.Payload)
	}
	return out, nil
}

func (o *pluginAutoSignInOwner) aggregate(rows map[string]pluginAutoSignInPhysical) ([]byte, int) {
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	m := hmac.New(sha256.New, o.a.Store.key)
	m.Write(o.mac("manifest", nil))
	size := 0
	for _, k := range keys {
		r := rows[k]
		raw, _ := json.Marshal([]any{r.Collection, r.Token, r.Part, r.Size, hex.EncodeToString(r.Digest)})
		m.Write(raw)
		m.Write([]byte{0})
		size += r.Size
	}
	return m.Sum(nil), size
}
func (o *pluginAutoSignInOwner) readTx(ctx context.Context, tx *sql.Tx) (pluginAutoSignInRead, error) {
	out := pluginAutoSignInRead{Cells: map[string]pluginAutoSignInCell{}, Rows: map[string]pluginAutoSignInPhysical{}}
	var sealed, digest []byte
	err := tx.QueryRowContext(ctx, `SELECT sealed,digest FROM plugin_signin_meta WHERE instance_id=? AND generation=?`, o.binding.Instance, o.binding.Generation).Scan(&sealed, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_signin_rows WHERE instance_id=? AND generation=?`, o.binding.Instance, o.binding.Generation).Scan(&count)
		if e != nil {
			return out, e
		}
		if count != 0 {
			return out, signinStoreError()
		}
		return out, err
	}
	if err != nil {
		return out, err
	}
	raw, err := o.a.pluginStore.unseal("signin_journal", o.binding.Instance, o.aad("meta", "", 0), sealed)
	if err != nil {
		return out, err
	}
	if !hmac.Equal(digest, o.mac("meta", raw)) {
		return out, signinStoreError()
	}
	var meta pluginAutoSignInMeta
	if cleanupJournalStrict(raw, &meta) != nil || meta.Version != 1 || meta.Scope.Instance != o.binding.Instance || meta.Scope.Generation != o.binding.Generation || meta.Count < 0 || meta.Count > pluginAutoSignInRowLimit || meta.Bytes < 0 || meta.Bytes > pluginAutoSignInCapacity {
		return out, signinStoreError()
	}
	rows, err := tx.QueryContext(ctx, `SELECT collection,token,part,sealed,digest,size FROM plugin_signin_rows WHERE instance_id=? AND generation=? ORDER BY collection,token,part`, o.binding.Instance, o.binding.Generation)
	if err != nil {
		return out, err
	}
	groups := map[string][]pluginAutoSignInRow{}
	total := 0
	for rows.Next() {
		var p pluginAutoSignInPhysical
		var cipher []byte
		if err = rows.Scan(&p.Collection, &p.Token, &p.Part, &cipher, &p.Digest, &p.Size); err != nil {
			break
		}
		if len(out.Rows) >= pluginAutoSignInRowLimit || p.Part < 0 || p.Size < 1 || p.Size > pluginValueLimit || len(cipher) > pluginValueLimit+64 {
			err = signinStoreError()
			break
		}
		raw, err = o.a.pluginStore.unseal("signin_journal", o.binding.Instance, o.aad(p.Collection, p.Token, p.Part), cipher)
		if err != nil {
			break
		}
		if len(raw) != p.Size || !hmac.Equal(p.Digest, o.mac("row", raw)) {
			err = signinStoreError()
			break
		}
		var r pluginAutoSignInRow
		if cleanupJournalStrict(raw, &r) != nil || r.Version != 1 || r.Instance != o.binding.Instance || r.Generation != o.binding.Generation || r.Collection != p.Collection || r.Token != p.Token || r.Part != p.Part || r.Parts < 1 || r.Part >= r.Parts || len(r.Data) > pluginAutoSignInChunk || r.Token != o.token(r.Collection, r.Key) {
			err = signinStoreError()
			break
		}
		total += p.Size
		if total > pluginAutoSignInCapacity {
			err = signinjournal.ErrCapacity
			break
		}
		out.Rows[signinPhysicalKey(p)] = p
		groups[signinKey(r.Collection, r.Key)] = append(groups[signinKey(r.Collection, r.Key)], r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	agg, size := o.aggregate(out.Rows)
	if len(out.Rows) != meta.Count || size != meta.Bytes || !hmac.Equal(agg, meta.Digest) {
		return out, signinStoreError()
	}
	for key, parts := range groups {
		r := parts[0]
		if len(parts) != r.Parts {
			return out, signinStoreError()
		}
		var data []byte
		if !r.Nil {
			data = []byte{}
		}
		for i, p := range parts {
			if p.Part != i || p.Parts != r.Parts || p.Nil != r.Nil || p.Key != r.Key || p.Collection != r.Collection || p.Nil && (p.Parts != 1 || len(p.Data) != 0) || i < len(parts)-1 && len(p.Data) != pluginAutoSignInChunk {
				return out, signinStoreError()
			}
			data = append(data, p.Data...)
		}
		out.Cells[key] = pluginAutoSignInCell{r.Collection, r.Key, data}
	}
	out.State, err = signinInflate(meta, out.Cells)
	out.MetaDigest = digest
	return out, err
}

func signinInflate(meta pluginAutoSignInMeta, cells map[string]pluginAutoSignInCell) (signinjournal.State, error) {
	s, err := signinjournal.NewState(meta.Scope, nil)
	if err != nil {
		return s, err
	}
	s.Epoch = meta.Epoch
	s.ActiveRun = meta.ActiveRun
	s.RuntimeEpoch = meta.RuntimeEpoch
	get := func(c, k string) ([]byte, error) {
		v, ok := cells[signinKey(c, k)]
		if !ok {
			return nil, signinStoreError()
		}
		return v.Data, nil
	}
	for _, c := range []string{"config", "runtime", "services"} {
		raw, e := get(c, "")
		if e != nil {
			return s, e
		}
		switch c {
		case "config":
			s.Config = raw
		case "runtime":
			s.Runtime = raw
		case "services":
			s.Services = raw
		}
	}
	raw, e := get("keys", "")
	if e != nil || cleanupJournalStrict(raw, &s.Keys) != nil {
		return s, signinStoreError()
	}
	// Allocate heads first, independent of SQL/map iteration order.
	for _, c := range cells {
		if c.Collection == "run.head" {
			parts, e := signinParts(c.Key, 1)
			var h pluginAutoSignInRunHead
			if e != nil || cleanupJournalStrict(c.Data, &h) != nil || h.ID != parts[0] {
				return s, signinStoreError()
			}
			s.Runs[h.ID] = &signinjournal.Run{ID: h.ID, Action: h.Action, Epoch: h.Epoch, Phase: h.Phase, Scope: h.Scope, Baseline: signinjournal.Baseline{Timezone: h.Timezone}, Lanes: map[string][]signinjournal.Entry{}}
		}
	}
	for _, c := range cells {
		if c.Collection == "lane" {
			p, e := signinParts(c.Key, 2)
			var count int
			if e != nil || cleanupJournalStrict(c.Data, &count) != nil || count < 0 || count > pluginAutoSignInRowLimit || s.Runs[p[0]] == nil {
				return s, signinStoreError()
			}
			s.Runs[p[0]].Lanes[p[1]] = make([]signinjournal.Entry, count)
		}
	}
	maxOutbox := -1
	for _, c := range cells {
		if c.Collection == "outbox.head" {
			n, e := strconv.Atoi(c.Key)
			if e != nil || n < 0 || n > pluginAutoSignInRowLimit {
				return s, signinStoreError()
			}
			if n > maxOutbox {
				maxOutbox = n
			}
		}
	}
	if maxOutbox >= 0 {
		s.Outbox = make([]signinjournal.OutboxItem, maxOutbox+1)
	}
	for _, c := range cells {
		switch c.Collection {
		case "head", "config", "runtime", "services", "keys", "run.head", "lane":
		case "history":
			s.History[c.Key] = c.Data
		case "cache":
			s.Cache[c.Key] = c.Data
		case "cache_ready":
			var v bool
			if cleanupJournalStrict(c.Data, &v) != nil {
				return s, signinStoreError()
			}
			s.CacheReady[c.Key] = v
		case "stats":
			var v signinjournal.Statistic
			if cleanupJournalStrict(c.Data, &v) != nil {
				return s, signinStoreError()
			}
			s.Stats[c.Key] = v
		case "blocks":
			var v string
			if cleanupJournalStrict(c.Data, &v) != nil {
				return s, signinStoreError()
			}
			s.Blocks[c.Key] = v
		case "jobs":
			s.Jobs[c.Key] = c.Data
		case "run.plan", "run.baseline", "run.keys", "run.final":
			n := 2
			if c.Collection == "run.plan" {
				n = 1
			}
			p, e := signinParts(c.Key, n)
			if e != nil || s.Runs[p[0]] == nil {
				return s, signinStoreError()
			}
			r := s.Runs[p[0]]
			switch c.Collection {
			case "run.plan":
				r.Plan = c.Data
			case "run.keys":
				if p[1] != "keys" || cleanupJournalStrict(c.Data, &r.Baseline.Keys) != nil {
					return s, signinStoreError()
				}
			case "run.final":
				switch p[1] {
				case "runtime":
					r.FinalRuntime = c.Data
				case "result":
					r.Result = c.Data
				default:
					return s, signinStoreError()
				}
			case "run.baseline":
				switch p[1] {
				case "runtime":
					r.Baseline.Runtime = c.Data
				case "history":
					r.Baseline.History = c.Data
				case "inventory":
					r.Baseline.Inventory = c.Data
				case "config":
					r.Baseline.Config = c.Data
				default:
					return s, signinStoreError()
				}
			}
		case "entry.head", "entry.value":
			p, e := signinParts(c.Key, 3)
			if e != nil || s.Runs[p[0]] == nil {
				return s, signinStoreError()
			}
			r := s.Runs[p[0]]
			i, e := strconv.Atoi(p[2])
			if e != nil || i < 0 || i >= len(r.Lanes[p[1]]) {
				return s, signinStoreError()
			}
			v := r.Lanes[p[1]][i]
			if c.Collection == "entry.value" {
				v.Value = c.Data
			} else {
				var h pluginAutoSignInEntryHead
				if cleanupJournalStrict(c.Data, &h) != nil {
					return s, signinStoreError()
				}
				v.Kind = h.Kind
				v.Digest = h.Digest
				v.Phase = h.Phase
				v.Error = h.Error
				v.ErrorClass = h.ErrorClass
				v.Guard = h.Guard
			}
			r.Lanes[p[1]][i] = v
		case "outbox.head", "outbox.payload":
			i, e := strconv.Atoi(c.Key)
			if e != nil || i < 0 || i >= len(s.Outbox) {
				return s, signinStoreError()
			}
			v := s.Outbox[i]
			if c.Collection == "outbox.payload" {
				v.Payload = c.Data
			} else {
				var h pluginAutoSignInOutboxHead
				if cleanupJournalStrict(c.Data, &h) != nil {
					return s, signinStoreError()
				}
				v.ID = h.ID
				v.Kind = h.Kind
				v.Phase = h.Phase
				v.RunID = h.RunID
				v.Lane = h.Lane
				v.Index = h.Index
			}
			s.Outbox[i] = v
		default:
			return s, signinStoreError()
		}
	}
	// A canonical complete projection also detects missing companion cells and
	// noncontiguous lanes/outbox indices, beyond the authenticated row manifest.
	canonical, err := signinCells(s)
	if err != nil || len(canonical) != len(cells) {
		return s, signinStoreError()
	}
	for k, v := range canonical {
		old, ok := cells[k]
		if !ok || !bytes.Equal(v.Data, old.Data) || ((v.Data == nil) != (old.Data == nil)) {
			return s, signinStoreError()
		}
	}
	return s, nil
}

func (o *pluginAutoSignInOwner) writeTx(ctx context.Context, tx *sql.Tx, old pluginAutoSignInRead, s signinjournal.State) error {
	if tx == nil || s.Schema != 1 || s.Scope != o.binding.Scope || s.Epoch != o.binding.Epoch {
		return signinjournal.ErrStale
	}
	cells, err := signinCells(s)
	if err != nil {
		return err
	}
	rows := map[string]pluginAutoSignInPhysical{}
	pending := 0
	for _, r := range s.Runs {
		for _, lane := range r.Lanes {
			for _, e := range lane {
				if e.Phase == "intent" || e.Phase == "uncertain" {
					pending++
				}
				if e.Phase == "receipt" && len(e.Value) > pluginAutoSignInResponseLimit {
					return signinjournal.ErrCapacity
				}
			}
		}
	}
	capacity, reserve := pluginAutoSignInCapacity, pluginAutoSignInResponseReserve
	if o.testCapacity > 0 {
		capacity = o.testCapacity
	}
	if o.testReserve > 0 {
		reserve = o.testReserve
	}
	if pending > capacity/reserve {
		return signinjournal.ErrCapacity
	}
	for key, c := range cells {
		if !utf8.ValidString(c.Collection) || !utf8.ValidString(c.Key) {
			return signinStoreError()
		}
		if (c.Collection == "entry.head" || c.Collection == "entry.value") && len(c.Key) > 1024 {
			return signinjournal.ErrCapacity
		}
		token := o.token(c.Collection, c.Key)
		prev, exists := old.Cells[key]
		if exists && bytes.Equal(prev.Data, c.Data) && ((prev.Data == nil) == (c.Data == nil)) {
			parts := (len(c.Data) + pluginAutoSignInChunk - 1) / pluginAutoSignInChunk
			if parts == 0 {
				parts = 1
			}
			for i := 0; i < parts; i++ {
				k := signinPhysicalKey(pluginAutoSignInPhysical{Collection: c.Collection, Token: token, Part: i})
				r, ok := old.Rows[k]
				if !ok {
					return signinStoreError()
				}
				rows[k] = r
			}
			continue
		}
		parts := (len(c.Data) + pluginAutoSignInChunk - 1) / pluginAutoSignInChunk
		if parts == 0 {
			parts = 1
		}
		for i := 0; i < parts; i++ {
			start, end := i*pluginAutoSignInChunk, (i+1)*pluginAutoSignInChunk
			if end > len(c.Data) {
				end = len(c.Data)
			}
			r := pluginAutoSignInRow{1, o.binding.Instance, o.binding.Generation, c.Collection, token, c.Key, i, parts, c.Data == nil, c.Data[start:end]}
			raw, e := signinJSON(r)
			if e != nil {
				return e
			}
			if len(raw) > pluginValueLimit {
				return signinjournal.ErrCapacity
			}
			sealed, e := o.a.pluginStore.seal("signin_journal", o.binding.Instance, o.aad(c.Collection, token, i), raw)
			if e != nil {
				return e
			}
			p := pluginAutoSignInPhysical{Collection: c.Collection, Token: token, Part: i, Size: len(raw), Digest: o.mac("row", raw), Sealed: sealed}
			rows[signinPhysicalKey(p)] = p
		}
		if len(rows) > pluginAutoSignInRowLimit {
			return signinjournal.ErrCapacity
		}
	}
	if len(rows) > pluginAutoSignInRowLimit {
		return signinjournal.ErrCapacity
	}
	agg, size := o.aggregate(rows)
	var receiptBytes int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(LENGTH(sealed)),0) FROM plugin_signin_config_receipts WHERE instance_id=? AND generation=?`, o.binding.Instance, o.binding.Generation).Scan(&receiptBytes); err != nil {
		return err
	}
	// Reserve one maximum private config receipt as well as ciphertext framing.
	// Config receipts themselves are bounded below, before their SQL publication.
	if size+len(rows)*28+receiptBytes+16384 > capacity-pending*reserve {
		return signinjournal.ErrCapacity
	}
	meta := pluginAutoSignInMeta{1, s.Scope, s.Epoch, s.ActiveRun, s.RuntimeEpoch, len(rows), size, agg}
	raw, err := signinJSON(meta)
	if err != nil {
		return err
	}
	if len(raw) > pluginValueLimit {
		return signinjournal.ErrCapacity
	}
	metaDigest := o.mac("meta", raw)
	// All admission/capacity/serialization checks above precede every SQL write.
	for _, r := range rows {
		if r.Sealed == nil {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_rows(instance_id,generation,collection,token,part,sealed,digest,size) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,generation,collection,token,part) DO UPDATE SET sealed=excluded.sealed,digest=excluded.digest,size=excluded.size`, o.binding.Instance, o.binding.Generation, r.Collection, r.Token, r.Part, r.Sealed, r.Digest, r.Size); err != nil {
			return err
		}
	}
	for key, r := range old.Rows {
		if _, exists := rows[key]; !exists {
			if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_signin_rows WHERE instance_id=? AND generation=? AND collection=? AND token=? AND part=?`, o.binding.Instance, o.binding.Generation, r.Collection, r.Token, r.Part); err != nil {
				return err
			}
		}
	}
	if !hmac.Equal(metaDigest, old.MetaDigest) {
		sealed, e := o.a.pluginStore.seal("signin_journal", o.binding.Instance, o.aad("meta", "", 0), raw)
		if e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_meta(instance_id,generation,sealed,digest) VALUES(?,?,?,?) ON CONFLICT(instance_id,generation) DO UPDATE SET sealed=excluded.sealed,digest=excluded.digest`, o.binding.Instance, o.binding.Generation, sealed, metaDigest)
	}
	return err
}

// Used by the trusted owner after a storage/reply failure in the same epoch.
// No request is retried and no response is invented. It uses the same pure
// recovery validation as exclusive process takeover inside this SQL transaction.
func (o *pluginAutoSignInOwner) recoverUnknown(ctx context.Context, run PluginRun) error {
	b, err := o.backend(&run)
	if err != nil {
		return err
	}
	release, err := b.AcquireExecution(ctx, o.binding)
	if err != nil {
		return err
	}
	defer release()
	return b.Update(ctx, o.binding, func(s *signinjournal.State) error { return signinjournal.RestoreOwnerState(s, o.binding) })
}
