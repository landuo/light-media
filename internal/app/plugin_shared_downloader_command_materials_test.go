package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/materialio"
	"light-media/internal/plugins"
	"light-media/internal/plugins/downloadercommands"
)

// These tests exercise the actual consumer owner and current App config guard,
// not the framework ticket issuer, worker, native factory or any HTTP endpoint.
// A failed claim must publish neither a snapshot nor bytes to its downstream.
type ownedMaterialConsumerFixture struct {
	ctx                           context.Context
	cancel                        context.CancelFunc
	app                           *App
	owner                         *originalDownloaderCommandUploads
	input                         *originalDownloaderCommandSnapshot
	active                        *atomic.Bool
	handle, capability, directory string
	closeOwner                    func() error
	closes                        atomic.Int32
}

func newOwnedMaterialConsumerFixture(t *testing.T, capability string, total int64, digest [sha256.Size]byte, hook func(context.Context) error) *ownedMaterialConsumerFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f := &ownedMaterialConsumerFixture{ctx: ctx, cancel: cancel, owner: newOriginalDownloaderCommandUploads(), capability: capability, directory: t.TempDir(), active: &atomic.Bool{}, app: &App{}}
	f.active.Store(true)
	kind, configKind := "qbittorrent", "qb"
	if capability == originalDownloaderUploadTRAdd {
		kind, configKind = "transmission", "transmission"
	}
	enabled := true
	f.input = &originalDownloaderCommandSnapshot{
		configured: DownloaderConfig{Config: download.Config{ID: "owned-material", Kind: configKind, BaseURL: "https://owned.invalid/material"}, Enabled: &enabled, Mappings: []PathMapping{{From: "/owned/from", To: "/owned/to"}}},
		args:       downloadercommands.Arguments{"save_path": "/owned/download", "nested": map[string]any{"items": []any{json.Number("9007199254740993"), "original"}}},
	}
	frozen := f.input.clone()
	f.app.config.Downloaders = []DownloaderConfig{f.input.clone().configured}
	scope := originalBaseCallScope{downloaderUploads: f.owner, validate: func(callCtx context.Context) error {
		if err := callCtx.Err(); err != nil {
			return err
		}
		if !f.active.Load() {
			return bad("PLUGIN_CALL_CLOSED", "owned authority ended")
		}
		return nil
	}}
	current := f.app.originalDownloaderCommandGuard(scope, kind, frozen)
	guard := func(callCtx context.Context) error {
		if err := current(callCtx); err != nil {
			return err
		}
		if hook != nil {
			return hook(callCtx)
		}
		return nil
	}
	if err := os.WriteFile(filepath.Join(f.directory, "keep-owned-sibling"), []byte("untouched"), 0600); err != nil {
		cancel()
		t.Fatal(err)
	}
	spool, err := materialio.New(ctx, f.directory, guard)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var once sync.Once
	var closeErr error
	f.closeOwner = func() error {
		once.Do(func() { closeErr = spool.Close(); f.closes.Add(1) })
		return closeErr
	}
	t.Cleanup(func() { cancel(); _ = f.closeOwner(); f.owner.close() })
	f.handle, err = f.owner.registerMaterial(ctx, &originalDownloaderMaterialUpload{capability: capability, snapshot: f.input, total: total, digest: digest, spool: spool, closeOwner: f.closeOwner})
	if err != nil {
		t.Fatal(err)
	}
	if !originalDownloaderUploadHandle(f.handle) {
		t.Fatal("noncanonical handle")
	}
	return f
}

func (f *ownedMaterialConsumerFixture) appendAll(t *testing.T, raw []byte) {
	t.Helper()
	for offset := 0; offset < len(raw); {
		end := min(offset+plugins.MaterialFrameBytes, len(raw))
		if err := f.owner.appendMaterial(f.ctx, f.capability, f.handle, int64(offset), raw[offset:end]); err != nil {
			t.Fatal("append", offset, err)
		}
		offset = end
	}
}

