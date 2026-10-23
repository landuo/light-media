package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins/autosignin"
	signinjournal "light-media/internal/plugins/autosignin/durable"
)

// The original global TEMP/signin/tjupt.json is intentionally replaced by the
// existing per-instance durable Cache["tjupt"], not another filesystem cache.
const AutoSignInTjuptSourceSHA256 = "ed717891d30abcac798407c8889776ec79191e447b4ef7f97aee1bddb0f98bb0"
const autoSignInTjuptCacheBytesLimit = 8 << 20
const autoSignInTjuptCacheTransferLimit int64 = 12 << 20

// Destination is a short-lived compare-and-swap admission, not a worker grant.
// A fresh export supplies it. Any owner/config/state change invalidates it.
type AutoSignInTjuptCacheDestination struct {
	InstanceID        string `json:"instance_id"`
	PluginID          string `json:"plugin_id"`
	PluginVersion     string `json:"plugin_version"`
	Implementation    string `json:"implementation"`
	Generation        uint64 `json:"generation"`
	ConfigRevision    uint64 `json:"config_revision"`
	AdmissionRevision uint64 `json:"admission_revision"`
	OwnerEpoch        string `json:"owner_epoch"`
	StateDigest       string `json:"state_digest"`
	Proof             string `json:"proof"`
}

// Bytes is JSON base64, never a path and never a decoded JSON object. In
// particular malformed source JSON and non-object JSON remain the same bytes;
// importing must not turn a later source parse/type failure into an empty cache.
// "source-bytes" is caller-supplied, unauthenticated original-source evidence.
// "sealed-export" authenticates only a snapshot observed by this local store;
// neither the pinned source SHA nor a local seal attests a production origin.
type AutoSignInTjuptCachePayload struct {
	Mode         string `json:"mode"`
	SourceSHA256 string `json:"source_sha256"`
	Key          string `json:"key"`
	Present      bool   `json:"present"`
	ReadyPresent bool   `json:"ready_present"`
	Ready        bool   `json:"ready"`
	Bytes        []byte `json:"bytes"`
	SHA256       string `json:"sha256"`
	Proof        string `json:"proof"`
}

type AutoSignInTjuptCacheTransfer struct {
	Version     int                             `json:"version"`
	Destination AutoSignInTjuptCacheDestination `json:"destination"`
	Cache       AutoSignInTjuptCachePayload     `json:"cache"`
}

type AutoSignInTjuptCacheReceipt struct {
	State          string `json:"state"`
	InstanceID     string `json:"instance_id"`
	Generation     uint64 `json:"generation"`
	ConfigRevision uint64 `json:"config_revision"`
	InputTrust     string `json:"input_trust"`
	Bytes          int    `json:"bytes"`
	SHA256         string `json:"sha256"`
	JobsQueued     int    `json:"jobs_queued"`
	ExternalCalls  int    `json:"external_calls"`
	ConfigChanged  bool   `json:"config_changed"`
	RuntimeChanged bool   `json:"runtime_changed"`
}

func invalidAutoSignInTjuptCache() error {
	return bad("AUTOSIGNIN_TJUPT_CACHE_INVALID", "Tjupt 缓存格式、归属或完整性核对失败，未导入")
}

// Borrow an existing process owner, or hold its existing flock without claiming
// a new epoch. Even cold exports and rejected imports never initialize, restore,
// recover, queue or mutate state. All acquisition is nonblocking: an execution
// in flight is rejected rather than imported immediately after it finishes.
func (a *App) autoSignInTjuptCacheOwner(ctx context.Context, instance string) (*pluginAutoSignInOwner, func(), error) {
	if a == nil || a.Store == nil || a.Store.lock == nil || a.pluginStore == nil || !pluginStorageID.MatchString(instance) {
		return nil, nil, signinjournal.ErrStale
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	r := &a.pluginAutoSignIn
	r.mu.Lock()
	slot, closing := r.owners[instance], r.closing
	r.mu.Unlock()
	if closing {
		return nil, nil, signinjournal.ErrStale
	}
	if slot != nil {
		select {
		case <-slot.ready:
		default:
			return nil, nil, signinjournal.ErrBusy
		}
		if slot.err != nil {
			return nil, nil, signinjournal.ErrStale
		}
		select {
		case slot.gate <- struct{}{}:
		default:
			return nil, nil, signinjournal.ErrBusy
		}
		o := slot.owner
		if o == nil {
			<-slot.gate
			return nil, nil, signinjournal.ErrStale
		}
		b, err := o.backend(nil)
		if err != nil {
			<-slot.gate
			return nil, nil, err
		}
		release, err := b.Hold(ctx, o.binding)
		if err != nil {
			<-slot.gate
			return nil, nil, err
		}
		return o, func() { release(); <-slot.gate }, nil
	}
	name := sha256.Sum256([]byte(instance))
	path := filepath.Join(filepath.Dir(a.Store.lock.Name()), ".signin-owner-"+hex.EncodeToString(name[:])+".lock")
	// O_CREAT is deliberately absent. A missing initialized owner is not an
	// invitation to provision state or run the plugin just to import a cache.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, signinjournal.ErrStale
	}
	f := os.NewFile(uintptr(fd), path)
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, nil, signinjournal.ErrBusy
	}
	o := &pluginAutoSignInOwner{a: a, file: f, execution: make(chan struct{}, 1)}
	return o, func() { _ = o.Close() }, nil
}

