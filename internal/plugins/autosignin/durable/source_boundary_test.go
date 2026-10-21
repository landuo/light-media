package durable

import (
	"bytes"
	"context"
	"errors"
	source "light-media/internal/plugins/autosignin"
	"testing"
)

func sourceLaneFixture(t *testing.T) (*FileStore, Binding, *Lease, func(Journal) *Lane) {
	t.Helper()
	store, b, lease := fixture(t)
	if _, err := store.Start(context.Background(), b, "source-run", "test", marshal(nil), Baseline{}); err != nil {
		t.Fatal(err)
	}
	return store, b, lease, func(j Journal) *Lane {
		return (&Execution{Store: j, Binding: b, Epoch: b.Epoch, RunID: "source-run", DigestKey: fixtureKey}).Lane("main", "")
	}
}
func TestSourceBoundaryRequiresIndependentProofAndNeverReinvokes(t *testing.T) {
	for _, mode := range []string{"missing", "unknown", "mismatch", "panic", "read_error", "bare_nil", "bare_error"} {
		t.Run(mode, func(t *testing.T) {
			store, b, _, lane := sourceLaneFixture(t)
			p := &syntheticSourcePort{invoke: func(r SourceRequest) (SourceResult, error) {
				switch mode {
				case "panic":
					panic("synthetic source callback panic")
				case "unknown":
					return SourceResult{Identity: r.Identity, Outcome: SourceUnknown, ProofID: "unknown"}, nil
				case "mismatch":
					i := r.Identity
					i.Index++
					return SourceResult{Identity: i, Outcome: SourceNormal, ProofID: "wrong"}, nil
				case "bare_error":
					return SourceResult{}, errors.New("callback failed")
				}
				return SourceResult{}, nil
			}}
			if mode == "read_error" {
				p.read = func(SourceIdentity) (SourceResult, bool, error) { return SourceResult{}, false, ErrStore }
			}
			for n := 0; n < 2; n++ {
				if err := lane(store).Source(context.Background(), "source.notify", source.Object{"title": "test"}, p); !errors.Is(err, ErrUncertain) {
					t.Fatal(mode, err)
				}
			}
			state, err := store.State(context.Background(), b)
			if err != nil || p.calls != 1 || p.binds != 1 || len(state.Blocks) != 1 || state.Runs["source-run"].Lanes["main"][0].Phase != "uncertain" {
				t.Fatal("unknown repeated or disappeared", p.calls, p.binds, state, err)
			}
			if err := store.Finish(context.Background(), b, "source-run", marshal(nil), marshal(nil), "completed"); !errors.Is(err, ErrUncertain) {
				t.Fatal("unknown completed", err)
			}
		})
	}
}
func TestSourceBoundaryReplayRequiresSameIndependentProof(t *testing.T) {
	store, _, _, lane := sourceLaneFixture(t)
	p := &syntheticSourcePort{}
	if err := lane(store).Source(context.Background(), "source.emit", source.Object{"event": "EventType.PluginAction"}, p); err != nil {
		t.Fatal(err)
	}
	if err := lane(store).Source(context.Background(), "source.emit", source.Object{"event": "EventType.PluginAction"}, p); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 || p.binds != 1 {
		t.Fatal("known replay called source", p.calls, p.binds)
	}
	p.read = func(i SourceIdentity) (SourceResult, bool, error) {
		return SourceResult{Identity: i, Outcome: SourceNormal, ProofID: "replacement-not-original"}, true, nil
	}
	if err := lane(store).Source(context.Background(), "source.emit", source.Object{"event": "EventType.PluginAction"}, p); !errors.Is(err, ErrUncertain) {
		t.Fatal("journal alone/replaced proof accepted", err)
	}
	if p.calls != 1 {
		t.Fatal("proof mismatch resent")
	}
}
func TestSourceBoundaryInvokeErrorWithProvenNormalReturn(t *testing.T) {
	store, _, _, lane := sourceLaneFixture(t)
	p := &syntheticSourcePort{invoke: func(r SourceRequest) (SourceResult, error) {
		return SourceResult{Identity: r.Identity, Outcome: SourceNormal, ProofID: "committed-before-lost-ack"}, ErrStore
	}}
	if err := lane(store).Source(context.Background(), "source.notify", source.Object{}, p); err != nil {
		t.Fatal("Invoke transport error overrode independent result", err)
	}
}
func TestSourceBoundaryRejectsLegacyAcceptedOutboxWithoutCallingHost(t *testing.T) {
	store, b, _, lane := sourceLaneFixture(t)
	if _, err := store.Apply(context.Background(), b, Effect{RunID: "source-run", Lane: "main", Kind: "notify", Digest: "old", Value: marshal(source.Object{"title": "old"}), Mutation: Mutation{Kind: "outbox"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := store.State(context.Background(), b)
	p := &syntheticSourcePort{}
	if err := lane(store).Source(context.Background(), "source.notify", source.Object{"title": "old"}, p); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	after, _ := store.State(context.Background(), b)
	if p.calls != 0 || p.binds != 0 || !bytes.Equal(marshal(before), marshal(after)) {
		t.Fatal("legacy outbox upgraded/replayed")
	}
}
func TestSourceBoundaryNetworkAndLocalWritersCannotForgeProof(t *testing.T) {
	store, b, _, lane := sourceLaneFixture(t)
	ctx := context.Background()
	p := &syntheticSourcePort{invoke: func(SourceRequest) (SourceResult, error) { return SourceResult{}, nil }}
	_ = lane(store).Source(ctx, "source.notify", source.Object{}, p)
	state, _ := store.State(ctx, b)
	entry := state.Runs["source-run"].Lanes["main"][0]
	v, err := sourceEnvelope(entry)
	if err != nil {
		t.Fatal(err)
	}
	n := NetworkIntent{RunID: "source-run", Lane: "main", Index: 0, Kind: entry.Kind, Digest: entry.Digest, Guard: entry.Guard}
	if _, _, err = store.BeginNetwork(ctx, b, n); !errors.Is(err, ErrConflict) {
		t.Fatal("network acquired source", err)
	}
	for _, err = range []error{store.CompleteNetwork(ctx, b, n, marshal(true), ""), store.Unknown(ctx, b, n), store.Reconcile(ctx, b, "source-run", "main", 0, marshal(true))} {
		if !errors.Is(err, ErrConflict) {
			t.Fatal("generic network mutated source", err)
		}
	}
	if _, err = store.Apply(ctx, b, Effect{RunID: "source-run", Lane: "main", Kind: entry.Kind, Digest: entry.Digest, Value: entry.Value}); !errors.Is(err, ErrConflict) {
		t.Fatal("Apply forged source", err)
	}
	good := SourceResult{Identity: v.Request.Identity, Outcome: SourceNormal, ProofID: "trusted"}
	for _, change := range []func(*SourceIdentity){func(i *SourceIdentity) { i.RunID = "another" }, func(i *SourceIdentity) { i.Lane = "other" }, func(i *SourceIdentity) { i.Index++ }, func(i *SourceIdentity) { i.Epoch = "other" }, func(i *SourceIdentity) { i.Config.Revision++ }, func(i *SourceIdentity) { i.Config.Digest = "other" }, func(i *SourceIdentity) { i.PayloadDigest = "other" }, func(i *SourceIdentity) { i.Kind = "source.emit" }, func(i *SourceIdentity) { i.Scope.Generation++ }} {
		forged := good
		change(&forged.Identity)
		if err := store.CompleteSource(ctx, b, v.Request, forged); !errors.Is(err, ErrConflict) {
			t.Fatal("wrong identity accepted", forged, err)
		}
	}
	after, _ := store.State(ctx, b)
	if !bytes.Equal(marshal(state), marshal(after)) {
		t.Fatal("rejected proofs mutated state")
	}
}
func TestSourceBoundaryUnknownRecoveryUsesOriginalEpochWithoutInvoke(t *testing.T) {
	store, b, lease, lane := sourceLaneFixture(t)
	ctx := context.Background()
	p := &syntheticSourcePort{invoke: func(SourceRequest) (SourceResult, error) { return SourceResult{}, nil }}
	_ = lane(store).Source(ctx, "source.notify", source.Object{}, p)
	state, _ := store.State(ctx, b)
	v, _ := sourceEnvelope(state.Runs["source-run"].Lanes["main"][0])
	oldEpoch := b.Epoch
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	b.Epoch = "replacement-owner"
	next, err := store.Claim(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	p.results = map[SourceIdentity]SourceResult{v.Request.Identity: {Identity: v.Request.Identity, Outcome: SourceNormal, ProofID: "late-trusted-observation"}}
	replay := (&Execution{Store: store, Binding: b, Epoch: oldEpoch, RunID: "source-run", DigestKey: fixtureKey}).Lane("main", "")
	if err := replay.Source(ctx, "source.notify", source.Object{}, p); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 || p.binds != 1 {
		t.Fatal("restart rebound/reinvoked", p.calls, p.binds)
	}
	state, _ = store.State(ctx, b)
	if len(state.Blocks) != 0 {
		t.Fatal("verified proof did not settle")
	}
}
func TestControllerSourceFailureBeforeHTTPIsTerminalAndTypedOnReplay(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "exception", true: "missing_port"}[missing], func(t *testing.T) {
			store, b, _ := fixture(t)
			http := &fixedHTTP{Response: confirmed}
			c := controller(store, b, http)
			boot(t, c)
			p := &syntheticSourcePort{invoke: func(r SourceRequest) (SourceResult, error) {
				return SourceResult{Identity: r.Identity, Outcome: SourceException, ErrorClass: "ValueError", Error: "synthetic render failure", ProofID: "render-exception-proof"}, nil
			}}
			c.Dependencies.Source = p
			if missing {
				c.Dependencies.Source = nil
			}
			cmd := Command{Action: "event", Event: &source.EventEnvelope{Data: source.Object{"action": "site_signin", "user": "synthetic"}}}
			for n := 0; n < 2; n++ {
				_, err := c.Execute(context.Background(), "event-run", cmd)
				var known *source.SourceFailure
				if missing {
					if !errors.Is(err, ErrStale) {
						t.Fatal(err)
					}
				} else if !errors.As(err, &known) || known.Class != "ValueError" {
					t.Fatal("lost typed exception", err)
				}
			}
			state, _ := store.State(context.Background(), b)
			if http.Calls.Load() != 0 || len(state.History) != 0 || len(state.Outbox) != 0 {
				t.Fatal("failure crossed initial source boundary")
			}
			if !missing && (state.ActiveRun != "" || state.Runs["event-run"].Phase != "failed_source" || p.calls != 1) {
				t.Fatal("typed failure not terminal/no repeat", state, p.calls)
			}
		})
	}
}
func TestControllerFreshInitDoesNotAuthorizeFreshService(t *testing.T) {
	store, b, _ := fixture(t)
	c := controller(store, b, nil)
	if _, err := c.Execute(context.Background(), "unauthorized-service", Command{Action: "service"}); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if _, err := c.Execute(context.Background(), "real-init", Command{Action: "init", Config: source.Object{"enabled": false}}); err != nil {
		t.Fatal("fresh real init blocked", err)
	}
}

type sourceCommitFault struct {
	Journal
	after  bool
	failed bool
}

func (j *sourceCommitFault) CompleteSource(ctx context.Context, b Binding, r SourceRequest, result SourceResult) error {
	if !j.failed {
		j.failed = true
		if j.after {
			if err := j.Journal.CompleteSource(ctx, b, r, result); err != nil {
				return err
			}
		}
		return ErrStore
	}
	return j.Journal.CompleteSource(ctx, b, r, result)
}
func TestSourceBoundaryCommitLossNeverRepeatsCallback(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_commit", true: "lost_commit_ack"}[after], func(t *testing.T) {
			store, b, _, lane := sourceLaneFixture(t)
			j := &sourceCommitFault{Journal: store, after: after}
			p := &syntheticSourcePort{}
			if err := lane(j).Source(context.Background(), "source.notify", source.Object{}, p); !errors.Is(err, ErrUncertain) {
				t.Fatal(err)
			}
			if err := lane(j).Source(context.Background(), "source.notify", source.Object{}, p); err != nil {
				t.Fatal(err)
			}
			state, _ := store.State(context.Background(), b)
			if p.calls != 1 || p.binds != 1 || len(state.Blocks) != 0 || state.Runs["source-run"].Lanes["main"][0].Phase != "receipt" {
				t.Fatal("commit-loss replay repeated source", p.calls, p.binds, state)
			}
		})
	}
}
func TestControllerSourceBoundariesPreserveHistoryNoticeConfigOrder(t *testing.T) {
	for _, target := range []string{"site_refresh", "cloudflare_speedtest", "category", "completion"} {
		t.Run(target, func(t *testing.T) {
			store, b, _ := fixture(t)
			http := &fixedHTTP{Response: confirmed}
			if target == "site_refresh" || target == "cloudflare_speedtest" {
				http.Response = `<input type="password">`
			}
			c := controller(store, b, http)
			boot(t, c)
			if _, err := c.Execute(context.Background(), "config-cf", Command{Action: "init", Config: source.Object{"enabled": true, "notify": true, "sign_sites": []any{1}, "login_sites": []any{}, "retry_keyword": "Cookie已失效", "auto_cf": 1}}); err != nil {
				t.Fatal(err)
			}
			before, _ := store.State(context.Background(), b)
			seen := []string{}
			p := &syntheticSourcePort{invoke: func(r SourceRequest) (SourceResult, error) {
				var payload source.Object
				if err := decode(r.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				label := ""
				if r.Identity.Kind == "source.emit" {
					data, _ := payload["data"].(map[string]any)
					if data == nil {
						if value, ok := payload["data"].(source.Object); ok {
							data = value
						}
					}
					label, _ = data["action"].(string)
				} else {
					title, _ := payload["title"].(string)
					switch title {
					case "开始站点签到 ...":
						label = "start"
					case "站点签到完成！":
						label = "completion"
					default:
						label = "category"
					}
				}
				seen = append(seen, label)
				result := SourceResult{Identity: r.Identity, Outcome: SourceNormal, ProofID: "proof:" + sourceGuard(r.Identity)}
				if label == target {
					result.Outcome = SourceException
					result.ErrorClass = "ValueError"
					result.Error = "synthetic source boundary"
				}
				return result, nil
			}}
			c.Dependencies.Source = p
			cmd := Command{Action: "service"}
			if target == "completion" {
				cmd = Command{Action: "event", Event: &source.EventEnvelope{Data: source.Object{"action": "site_signin", "user": "synthetic"}}}
			}
			_, err := c.Execute(context.Background(), "ordered", cmd)
			var known *source.SourceFailure
			if !errors.As(err, &known) {
				t.Fatal("target did not fail", target, seen, err)
			}
			state, _ := store.State(context.Background(), b)
			wantHistory := 2
			if target == "site_refresh" {
				wantHistory = 1
			}
			if len(state.History) != wantHistory || !bytes.Equal(state.Config, before.Config) || seen[len(seen)-1] != target || state.Runs["ordered"].Phase != "failed_source" {
				t.Fatal("source exception crossed boundary", target, seen, len(state.History), state.Runs["ordered"].Phase)
			}
			for _, e := range state.Runs["ordered"].Lanes["main"] {
				if e.Kind == "config" {
					t.Fatal("config ran after source exception")
				}
			}
		})
	}
}

func TestSourceCallbackDoesNotHoldStoreLocksAndRetainsExecutionOwnership(t *testing.T) {
	store, b, lease := fixture(t)
	http := &fixedHTTP{Response: confirmed}
	c := controller(store, b, http)
	boot(t, c)
	entered, release := make(chan struct{}), make(chan struct{})
	p := &syntheticSourcePort{invoke: func(r SourceRequest) (SourceResult, error) {
		if _, err := store.State(context.Background(), b); err != nil {
			t.Error("callback could not read journal", err)
		}
		close(entered)
		<-release
		return SourceResult{Identity: r.Identity, Outcome: SourceNormal, ProofID: "callback-proof"}, nil
	}}
	c.Dependencies.Source = p
	done := make(chan error, 1)
	go func() {
		_, err := c.Execute(context.Background(), "blocked-notice", Command{Action: "service"})
		done <- err
	}()
	<-entered
	if err := lease.Close(); !errors.Is(err, ErrBusy) {
		t.Error("callback released owner", err)
	}
	if _, err := c.Execute(context.Background(), "parallel", Command{Action: "service"}); !errors.Is(err, ErrBusy) {
		t.Error("callback released execution", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestSourceCancellationRecordsProofButCannotContinueConfig(t *testing.T) {
	store, b, _ := fixture(t)
	http := &fixedHTTP{Response: confirmed}
	c := controller(store, b, http)
	boot(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &syntheticSourcePort{invoke: func(r SourceRequest) (SourceResult, error) {
		cancel()
		return SourceResult{Identity: r.Identity, Outcome: SourceNormal, ProofID: "normal-before-cancel"}, nil
	}}
	c.Dependencies.Source = p
	if _, err := c.Execute(ctx, "cancelled-source", Command{Action: "service"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	state, _ := store.State(context.Background(), b)
	entries := state.Runs["cancelled-source"].Lanes["main"]
	if entries[len(entries)-1].Kind != "source.notify" || entries[len(entries)-1].Phase != "receipt" || state.ActiveRun != "cancelled-source" {
		t.Fatal("cancelled source continued or discarded result", entries)
	}
	if _, err := c.Execute(context.Background(), "cancelled-source", Command{Action: "service"}); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatal("cancelled source was reinvoked")
	}
}
func TestTerminalSourceFailureStillRequiresIndependentProofOnReplay(t *testing.T) {
	store, b, _ := fixture(t)
	c := controller(store, b, &fixedHTTP{Response: confirmed})
	boot(t, c)
	p := &syntheticSourcePort{invoke: func(r SourceRequest) (SourceResult, error) {
		return SourceResult{Identity: r.Identity, Outcome: SourceException, ErrorClass: "ValueError", Error: "synthetic", ProofID: "typed-proof"}, nil
	}}
	c.Dependencies.Source = p
	_, err := c.Execute(context.Background(), "typed-terminal", Command{Action: "service"})
	var known *source.SourceFailure
	if !errors.As(err, &known) {
		t.Fatal(err)
	}
	p.results = nil
	if _, err := c.Execute(context.Background(), "typed-terminal", Command{Action: "service"}); !errors.Is(err, ErrUncertain) {
		t.Fatal("journal typed failure substituted for independent proof", err)
	}
	if p.calls != 1 {
		t.Fatal("terminal typed failure resent")
	}
}

func TestCompletedSourceReplayChecksEveryIndependentProofWithoutMutation(t *testing.T) {
	for _, which := range []string{"missing_first", "missing_middle", "missing_last", "changed_identity", "changed_proof", "unknown", "read_panic", "no_port"} {
		t.Run(which, func(t *testing.T) {
			store, b, _ := fixture(t)
			http := &fixedHTTP{Response: confirmed}
			c := controller(store, b, http)
			boot(t, c)
			p := &syntheticSourcePort{}
			c.Dependencies.Source = p
			cmd := Command{Action: "event", Event: &source.EventEnvelope{Data: source.Object{"action": "site_signin", "user": "synthetic"}}}
			if _, err := c.Execute(context.Background(), "completed-event", cmd); err != nil {
				t.Fatal(err)
			}
			state, _ := store.State(context.Background(), b)
			requests := []SourceIdentity{}
			for _, entry := range state.Runs["completed-event"].Lanes["main"] {
				if sourceKind(entry.Kind) {
					v, err := sourceEnvelope(entry)
					if err != nil {
						t.Fatal(err)
					}
					requests = append(requests, v.Request.Identity)
				}
			}
			if len(requests) != 3 || p.calls != 3 || p.binds != 3 {
				t.Fatal("fixture did not exercise all notices", len(requests), p.calls, p.binds)
			}
			if _, err := c.Execute(context.Background(), "completed-event", cmd); err != nil {
				t.Fatal("valid completed proof rejected", err)
			}
			switch which {
			case "missing_first":
				delete(p.results, requests[0])
			case "missing_middle":
				delete(p.results, requests[1])
			case "missing_last":
				delete(p.results, requests[2])
			case "changed_identity":
				r := p.results[requests[1]]
				r.Identity.Config.Revision++
				p.results[requests[1]] = r
			case "changed_proof":
				r := p.results[requests[1]]
				r.ProofID = "substituted"
				p.results[requests[1]] = r
			case "unknown":
				r := p.results[requests[1]]
				r.Outcome = SourceUnknown
				p.results[requests[1]] = r
			case "read_panic":
				p.read = func(SourceIdentity) (SourceResult, bool, error) { panic("synthetic foreign proof panic") }
			case "no_port":
				c.Dependencies.Source = nil
			}
			if result, err := c.Execute(context.Background(), "completed-event", cmd); !errors.Is(err, ErrUncertain) || result != nil {
				t.Fatal("terminal journal bypassed missing independent proof", result, err)
			}
			after, _ := store.State(context.Background(), b)
			if !bytes.Equal(marshal(state), marshal(after)) || p.calls != 3 || p.binds != 3 || http.Calls.Load() != 1 {
				t.Fatal("completed proof check mutated/reinvoked")
			}
		})
	}
}
