package durable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	source "light-media/internal/plugins/autosignin"
)

func TestExistingRunsRecoverFromFrozenInventoryWithoutCurrentLookup(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(itoa(map[bool]int{false: 0, true: 1}[interrupted]), func(t *testing.T) {
			store, binding, _ := fixture(t)
			target := &fixedHTTP{Response: confirmed}
			c := controller(store, binding, target)
			boot(t, c)
			if interrupted {
				c.Journal = &failEffectStore{Journal: store, Kind: "notify"}
			}
			first, err := c.Execute(context.Background(), "frozen", Command{Action: "service"})
			if interrupted && !errors.Is(err, ErrStore) || !interrupted && err != nil {
				t.Fatal(err)
			}
			currentReads := 0
			c.Dependencies.Inventory = func(context.Context) (source.Inventory, error) {
				currentReads++
				return source.Inventory{}, errors.New("current inventory unavailable")
			}
			again, err := c.Execute(context.Background(), "frozen", Command{Action: "service"})
			if err != nil || currentReads != 0 || target.Calls.Load() != 1 {
				t.Fatal("frozen run depended on current inventory or repeated HTTP", err, currentReads, target.Calls.Load())
			}
			if !interrupted && !bytes.Equal(marshal(first), marshal(again)) {
				t.Fatal("completed result changed")
			}
			if _, err := c.Execute(context.Background(), "frozen", Command{Action: "api"}); !errors.Is(err, ErrConflict) || currentReads != 0 {
				t.Fatal("replay bypassed immutable plan", err)
			}
		})
	}
}

type failFinishOnce struct {
	Journal
	id   string
	done bool
}

func (f *failFinishOnce) Finish(ctx context.Context, b Binding, id string, runtime, result json.RawMessage, phase string) error {
	if id == f.id && !f.done {
		f.done = true
		return ErrStore
	}
	return f.Journal.Finish(ctx, b, id, runtime, result, phase)
}

func TestDomainAPIRecoveryFreezesFoundMissingAndFailedPublicLookup(t *testing.T) {
	for _, scenario := range []string{"found", "missing", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			store, binding, _ := fixture(t)
			target := &fixedHTTP{Response: confirmed}
			c := controller(store, binding, target)
			boot(t, c)
			reads := 0
			c.Dependencies.Lookup = func(context.Context, string) (source.Object, error) {
				reads++
				switch scenario {
				case "found":
					return source.Object{"id": 1, "name": "Synthetic", "cookie": "not-a-public-baseline-field"}, nil
				case "failed":
					return nil, errors.New("synthetic lookup failure")
				default:
					return nil, nil
				}
			}
			c.Journal = &failFinishOnce{Journal: store, id: "api-recovery"}
			command := Command{Action: "api", Domain: "https://synthetic.invalid", APIKeyAccepted: true}
			first, err := c.Execute(context.Background(), "api-recovery", command)
			if err == nil {
				t.Fatal("finish failure was not observed")
			}
			c.Dependencies.Lookup = func(context.Context, string) (source.Object, error) {
				reads++
				if scenario == "found" {
					return nil, errors.New("site removed after original response")
				}
				return source.Object{"id": 1, "name": "changed"}, nil
			}
			again, err := c.Execute(context.Background(), "api-recovery", command)
			if scenario == "failed" {
				if err == nil || err.Error() != "synthetic lookup failure" {
					t.Fatal("known lookup error was replaced", err)
				}
			} else if err != nil || !bytes.Equal(marshal(first), marshal(again)) {
				t.Fatal("committed API outcome changed", first, again, err)
			}
			wantCalls := int32(0)
			if scenario == "found" {
				wantCalls = 1
			}
			if reads != 1 || target.Calls.Load() != wantCalls {
				t.Fatal("API recovery repeated current lookup or external request", reads, target.Calls.Load())
			}
			state, err := store.State(context.Background(), binding)
			if err != nil || bytes.Contains(marshal(state.Runs["api-recovery"].Lanes["main"]), []byte("not-a-public-baseline-field")) {
				t.Fatal("API lookup persisted private fields", err)
			}
		})
	}
}

type failNetworkCommit struct {
	Journal
	after, done bool
}

func (f *failNetworkCommit) CompleteNetwork(ctx context.Context, b Binding, n NetworkIntent, value json.RawMessage, known string) error {
	if !f.done {
		f.done = true
		if f.after {
			if err := f.Journal.CompleteNetwork(ctx, b, n, value, known); err != nil {
				return err
			}
		}
		return ErrStore
	}
	return f.Journal.CompleteNetwork(ctx, b, n, value, known)
}