func (f *ownedMaterialConsumerFixture) assertClean(t *testing.T) {
	t.Helper()
	if f.closes.Load() != 1 {
		t.Fatal("owner did not close exactly once before returning", f.closes.Load())
	}
	entries, err := os.ReadDir(f.directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "keep-owned-sibling" {
		t.Fatal("owned spool leaked or sibling removed", entries, err)
	}
	kept, err := os.ReadFile(filepath.Join(f.directory, "keep-owned-sibling"))
	if err != nil || string(kept) != "untouched" {
		t.Fatal("sibling modified", err)
	}
}

func (f *ownedMaterialConsumerFixture) assertTombstone(t *testing.T) {
	t.Helper()
	f.owner.mu.Lock()
	value := f.owner.materials[f.handle]
	terminal := value != nil && value.consumed && value.snapshot == nil
	f.owner.mu.Unlock()
	if !terminal {
		t.Fatal("no consumed owner tombstone")
	}
	ctx := context.Background() // A fresh context cannot revive a failed/closed claim.
	if snap, raw, err := f.owner.claimMaterialBound(ctx, f.capability, f.handle); err == nil || snap != nil || raw != nil {
		t.Fatal("second submit revived state", err)
	}
	if err := f.owner.abortMaterial(ctx, f.capability, f.handle); err == nil {
		t.Fatal("consumed abort falsely succeeded")
	}
	if err := f.owner.appendMaterial(ctx, f.capability, f.handle, 0, []byte("x")); err == nil {
		t.Fatal("consumed append revived state")
	}
	if snap, err := f.owner.snapshotMaterial(ctx, f.capability, f.handle); err == nil || snap != nil {
		t.Fatal("consumed snapshot escaped", err)
	}
	f.assertClean(t)
}

func TestOriginalDownloaderMaterialOwnerClaimAndGuard(t *testing.T) {
	for _, tc := range []struct {
		name, capability string
		raw              []byte
	}{
		{"complete_qb", originalDownloaderUploadQBAdd, bytes.Repeat([]byte("owned"), plugins.MaterialFrameBytes/5+7)},
		{"empty_tr", originalDownloaderUploadTRAdd, []byte{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOwnedMaterialConsumerFixture(t, tc.capability, int64(len(tc.raw)), sha256.Sum256(tc.raw), nil)
			expected := f.input.clone()
			f.input.configured.BaseURL = "https://must-not-be-used.invalid/changed"
			*f.input.configured.Enabled = false
			f.input.configured.Mappings[0].From = "/changed"
			f.input.args["nested"].(map[string]any)["items"].([]any)[1] = "changed"
			detached, err := f.owner.snapshotMaterial(f.ctx, f.capability, f.handle)
			if err != nil || !reflect.DeepEqual(detached, expected) {
				t.Fatal("registration retained caller aliases", err)
			}
			detached.args["nested"].(map[string]any)["items"].([]any)[0] = "changed again"
			f.appendAll(t, tc.raw)
			snap, raw, err := f.owner.claimMaterialBound(f.ctx, f.capability, f.handle)
			if err != nil || raw == nil || !bytes.Equal(raw, tc.raw) || !reflect.DeepEqual(snap, expected) {
				t.Fatal("verified immutable claim", len(raw), err)
			}
			f.assertTombstone(t)
			f.owner.close()
			if !bytes.Equal(raw, tc.raw) || raw == nil {
				t.Fatal("closing scope cleared already returned bytes")
			}
		})
	}
	for _, tc := range []struct {
		name    string
		written []byte
		digest  [sha256.Size]byte
		want    error
	}{
		{"incomplete", []byte("A"), sha256.Sum256([]byte("AB")), materialio.ErrLength},
		{"wrong_digest", []byte("AX"), sha256.Sum256([]byte("AB")), materialio.ErrDigest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOwnedMaterialConsumerFixture(t, originalDownloaderUploadQBAdd, 2, tc.digest, nil)
			f.appendAll(t, tc.written)
			snap, raw, err := f.owner.claimMaterialBound(f.ctx, f.capability, f.handle)
			if !errors.Is(err, tc.want) || snap != nil || raw != nil {
				t.Fatal("failed claim published downstream input", err)
			}
			f.assertTombstone(t)
		})
	}
	for _, reason := range []string{"endpoint_changed", "authority_revoked"} {
		t.Run(reason, func(t *testing.T) {
			data := []byte("AB")
			f := newOwnedMaterialConsumerFixture(t, originalDownloaderUploadQBAdd, 2, sha256.Sum256(data), nil)
			f.appendAll(t, data)
			if reason == "endpoint_changed" {
				f.app.cfgMu.Lock()
				f.app.config.Downloaders[0].BaseURL = "https://changed.invalid/material"
				f.app.cfgMu.Unlock()
			} else {
				f.active.Store(false)
			}
			snap, raw, err := f.owner.claimMaterialBound(f.ctx, f.capability, f.handle)
			var apiErr *APIError
			// Actual current guard is exercised inside the real spool Claim.
			// This is not a claim that the framework/HTTP route ran here.
			if err == nil || snap != nil || raw != nil {
				t.Fatal("revoked current guard published downstream input", err)
			}
			wantCode := "DOWNLOADER_CHANGED"
			if reason == "authority_revoked" {
				wantCode = "PLUGIN_CALL_CLOSED"
			}
			if !errors.As(err, &apiErr) || apiErr.Code != wantCode {
				t.Fatal("wrong current guard error", err)
			}
			f.assertTombstone(t)
		})
	}
}