func (o *pluginAutoSignInOwner) tjuptCacheCurrentTx(ctx context.Context, tx *sql.Tx, instance string) (pluginAutoSignInRead, AutoSignInTjuptCacheDestination, error) {
	var empty pluginAutoSignInRead
	var d AutoSignInTjuptCacheDestination
	var sealed []byte
	var generation uint64
	if err := tx.QueryRowContext(ctx, `SELECT generation,sealed FROM plugin_signin_owners WHERE instance_id=?`, instance).Scan(&generation, &sealed); err != nil {
		return empty, d, err
	}
	raw, err := o.a.pluginStore.unseal("signin_owner", instance, "current", sealed)
	if err != nil {
		return empty, d, err
	}
	var binding signinjournal.Binding
	if cleanupJournalStrict(raw, &binding) != nil || binding.Instance != instance || binding.Generation != generation || generation == 0 || binding.Revision == 0 || binding.Implementation != autoSignInImplementation || binding.Epoch == "" {
		return empty, d, signinjournal.ErrStale
	}
	if o.binding.Instance == "" {
		o.binding = binding // cold, exclusively flocked reader; no SQL owner claim.
	} else if o.binding != binding {
		return empty, d, signinjournal.ErrStale
	}
	loaded, err := o.readTx(ctx, tx)
	if err != nil {
		return empty, d, err
	}
	if loaded.State.Scope != binding.Scope || loaded.State.Epoch != binding.Epoch {
		return empty, d, signinjournal.ErrStale
	}
	b, err := o.backend(nil)
	if err != nil {
		return empty, d, err
	}
	if err = b.guardTx(ctx, tx, binding, loaded.State); err != nil {
		return empty, d, err
	}
	if err = tjuptCacheIdleState(loaded.State); err != nil {
		return empty, d, err
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND state IN ('pending','running','reconcile_pending')`,
		`SELECT COUNT(*) FROM plugin_schedule_occurrences WHERE instance_id=? AND state IN ('queued','running')`,
		`SELECT COUNT(*) FROM plugin_event_batches WHERE instance_id=? AND state='collecting' AND run_id IS NULL`,
	} {
		var count int
		if err = tx.QueryRowContext(ctx, query, instance).Scan(&count); err != nil {
			return empty, d, err
		}
		if count != 0 {
			return empty, d, signinjournal.ErrBusy
		}
	}
	var revision uint64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, instance).Scan(&revision); err != nil {
		return empty, d, err
	}
	d = AutoSignInTjuptCacheDestination{InstanceID: instance, PluginID: "AutoSignIn", PluginVersion: autosignin.Version, Implementation: binding.Implementation, Generation: binding.Generation, ConfigRevision: revision, AdmissionRevision: binding.Revision, OwnerEpoch: binding.Epoch, StateDigest: hex.EncodeToString(loaded.MetaDigest)}
	d.Proof, err = o.tjuptCacheProof("destination", d)
	return loaded, d, err
}

func tjuptCacheIdleState(s signinjournal.State) error {
	if s.ActiveRun != "" || len(s.Blocks) != 0 {
		return signinjournal.ErrBusy
	}
	var runtime autosignin.Object
	if autosignin.Decode(s.Runtime, &runtime) != nil || runtime == nil || !json.Valid(s.Config) {
		return signinjournal.ErrStore
	}
	for id, run := range s.Runs {
		if run == nil || run.ID != id || run.Scope != s.Scope || run.Lanes == nil {
			return signinjournal.ErrStore
		}
		switch run.Phase {
		case "completed", "failed_source", "abandoned":
		case "running", "interrupted":
			return signinjournal.ErrBusy
		default:
			return signinjournal.ErrStore
		}
		for _, entries := range run.Lanes {
			for _, entry := range entries {
				switch entry.Phase {
				case "intent", "uncertain":
					return signinjournal.ErrUncertain
				case "applied", "receipt", "known_error":
				default:
					return signinjournal.ErrStore
				}
			}
		}
	}
	for _, item := range s.Outbox {
		switch item.Phase {
		case "delivered":
		case "pending", "intent", "uncertain":
			return signinjournal.ErrBusy
		default:
			return signinjournal.ErrStore
		}
	}
	return nil
}

func (o *pluginAutoSignInOwner) tjuptCacheProof(kind string, value any) (string, error) {
	raw, err := signinJSON(value)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(o.mac("tjupt-cache-transfer-v1/"+kind, raw)), nil
}

func (o *pluginAutoSignInOwner) tjuptCachePayloadProof(d AutoSignInTjuptCacheDestination, c AutoSignInTjuptCachePayload) (string, error) {
	// An archived local payload can be paired with a freshly exported admission
	// from the same instance/generation/config scope, but never another clone.
	d.OwnerEpoch, d.StateDigest, d.Proof, c.Proof = "", "", "", ""
	return o.tjuptCacheProof("payload", AutoSignInTjuptCacheTransfer{Version: 1, Destination: d, Cache: c})
}

func (a *App) ExportAutoSignInTjuptCache(ctx context.Context, instance string) (AutoSignInTjuptCacheTransfer, error) {
	var out AutoSignInTjuptCacheTransfer
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	o, release, err := a.autoSignInTjuptCacheOwner(ctx, instance)
	if err != nil {
		return out, err
	}
	defer release()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	loaded, d, err := o.tjuptCacheCurrentTx(ctx, tx, instance)
	if err != nil {
		return out, err
	}
	data, present := loaded.State.Cache["tjupt"]
	ready, readyPresent := loaded.State.CacheReady["tjupt"]
	if len(data) > autoSignInTjuptCacheBytesLimit {
		return out, signinjournal.ErrCapacity
	}
	out = AutoSignInTjuptCacheTransfer{Version: 1, Destination: d, Cache: AutoSignInTjuptCachePayload{Mode: "sealed-export", SourceSHA256: AutoSignInTjuptSourceSHA256, Key: "tjupt", Present: present, ReadyPresent: readyPresent, Ready: ready, Bytes: signinTypedClone(data), SHA256: sourceSettingsDigest(data)}}
	out.Cache.Proof, err = o.tjuptCachePayloadProof(d, out.Cache)
	return out, err
}

func (a *App) ImportAutoSignInTjuptCache(ctx context.Context, instance string, raw []byte) (AutoSignInTjuptCacheReceipt, error) {
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	return a.importAutoSignInTjuptCacheLocked(ctx, instance, raw)
}

// The HTTP lifecycle dispatcher already holds pluginLifecycleMu. No caller may
// use this operation as a worker capability or pass it an untrusted file path.
func (a *App) importAutoSignInTjuptCacheLocked(ctx context.Context, instance string, raw []byte) (AutoSignInTjuptCacheReceipt, error) {
	return a.importAutoSignInTjuptCacheCommitLocked(ctx, instance, raw, nil)
}

// commit is a private test seam for a lost SQL commit acknowledgment. It is not
// selectable by a worker, HTTP field, environment variable or configuration.
func (a *App) importAutoSignInTjuptCacheCommitLocked(ctx context.Context, instance string, raw []byte, commit func(*sql.Tx) error) (AutoSignInTjuptCacheReceipt, error) {
	var receipt AutoSignInTjuptCacheReceipt
	var in AutoSignInTjuptCacheTransfer
	if len(raw) == 0 || int64(len(raw)) > autoSignInTjuptCacheTransferLimit || sourceSettingsJSON(raw, &in) != nil || in.Version != 1 || in.Destination.InstanceID != instance || len(in.Cache.Bytes) > autoSignInTjuptCacheBytesLimit {
		return receipt, invalidAutoSignInTjuptCache()
	}
	// Exact field sets also reject absent/null booleans and byte strings instead
	// of silently interpreting them as Go zero values.
	root, err := sourceSettingsObject(raw, "version", "destination", "cache")
	if err != nil {
		return receipt, invalidAutoSignInTjuptCache()
	}
	destination, err := sourceSettingsObject(root["destination"], "instance_id", "plugin_id", "plugin_version", "implementation", "generation", "config_revision", "admission_revision", "owner_epoch", "state_digest", "proof")
	if err != nil {
		return receipt, invalidAutoSignInTjuptCache()
	}
	cache, err := sourceSettingsObject(root["cache"], "mode", "source_sha256", "key", "present", "ready_present", "ready", "bytes", "sha256", "proof")
	if err != nil {
		return receipt, invalidAutoSignInTjuptCache()
	}
	for key, value := range cache {
		if key != "bytes" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return receipt, invalidAutoSignInTjuptCache()
		}
	}
	for _, value := range destination {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return receipt, invalidAutoSignInTjuptCache()
		}
	}
	if in.Cache.SourceSHA256 != AutoSignInTjuptSourceSHA256 || in.Cache.Key != "tjupt" || !in.Cache.Present || in.Cache.SHA256 != sourceSettingsDigest(in.Cache.Bytes) || !in.Cache.ReadyPresent && in.Cache.Ready {
		return receipt, invalidAutoSignInTjuptCache()
	}
	switch in.Cache.Mode {
	case "source-bytes":
		if in.Cache.Proof != "" || in.Cache.Bytes == nil || !in.Cache.ReadyPresent || !in.Cache.Ready {
			return receipt, invalidAutoSignInTjuptCache()
		}
		receipt.InputTrust = "caller-supplied-source-bytes-not-authenticated"
	case "sealed-export":
		if in.Cache.Proof == "" {
			return receipt, invalidAutoSignInTjuptCache()
		}
		receipt.InputTrust = "local-store-export-authenticated-not-original-source-attestation"
	default:
		return receipt, invalidAutoSignInTjuptCache()
	}
	o, release, err := a.autoSignInTjuptCacheOwner(ctx, instance)
	if err != nil {
		return receipt, err
	}
	defer release()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return receipt, err
	}
	defer tx.Rollback()
	loaded, current, err := o.tjuptCacheCurrentTx(ctx, tx, instance)
	if err != nil {
		return receipt, err
	}
	if in.Destination != current {
		return receipt, signinjournal.ErrStale
	}
	if in.Cache.Mode == "sealed-export" {
		proof, err := o.tjuptCachePayloadProof(current, in.Cache)
		if err != nil || !hmac.Equal([]byte(proof), []byte(in.Cache.Proof)) {
			return receipt, invalidAutoSignInTjuptCache()
		}
	}
	state := loaded.State
	state.Cache["tjupt"] = signinTypedClone(in.Cache.Bytes)
	if in.Cache.ReadyPresent {
		state.CacheReady["tjupt"] = in.Cache.Ready
	} else {
		delete(state.CacheReady, "tjupt")
	}
	if err = o.writeTx(ctx, tx, loaded, state); err != nil {
		return receipt, err
	}
	if commit == nil {
		commit = func(tx *sql.Tx) error { return tx.Commit() }
	}
	if err = commit(tx); err != nil {
		_ = tx.Rollback()
		// Retain the lifecycle, native execution and storage barriers during
		// readback. A lost acknowledgment must not be called a rollback or
		// invite retry if the exact requested state already committed.
		checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		check, readErr := a.Store.DB.BeginTx(checkCtx, &sql.TxOptions{ReadOnly: true})
		if readErr != nil {
			return receipt, signinjournal.ErrUncertain
		}
		defer check.Rollback()
		actual, _, readErr := o.tjuptCacheCurrentTx(checkCtx, check, instance)
		if readErr != nil {
			return receipt, signinjournal.ErrUncertain
		}
		if !reflect.DeepEqual(actual.State, state) {
			if hmac.Equal(actual.MetaDigest, loaded.MetaDigest) {
				return receipt, err // exact authenticated beforeimage, known unapplied.
			}
			return receipt, signinjournal.ErrUncertain
		}
	}
	receipt.State, receipt.InstanceID, receipt.Generation, receipt.ConfigRevision = "imported", instance, current.Generation, current.ConfigRevision
	receipt.Bytes, receipt.SHA256 = len(in.Cache.Bytes), in.Cache.SHA256
	return receipt, nil
}