func TestNetworkCommitFailureCanReconcileWithoutOwnerRestartOrResend(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(itoa(map[bool]int{false: 0, true: 1}[after]), func(t *testing.T) {
			store, binding, _ := fixture(t)
			target := &fixedHTTP{Response: confirmed}
			c := controller(store, binding, target)
			boot(t, c)
			c.Journal = &failNetworkCommit{Journal: store, after: after}
			if _, err := c.Execute(context.Background(), "response-commit", Command{Action: "service"}); !errors.Is(err, ErrUncertain) {
				t.Fatal(err)
			}
			state, err := store.State(context.Background(), binding)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for lane, entries := range state.Runs["response-commit"].Lanes {
				for i, entry := range entries {
					if entry.Kind != "http" {
						continue
					}
					found = true
					if after && entry.Phase != "receipt" || !after && entry.Phase != "uncertain" {
						t.Fatal("commit reply ambiguity rewrote known receipt or left unreconcilable intent", entry.Phase)
					}
					if !after {
						if err := store.Reconcile(context.Background(), binding, "response-commit", lane, i, marshal(response{Status: 200, Text: confirmed, Content: []byte(confirmed)})); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if !found {
				t.Fatal("missing HTTP intent/receipt")
			}
			if _, err := c.Execute(context.Background(), "response-commit", Command{Action: "service"}); err != nil || target.Calls.Load() != 1 {
				t.Fatal("receipt recovery resent HTTP", err, target.Calls.Load())
			}
		})
	}
}

func TestOutboxCommitFailureCanReconcileWithoutOwnerRestartOrResend(t *testing.T) {
	for _, stage := range []string{"before_write", "after_commit"} {
		t.Run(stage, func(t *testing.T) {
			store, binding, _ := fixture(t)
			ctx := context.Background()
			if _, err := store.Start(ctx, binding, "outbox-recovery", "test", marshal(nil), Baseline{}); err != nil {
				t.Fatal(err)
			}
			lane := (&Execution{Store: store, Binding: binding, RunID: "outbox-recovery", DigestKey: fixtureKey}).Lane("main", "")
			if err := lane.Effect(ctx, "notify", "", source.Object{"title": "synthetic"}, Mutation{Kind: "outbox"}); err != nil {
				t.Fatal(err)
			}
			var id string
			deliveries := 0
			ok, err := store.DispatchOne(ctx, binding, func(_ context.Context, item OutboxItem) (Acknowledgement, error) {
				deliveries++
				id = item.ID
				fail := true
				store.Fault = func(actual string) error {
					if actual == stage && fail {
						fail = false
						return ErrStore
					}
					return nil
				}
				return Acknowledgement{ID: id, Committed: true}, nil
			})
			if !ok || !errors.Is(err, ErrUncertain) {
				t.Fatal(ok, err)
			}
			store.Fault = nil
			state, err := store.State(ctx, binding)
			want := "uncertain"
			if stage == "after_commit" {
				want = "delivered"
			}
			if err != nil || state.Outbox[0].Phase != want || deliveries != 1 {
				t.Fatal("outbox recovery lost or repeated committed sink", state.Outbox, err, deliveries)
			}
			if want == "uncertain" {
				if err := store.ReconcileOutbox(ctx, binding, id, Acknowledgement{ID: id, Committed: true}); err != nil {
					t.Fatal(err)
				}
			}
			if ok, err = store.DispatchOne(ctx, binding, func(context.Context, OutboxItem) (Acknowledgement, error) {
				deliveries++
				return Acknowledgement{}, nil
			}); ok || err != nil || deliveries != 1 {
				t.Fatal("known sink was delivered again", ok, err, deliveries)
			}
		})
	}
}

func TestOwnerRestorePreservesRunBaselineAndKnownReceipts(t *testing.T) {
	scope := Scope{Instance: "AutoSignIn", Generation: 1, Revision: 1, Implementation: "fixed"}
	state, err := NewState(scope, marshal(source.Object{}))
	if err != nil {
		t.Fatal(err)
	}
	state.Epoch = "old-owner"
	state.ActiveRun = "run"
	state.Runs["run"] = &Run{ID: "run", Epoch: "old-owner", Phase: "running", Scope: scope, Baseline: Baseline{Timezone: "Asia/Shanghai"}, Lanes: map[string][]Entry{"site": {{Kind: "http", Guard: "guard", Digest: "request", Phase: "intent"}, {Kind: "http", Guard: "known", Digest: "known", Phase: "receipt", Value: marshal("response")}}}}
	state.Outbox = []OutboxItem{{ID: "sink", Phase: "intent"}, {ID: "committed", Phase: "delivered"}}
	if err := RestoreOwnerState(&state, Binding{Scope: scope, Epoch: "new-owner"}); err != nil {
		t.Fatal(err)
	}
	run := state.Runs["run"]
	if state.Epoch != "new-owner" || run.Epoch != "old-owner" || run.Baseline.Timezone != "Asia/Shanghai" || state.ActiveRun != "run" || run.Lanes["site"][0].Phase != "uncertain" || state.Blocks["guard"] != "run" || run.Lanes["site"][1].Phase != "receipt" || state.Outbox[0].Phase != "uncertain" || state.Outbox[1].Phase != "delivered" {
		t.Fatal("restore changed baseline, erased uncertainty or renewed an external effect")
	}
	state.Runs["corrupt"] = nil
	before := marshal(state)
	if err := RestoreOwnerState(&state, Binding{Scope: scope, Epoch: "invalid-claim"}); !errors.Is(err, ErrStore) || !bytes.Equal(before, marshal(state)) {
		t.Fatal("corrupt state partially restored", err)
	}
}