// A private test context marker pauses the actual guard while Spool owns its
// physical IO mutex. It grants no framework authority and changes no product.
type ownedMaterialHookKey struct{}
type ownedMaterialBlock struct {
	phase                  string
	entered, release       chan struct{}
	enterOnce, releaseOnce sync.Once
}

func newOwnedMaterialBlock(t *testing.T, phase string) *ownedMaterialBlock {
	t.Helper()
	b := &ownedMaterialBlock{phase: phase, entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(b.unblock)
	return b
}
func (b *ownedMaterialBlock) unblock() { b.releaseOnce.Do(func() { close(b.release) }) }
func (b *ownedMaterialBlock) guard(ctx context.Context) error {
	if ctx.Value(ownedMaterialHookKey{}) != b.phase {
		return nil
	}
	b.enterOnce.Do(func() { close(b.entered) })
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *ownedMaterialBlock) marked(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownedMaterialHookKey{}, b.phase)
}
func waitOwnedMaterialSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-signal:
	case <-timer.C:
		t.Fatal("owned guard/IO did not reach bounded rendezvous")
	}
}
func waitOwnedMaterialError(t *testing.T, result <-chan error) error {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		t.Fatal("owned operation did not join")
		return nil
	}
}

type ownedMaterialClaimResult struct {
	snapshot *originalDownloaderCommandSnapshot
	content  []byte
	err      error
}

func waitOwnedMaterialClaim(t *testing.T, result <-chan ownedMaterialClaimResult) ownedMaterialClaimResult {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case value := <-result:
		return value
	case <-timer.C:
		t.Fatal("owned claim did not join")
		return ownedMaterialClaimResult{}
	}
}
func waitOwnedMaterialConsumed(t *testing.T, f *ownedMaterialConsumerFixture) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		f.owner.mu.Lock()
		consumed := f.owner.materials[f.handle].consumed
		f.owner.mu.Unlock()
		if consumed {
			return
		}
		select {
		case <-tick.C:
		case <-timer.C:
			t.Fatal("submit did not linearize before waiting for IO")
		}
	}
}

