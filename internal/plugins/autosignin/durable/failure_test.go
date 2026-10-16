package durable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	source "light-media/internal/plugins/autosignin"
)

func TestDurableCrashProcess(t *testing.T) {
	index := slices.Index(os.Args, "durable-crash-fixture")
	if index < 0 {
		return
	}
	dir, origin := os.Args[index+1], os.Args[index+2]
	store, err := OpenFileStore(dir, "AutoSignIn", fixtureKey)
	if err != nil {
		os.Exit(7)
	}
	binding := Binding{Scope: Scope{Instance: "AutoSignIn", Generation: 1, Revision: 1, Implementation: "fixed-core-and-adapter-sha"}, Epoch: "crashed-child"}
	lease, err := store.Claim(context.Background(), binding)
	if err != nil {
		os.Exit(8)
	}
	defer lease.Close()
	// The immutable baseline exists before the physical HTTP request. The child
	// exits after the owned server commits and replies but before receipt writing.
	base := Baseline{Runtime: json.RawMessage(`{}`), History: json.RawMessage(`{}`), Inventory: json.RawMessage(`{}`)}
	if _, err = store.Start(context.Background(), binding, "physical-crash", "physical", json.RawMessage(`{}`), base); err != nil {
		os.Exit(9)
	}
	lane := (&Execution{Store: store, Binding: binding, RunID: "physical-crash", DigestKey: fixtureKey}).Lane("site", "fixed-site-operation")
	transport, _ := source.BindSiteHTTP(source.SiteHTTPBinding{URL: origin, Cookie: "sid=synthetic-child-only"})
	crash := crashAfterHTTP{target: transport}
	_, _ = (HTTP{Lane: lane, Target: crash}).Do(context.Background(), source.PageRequest{Method: "POST", URL: origin + "/commit", Form: map[string]string{"action": "synthetic-only"}})
	os.Exit(10)
}

type crashAfterHTTP struct{ target source.PageTransport }

