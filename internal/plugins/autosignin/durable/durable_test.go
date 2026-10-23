package durable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	source "light-media/internal/plugins/autosignin"
)

var fixtureKey = [32]byte{1, 2, 3, 4, 5, 6, 7, 8, 9}

func fixture(t *testing.T) (*FileStore, Binding, *Lease) {
	t.Helper()
	store, err := OpenFileStore(t.TempDir(), "AutoSignIn", fixtureKey)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime("UTC")
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{Scope: Scope{Instance: "AutoSignIn", Generation: 1, Revision: 1, Implementation: "fixed-core-and-adapter-sha"}, Epoch: "owner-one"}
	if err = store.Provision(context.Background(), binding.Scope, runtime); err != nil {
		t.Fatal(err)
	}
	lease, err := store.Claim(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lease.Close() })
	return store, binding, lease
}
func inventory() source.Inventory {
	row := source.Object{"id": 1, "name": "Synthetic"}
	return source.Inventory{Configured: []source.Object{row}, Indexers: []source.Object{row}}
}
func controller(store Journal, binding Binding, target source.PageTransport) *Controller {
	return &Controller{Journal: store, Binding: binding, DigestKey: fixtureKey, Dependencies: Dependencies{Source: &syntheticSourcePort{}, Inventory: func(context.Context) (source.Inventory, error) { return inventory(), nil }, Factory: func(context.Context, source.Object) (BoundSite, error) {
		return BoundSite{Site: source.Object{"id": 1, "name": "Synthetic", "url": "https://synthetic.invalid/", "cookie": "synthetic-secret-cookie"}, Hooks: source.SiteVisitHooks{HTTP: target}, Revision: "synthetic-site-grant-v1"}, nil
	}, Now: func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }, RandomInt: func(a, b int) (int, error) { return a, nil }}}
}
func boot(t *testing.T, c *Controller) {
	t.Helper()
	ctx := context.Background()
	if _, err := c.Execute(ctx, "cold", Command{Action: "cold_init"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Execute(ctx, "init", Command{Action: "init", Config: source.Object{"enabled": true, "notify": true, "sign_sites": []any{1}, "login_sites": []any{}, "retry_keyword": "Cookie已失效", "cron": "2.3/9-23"}}); err != nil {
		t.Fatal(err)
	}
}

type fixedHTTP struct {
	Calls    atomic.Int32
	Response string
	After    func()
}

func (f *fixedHTTP) Do(context.Context, source.PageRequest) (*source.PageResponse, error) {
	f.Calls.Add(1)
	if f.After != nil {
		f.After()
	}
	return &source.PageResponse{Status: 200, Text: f.Response, Content: []byte(f.Response)}, nil
}

const confirmed = `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`

func TestDurableCorePerEffectOrderAndCompletedReplay(t *testing.T) {
	store, binding, _ := fixture(t)
	http := &fixedHTTP{Response: confirmed}
	c := controller(store, binding, http)
	boot(t, c)
	first, err := c.Execute(context.Background(), "service-one", Command{Action: "service"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.State(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	entries := state.Runs["service-one"].Lanes["main"]
	kinds := []string{}
	for _, entry := range entries {
		kinds = append(kinds, entry.Kind)
	}
	if string(marshal(kinds)) != `["history.put","history.put","source.notify","config"]` {
		t.Fatal("source commit order changed", kinds)
	}
	if http.Calls.Load() != 1 || state.Stats["synthetic.invalid"].Success != 1 || len(state.Outbox) != 0 || state.ActiveRun != "" {
		t.Fatal("missing real effects", http.Calls.Load(), state)
	}
	second, err := c.Execute(context.Background(), "service-one", Command{Action: "service"})
	if err != nil {
		t.Fatal(err)
	}
	if http.Calls.Load() != 1 || !bytes.Equal(marshal(first["config"]), marshal(second["config"])) {
		t.Fatal("completed run sent or changed state")
	}
	sealed, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"synthetic-secret-cookie", "Synthetic", "logout.php", "确认签到"} {
		if bytes.Contains(sealed, []byte(needle)) {
			t.Fatal("plaintext durable payload", needle)
		}
	}
	if _, err = store.Claim(context.Background(), Binding{Scope: binding.Scope, Epoch: "stolen"}); !errors.Is(err, ErrBusy) {
		t.Fatal("another live process stole instance lease", err)
	}
}

// A failure after monthly history commits must interrupt the original method
// immediately, then replay receipts from baseline without appending twice.
type failEffectStore struct {
	Journal
	Kind string
	Once bool
}

func (f *failEffectStore) Apply(ctx context.Context, b Binding, e Effect) (Entry, error) {
	if e.Kind == f.Kind && !f.Once {
		f.Once = true
		return Entry{}, ErrStore
	}
	return f.Journal.Apply(ctx, b, e)
}
func (f *failEffectStore) BeginSource(ctx context.Context, b Binding, r SourceRequest) (Entry, bool, error) {
	if r.Identity.Kind == "source."+f.Kind && !f.Once {
		f.Once = true
		return Entry{}, false, ErrStore
	}
	return f.Journal.BeginSource(ctx, b, r)
}
func TestMonthlyHistoryBeforeDailyFailureResumesWithoutNetworkOrDuplicateRows(t *testing.T) {
	store, binding, _ := fixture(t)
	http := &fixedHTTP{Response: confirmed}
	c := controller(store, binding, http)
	boot(t, c)
	failed := &failEffectStore{Journal: store, Kind: "notify"}
	c.Journal = failed
	if _, err := c.Execute(context.Background(), "service-crash", Command{Action: "service"}); !errors.Is(err, ErrStore) {
		t.Fatal(err)
	}
	state, _ := store.State(context.Background(), binding)
	if len(state.History) != 2 || len(state.Outbox) != 0 || state.ActiveRun != "service-crash" {
		t.Fatal("later effects leaked through failed boundary", state)
	}
	if _, err := c.Execute(context.Background(), "other", Command{Action: "service"}); !errors.Is(err, ErrBusy) {
		t.Fatal("unresolved run was bypassed", err)
	}
	if _, err := c.Execute(context.Background(), "service-crash", Command{Action: "service"}); err != nil {
		t.Fatal(err)
	}
	state, _ = store.State(context.Background(), binding)
	var rows []any
	json.Unmarshal(state.History["1月2日"], &rows)
	if http.Calls.Load() != 1 || len(rows) != 1 || state.Stats["synthetic.invalid"].Success != 1 || len(state.Outbox) != 0 {
		t.Fatal("resume duplicated prior effects", http.Calls.Load(), len(rows), state)
	}
}
func TestUnknownHTTPBlocksFallbackAndNewRunsUntilTrustedReconciliation(t *testing.T) {
	store, binding, lease := fixture(t)
	target := &dropHTTP{}
	c := controller(store, binding, target)
	boot(t, c)
	if _, err := c.Execute(context.Background(), "dropped", Command{Action: "service"}); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	state, _ := store.State(context.Background(), binding)
	if target.Calls.Load() != 1 || len(state.History) != 0 || len(state.Stats) != 0 || len(state.Blocks) != 1 {
		t.Fatal("unknown outcome caused fallback/stat/history", target.Calls.Load(), state)
	}
	lease.Close()
	binding.Epoch = "owner-two"
	newLease, err := store.Claim(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	defer newLease.Close()
	c.Binding = binding
	if _, err = c.Execute(context.Background(), "dropped", Command{Action: "service"}); !errors.Is(err, ErrUncertain) {
		t.Fatal("restart blindly repeated", err)
	}
	if target.Calls.Load() != 1 {
		t.Fatal("second external submission")
	}
	// A host-verified observed response is the only way to finish this pending
	// operation. This is a trusted API, never a worker-reported success boolean.
	state, _ = store.State(context.Background(), binding)
	entries := state.Runs["dropped"].Lanes["签到:0"]
	index := -1
	for i, e := range entries {
		if e.Phase == "uncertain" {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("missing uncertain receipt")
	}
	proof := marshal(response{Status: 200, Text: confirmed, Content: []byte(confirmed)})
	if err = store.Reconcile(context.Background(), binding, "dropped", "签到:0", index, proof); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Execute(context.Background(), "dropped", Command{Action: "service"}); err != nil {
		t.Fatal(err)
	}
	if target.Calls.Load() != 1 {
		t.Fatal("proof reconciliation resent request")
	}
}

type dropHTTP struct{ Calls atomic.Int32 }

func (f *dropHTTP) Do(context.Context, source.PageRequest) (*source.PageResponse, error) {
	f.Calls.Add(1)
	return nil, nil
}