func TestOriginalDownloaderMaterialOwnerAppendClaimRace(t *testing.T) {
	t.Run("inflight_last_append_then_claim", func(t *testing.T) {
		block := newOwnedMaterialBlock(t, "append")
		f := newOwnedMaterialConsumerFixture(t, originalDownloaderUploadQBAdd, 2, sha256.Sum256([]byte("AB")), block.guard)
		defer block.unblock()
		f.appendAll(t, []byte("A"))
		appended := make(chan error, 1)
		go func() {
			appended <- f.owner.appendMaterial(block.marked(f.ctx), f.capability, f.handle, 1, []byte("B"))
		}()
		waitOwnedMaterialSignal(t, block.entered)
		claimed := make(chan ownedMaterialClaimResult, 1)
		go func() {
			s, b, e := f.owner.claimMaterialBound(f.ctx, f.capability, f.handle)
			claimed <- ownedMaterialClaimResult{s, b, e}
		}()
		waitOwnedMaterialConsumed(t, f)
		// The already-admitted append holds spool IO; no second append may
		// enter after the owner claim's monotonic tombstone transition.
		if err := f.owner.appendMaterial(f.ctx, f.capability, f.handle, 1, []byte("B")); err == nil {
			t.Fatal("append after submit entered")
		}
		block.unblock()
		if err := waitOwnedMaterialError(t, appended); err != nil {
			t.Fatal("admitted final append", err)
		}
		got := waitOwnedMaterialClaim(t, claimed)
		if got.err != nil || got.snapshot == nil || string(got.content) != "AB" {
			t.Fatal("claim did not wait for complete physical append", got.err)
		}
		f.assertTombstone(t)
	})
	t.Run("incomplete_claim_precedes_last_append", func(t *testing.T) {
		block := newOwnedMaterialBlock(t, "claim")
		f := newOwnedMaterialConsumerFixture(t, originalDownloaderUploadTRAdd, 2, sha256.Sum256([]byte("AB")), block.guard)
		defer block.unblock()
		f.appendAll(t, []byte("A"))
		claimed := make(chan ownedMaterialClaimResult, 1)
		go func() {
			s, b, e := f.owner.claimMaterialBound(block.marked(f.ctx), f.capability, f.handle)
			claimed <- ownedMaterialClaimResult{s, b, e}
		}()
		waitOwnedMaterialSignal(t, block.entered)
		appended := make(chan error, 1)
		go func() { appended <- f.owner.appendMaterial(f.ctx, f.capability, f.handle, 1, []byte("B")) }()
		if err := waitOwnedMaterialError(t, appended); err == nil {
			t.Fatal("last append revived an in-flight consumed claim")
		}
		block.unblock()
		got := waitOwnedMaterialClaim(t, claimed)
		if !errors.Is(got.err, materialio.ErrLength) || got.snapshot != nil || got.content != nil {
			t.Fatal("incomplete race published bytes", got.err)
		}
		f.assertTombstone(t)
	})
	t.Run("cancel_inflight_last_append", func(t *testing.T) {
		block := newOwnedMaterialBlock(t, "append")
		f := newOwnedMaterialConsumerFixture(t, originalDownloaderUploadQBAdd, 2, sha256.Sum256([]byte("AB")), block.guard)
		defer block.unblock()
		f.appendAll(t, []byte("A"))
		appended := make(chan error, 1)
		go func() {
			appended <- f.owner.appendMaterial(block.marked(f.ctx), f.capability, f.handle, 1, []byte("B"))
		}()
		waitOwnedMaterialSignal(t, block.entered)
		f.cancel()
		if err := waitOwnedMaterialError(t, appended); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation did not join actual append", err)
		}
		// Even a fresh caller context cannot resurrect this invocation's spool.
		snap, raw, err := f.owner.claimMaterialBound(context.Background(), f.capability, f.handle)
		if !errors.Is(err, context.Canceled) || snap != nil || raw != nil {
			t.Fatal("cancelled spool published downstream input", err)
		}
		f.assertTombstone(t)
	})
}