func (c crashAfterHTTP) Do(ctx context.Context, r source.PageRequest) (*source.PageResponse, error) {
	_, err := c.target.Do(ctx, r)
	if err != nil {
		os.Exit(11)
	}
	os.Exit(3)
	return nil, nil
}
func TestRealProcessCrashAfterOwnedHTTPCommitLeavesUncertainAndNoResend(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenFileStore(dir, "AutoSignIn", fixtureKey)
	if err != nil {
		t.Fatal(err)
	}
	runtime, _ := NewRuntime("UTC")
	scope := Scope{Instance: "AutoSignIn", Generation: 1, Revision: 1, Implementation: "fixed-core-and-adapter-sha"}
	if err = store.Provision(context.Background(), scope, runtime); err != nil {
		t.Fatal(err)
	}
	var commits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Cookie") != "sid=synthetic-child-only" {
			t.Error("owned synthetic commit differs")
		}
		commits.Add(1)
		io.WriteString(w, "committed")
	}))
	defer server.Close()
	path, _ := os.Executable()
	cmd := exec.Command(path, "-test.run=^TestDurableCrashProcess$", "--", "durable-crash-fixture", dir, server.URL)
	cmd.Env = []string{"GOMEMLIMIT=64MiB"}
	err = cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatal("fixture did not crash after response", err)
	}
	binding := Binding{Scope: scope, Epoch: "recovered-parent"}
	lease, err := store.Claim(context.Background(), binding)
	if err != nil {
		t.Fatal("OS crash did not release lease", err)
	}
	defer lease.Close()
	state, err := store.State(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if state.Runs["physical-crash"].Lanes["site"][0].Phase != "uncertain" || state.Blocks["fixed-site-operation"] != "physical-crash" {
		t.Fatal("ambiguous request was erased")
	}
	lane := (&Execution{Store: store, Binding: binding, RunID: "physical-crash", DigestKey: fixtureKey}).Lane("site", "fixed-site-operation")
	transport, _ := source.BindSiteHTTP(source.SiteHTTPBinding{URL: server.URL, Cookie: "sid=synthetic-child-only"})
	if _, err = (HTTP{lane, transport}).Do(context.Background(), source.PageRequest{Method: "POST", URL: server.URL + "/commit", Form: map[string]string{"action": "synthetic-only"}}); !errors.Is(err, ErrUncertain) {
		t.Fatal("crash was automatically retried", err)
	}
	if commits.Load() != 1 {
		t.Fatal("duplicate owned commit", commits.Load())
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".owned-state-*"))
	if len(files) != 0 {
		t.Fatal("atomic store left temporary plaintext", files)
	}
}
func TestConfigFailureStopsInitBeforeModuleLoadAndOnlyOnceClear(t *testing.T) {
	store, binding, _ := fixture(t)
	wrapped := &failEffectStore{Journal: store, Kind: "config"}
	c := controller(wrapped, binding, nil)
	if _, err := c.Execute(context.Background(), "cold", Command{Action: "cold_init"}); err != nil {
		t.Fatal(err)
	}
	_, err := c.Execute(context.Background(), "only-once", Command{Action: "init", Config: source.Object{"enabled": false, "onlyonce": true, "sign_sites": []any{1}}})
	if !errors.Is(err, ErrStore) {
		t.Fatal(err)
	}
	state, _ := store.State(context.Background(), binding)
	if len(state.Runs["only-once"].Lanes["main"]) != 0 || len(state.Jobs) != 0 || len(state.Config) != 0 && !bytes.Equal(state.Config, []byte("null")) {
		t.Fatal("later init effects ran after first config failure", state)
	}
	if _, err = c.Execute(context.Background(), "only-once", Command{Action: "init", Config: source.Object{"enabled": false, "onlyonce": true, "sign_sites": []any{1}}}); err != nil {
		t.Fatal(err)
	}
	state, _ = store.State(context.Background(), binding)
	var config source.Object
	source.Decode(state.Config, &config)
	if source.Truth(config["onlyonce"]) || len(state.Jobs) != 1 {
		t.Fatal("once clear/job not committed", state)
	}
}
func TestCommitReplyLossReplaysKnownEffectWithoutAnotherWriteOrDelivery(t *testing.T) {
	store, binding, _ := fixture(t)
	base := Baseline{}
	if _, err := store.Start(context.Background(), binding, "run", "test", json.RawMessage(`{}`), base); err != nil {
		t.Fatal(err)
	}
	fail := true
	store.Fault = func(stage string) error {
		if stage == "after_commit" && fail {
			fail = false
			return ErrStore
		}
		return nil
	}
	execution := &Execution{Store: store, Binding: binding, RunID: "run", DigestKey: fixtureKey}
	lane := execution.Lane("main", "")
	if err := lane.Effect(context.Background(), "notify", "", source.Object{"title": "synthetic-only"}, Mutation{Kind: "outbox"}); !errors.Is(err, ErrStore) {
		t.Fatal(err)
	}
	// A write-disabled store can still acknowledge an already committed entry.
	store.Fault = func(stage string) error {
		if stage == "before_write" {
			return ErrStore
		}
		return nil
	}
	lane = (&Execution{Store: store, Binding: binding, RunID: "run", DigestKey: fixtureKey}).Lane("main", "")
	if err := lane.Effect(context.Background(), "notify", "", source.Object{"title": "synthetic-only"}, Mutation{Kind: "outbox"}); err != nil {
		t.Fatal("committed replay attempted write", err)
	}
	store.Fault = nil
	state, _ := store.State(context.Background(), binding)
	if len(state.Outbox) != 1 {
		t.Fatal("reply loss duplicated outbox")
	}
	deliveries := 0
	if ok, err := store.DispatchOne(context.Background(), binding, func(_ context.Context, item OutboxItem) (Acknowledgement, error) {
		deliveries++
		return Acknowledgement{ID: item.ID, Committed: true}, nil
	}); !ok || err != nil {
		t.Fatal(err)
	}
	if ok, err := store.DispatchOne(context.Background(), binding, func(context.Context, OutboxItem) (Acknowledgement, error) {
		deliveries++
		return Acknowledgement{}, nil
	}); ok || err != nil || deliveries != 1 {
		t.Fatal("delivered outbox repeated", ok, err, deliveries)
	}
}

func TestConcurrentSameRunRejectedWhileNetworkAndReadViewsRemainAvailable(t *testing.T) {
	store, binding, _ := fixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	target := &fixedHTTP{Response: confirmed, After: func() { close(entered); <-release }}
	c := controller(store, binding, target)
	boot(t, c)
	done := make(chan error, 1)
	go func() { _, err := c.Execute(context.Background(), "same", Command{Action: "service"}); done <- err }()
	<-entered
	if _, err := c.Execute(context.Background(), "same", Command{Action: "service"}); !errors.Is(err, ErrBusy) {
		t.Fatal("same run entered twice", err)
	}
	// No whole-file transaction is held during network waiting.
	if _, err := store.State(context.Background(), binding); err != nil {
		t.Fatal("read blocked or failed during network", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if target.Calls.Load() != 1 {
		t.Fatal("duplicate network request")
	}
}
func TestCredentialBindingChangedDuringRecoveryCannotReuseWrongResponse(t *testing.T) {
	store, binding, _ := fixture(t)
	target := &fixedHTTP{Response: confirmed}
	c := controller(store, binding, target)
	boot(t, c)
	c.Journal = &failEffectStore{Journal: store, Kind: "notify"}
	if _, err := c.Execute(context.Background(), "recovery", Command{Action: "service"}); !errors.Is(err, ErrStore) {
		t.Fatal(err)
	}
	old := c.Dependencies.Factory
	c.Dependencies.Factory = func(ctx context.Context, ref source.Object) (BoundSite, error) {
		bound, err := old(ctx, ref)
		bound.Site["cookie"] = "synthetic-changed-cookie"
		bound.Revision = "site-grant-v2"
		return bound, err
	}
	if _, err := c.Execute(context.Background(), "recovery", Command{Action: "service"}); !errors.Is(err, ErrConflict) {
		t.Fatal("credential snapshot changed silently", err)
	}
	if target.Calls.Load() != 1 {
		t.Fatal("changed binding sent network")
	}
}
func TestRuntimeEpochOrdinaryInitAndColdRestartHaveDifferentWindowRules(t *testing.T) {
	store, binding, lease := fixture(t)
	c := controller(store, binding, nil)
	boot(t, c)
	if _, err := c.Execute(context.Background(), "save", Command{Action: "init", Config: source.Object{"enabled": true, "cron": "2.3", "sign_sites": []any{1}}}); err != nil {
		t.Fatal(err)
	}
	state, _ := store.State(context.Background(), binding)
	var runtime source.Object
	source.Decode(state.Runtime, &runtime)
	if string(marshal(runtime["start_time"])) != "9" {
		t.Fatal("ordinary save reset source window", runtime)
	}
	lease.Close()
	binding.Epoch = "new-process"
	newLease, err := store.Claim(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	defer newLease.Close()
	c.Binding = binding
	if _, err = c.Execute(context.Background(), "unsafe-start", Command{Action: "service"}); !errors.Is(err, ErrStale) {
		t.Fatal("new manager reused old instance without cold_init", err)
	}
	if _, err = c.Execute(context.Background(), "restart", Command{Action: "cold_init"}); err != nil {
		t.Fatal(err)
	}
	state, _ = store.State(context.Background(), binding)
	source.Decode(state.Runtime, &runtime)
	if runtime["start_time"] != nil || runtime["end_time"] != nil {
		t.Fatal("cold restart retained old volatile window", runtime)
	}
}
func TestOutboxLostReplyBlocksRedeliveryAndPreservesSourceQueueOrder(t *testing.T) {
	store, binding, _ := fixture(t)
	store.Start(context.Background(), binding, "outbox", "test", json.RawMessage(`{}`), Baseline{})
	lane := (&Execution{Store: store, Binding: binding, RunID: "outbox", DigestKey: fixtureKey}).Lane("main", "")
	for _, title := range []string{"one", "two"} {
		if err := lane.Effect(context.Background(), "notify", "", source.Object{"title": title}, Mutation{Kind: "outbox"}); err != nil {
			t.Fatal(err)
		}
	}
	sent := 0
	var firstID string
	if _, err := store.DispatchOne(context.Background(), binding, func(_ context.Context, item OutboxItem) (Acknowledgement, error) {
		sent++
		firstID = item.ID
		return Acknowledgement{}, errors.New("synthetic lost reply after commit")
	}); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	if _, err := store.DispatchOne(context.Background(), binding, func(context.Context, OutboxItem) (Acknowledgement, error) { sent++; return Acknowledgement{}, nil }); !errors.Is(err, ErrUncertain) || sent != 1 {
		t.Fatal("uncertain notification or later event sent", err, sent)
	}
	if err := store.ReconcileOutbox(context.Background(), binding, firstID, Acknowledgement{ID: firstID, Committed: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DispatchOne(context.Background(), binding, func(_ context.Context, item OutboxItem) (Acknowledgement, error) {
		sent++
		if string(item.Payload) != `{"title":"two"}` {
			t.Error("source outbox order changed")
		}
		return Acknowledgement{ID: item.ID, Committed: true}, nil
	}); err != nil || sent != 2 {
		t.Fatal(err, sent)
	}
}

type knownSchedulerFailureStore struct{ Journal }

func (s knownSchedulerFailureStore) Apply(ctx context.Context, b Binding, e Effect) (Entry, error) {
	if e.Kind == "scheduler_remove" {
		e.ErrorClass = "RuntimeError"
		e.KnownError = "synthetic known scheduler failure"
	}
	return s.Journal.Apply(ctx, b, e)
}
func TestKnownSourceSchedulerFailureIsPersistedUnappliedAndSwallowedLikeOriginal(t *testing.T) {
	store, binding, _ := fixture(t)
	c := controller(store, binding, nil)
	if _, err := c.Execute(context.Background(), "cold", Command{Action: "cold_init"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Execute(context.Background(), "once", Command{Action: "init", Config: source.Object{"enabled": false, "onlyonce": true}}); err != nil {
		t.Fatal(err)
	}
	c.Journal = knownSchedulerFailureStore{store}
	if _, err := c.Execute(context.Background(), "save-disabled", Command{Action: "init", Config: source.Object{"enabled": false}}); err != nil {
		t.Fatal("source ordinary stop exception escaped", err)
	}
	state, _ := store.State(context.Background(), binding)
	entry := state.Runs["save-disabled"].Lanes["main"][0]
	if entry.Phase != "known_error" || entry.ErrorClass != "RuntimeError" || len(state.Jobs) != 1 {
		t.Fatal("failed removal was falsely applied", entry, state.Jobs)
	}
	var runtime source.Object
	source.Decode(state.Runtime, &runtime)
	if runtime["scheduler"] == nil {
		t.Fatal("source retained scheduler pointer was discarded")
	}
}

func TestLeaseCannotCloseDuringExecutionAndClosedControllerCannotRevive(t *testing.T) {
	store, binding, lease := fixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	target := &fixedHTTP{Response: confirmed, After: func() { close(entered); <-release }}
	c := controller(store, binding, target)
	boot(t, c)
	done := make(chan error, 1)
	go func() {
		_, err := c.Execute(context.Background(), "lease-work", Command{Action: "service"})
		done <- err
	}()
	<-entered
	if err := lease.Close(); !errors.Is(err, ErrBusy) {
		t.Fatal("lifecycle ownership released around active network", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Execute(context.Background(), "revived", Command{Action: "service"}); !errors.Is(err, ErrStale) {
		t.Fatal("closed owner revived controller", err)
	}
}

func TestColdInitReplayFreezesOriginalConfigAndTimezoneBeforeOnlyOnceClears(t *testing.T) {
	store, binding, _ := fixture(t)
	ctx := context.Background()
	initial := source.Object{"enabled": true, "onlyonce": true, "sign_sites": []any{1}, "cron": "0 9 * * *"}
	if err := store.update(ctx, func(s *State) error { s.Config = marshal(initial); return nil }); err != nil {
		t.Fatal(err)
	}
	c := controller(&failEffectStore{Journal: store, Kind: "service.definitions"}, binding, nil)
	c.Dependencies.Timezone = "Asia/Shanghai"
	command := Command{Action: "cold_init"}
	if _, err := c.Execute(ctx, "cold-once", command); !errors.Is(err, ErrStore) {
		t.Fatal("did not interrupt after once-clearing config commit", err)
	}
	state, err := store.State(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	var committed, baseline source.Object
	source.Decode(state.Config, &committed)
	source.Decode(state.Runs["cold-once"].Baseline.Config, &baseline)
	if source.Truth(committed["onlyonce"]) || !source.Truth(baseline["onlyonce"]) || len(state.Jobs) != 1 {
		t.Fatal("fixture missed config-cleared boundary", committed, baseline, state.Jobs)
	}
	// Neither the committed false flag nor an administrative timezone change
	// may replace this interrupted run's immutable initial inputs.
	c.Dependencies.Timezone = "America/New_York"
	if _, err := c.Execute(ctx, "cold-once", command); err != nil {
		t.Fatal("cold-init replay used advanced head instead of frozen inputs", err)
	}
	state, err = store.State(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Jobs) != 1 || state.ActiveRun != "" || !bytes.Contains(state.Services, []byte(`"timezone":"Asia/Shanghai"`)) {
		t.Fatal("recovery duplicated once job or changed trigger timezone", state.Jobs, string(state.Services))
	}
}

func TestRunningOnceSchedulerStopBoundaryCanInterruptAndResume(t *testing.T) {
	store, binding, _ := fixture(t)
	c := controller(store, binding, nil)
	ctx := context.Background()
	if _, err := c.Execute(ctx, "cold", Command{Action: "cold_init"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Execute(ctx, "once", Command{Action: "init", Config: source.Object{"enabled": false, "onlyonce": true}}); err != nil {
		t.Fatal(err)
	}
	c.Journal = &failEffectStore{Journal: store, Kind: "scheduler_stop"}
	command := Command{Action: "init", Config: source.Object{"enabled": false}}
	if _, err := c.Execute(ctx, "stop-running", command); !errors.Is(err, ErrStore) {
		t.Fatal("scheduler shutdown was omitted or not stoppable", err)
	}
	state, err := store.State(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	entries := state.Runs["stop-running"].Lanes["main"]
	if len(entries) != 1 || entries[0].Kind != "scheduler_remove" || len(state.Jobs) != 0 {
		t.Fatal("shutdown failure lost successful remove prefix", entries, state.Jobs)
	}
	if _, err = c.Execute(ctx, "stop-running", command); err != nil {
		t.Fatal("running once scheduler could not finish shutdown", err)
	}
	state, _ = store.State(ctx, binding)
	var runtime source.Object
	source.Decode(state.Runtime, &runtime)
	if runtime["scheduler"] != nil || state.ActiveRun != "" || len(state.Jobs) != 0 {
		t.Fatal("source successful stop did not clear scheduler", runtime, state.Jobs)
	}
	entries = state.Runs["stop-running"].Lanes["main"]
	if entries[0].Kind != "scheduler_remove" || entries[1].Kind != "scheduler_stop" || entries[2].Kind != "config" {
		t.Fatal("source stop/save effect order changed", entries)
	}
}
