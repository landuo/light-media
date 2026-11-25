package plugins

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestRunnerObservationActualSDKSequenceAndNilParity(t *testing.T) {
	var observed []RunObservation
	host := func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":"from-host"}`), nil
	}
	runner := Runner{Timeout: 5 * time.Second, ObservePhase: func(v RunObservation) { observed = append(observed, v) }}
	got, err := runner.Run(context.Background(), fixtureCommand(t, "sdk"), fixtureManifest(), "Fixture", "invoke", nil, host)
	if err != nil {
		t.Fatal("observed SDK failed", codeOf(err))
	}
	want := []RunPhase{PhaseRunEntered, PhaseStartBegin, PhaseStartEnd, PhaseHandshakeBegin, PhaseHandshakeEnd, PhaseNegotiateBegin, PhaseNegotiateEnd, PhaseInvokeBegin, PhaseInvokeEnd, PhaseStopBegin, PhaseStopEnd, PhaseWaitBegin, PhaseWaitEnd, PhaseRunReturned}
	var phases []RunPhase
	for _, v := range observed {
		phases = append(phases, v.Phase)
		if v.ContextState != RunContextActive {
			t.Fatal("observer reported internal cleanup cancellation")
		}
	}
	if !reflect.DeepEqual(phases, want) {
		t.Fatal("unexpected actual process boundary order", phases)
	}
	runner.ObservePhase = nil
	plain, err := runner.Run(context.Background(), fixtureCommand(t, "sdk"), fixtureManifest(), "Fixture", "invoke", nil, host)
	if err != nil || !reflect.DeepEqual(plain, got) {
		t.Fatal("nil observer changed SDK outcome", codeOf(err))
	}
}

func TestRunnerObservationActualFailureAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		timeout  time.Duration
		code     string
		state    RunContextState
	}{
		{"wrong_version", 5 * time.Second, "handshake_mismatch", RunContextActive},
		{"blocked", 200 * time.Millisecond, "execution_canceled", RunContextDeadline},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			var events []RunObservation
			runner := Runner{Timeout: tc.timeout, ObservePhase: func(v RunObservation) { events = append(events, v) }}
			_, err := runner.Run(context.Background(), fixtureCommand(t, tc.scenario), fixtureManifest(), "Fixture", "invoke", nil, nil)
			if codeOf(err) != tc.code {
				t.Fatal("existing error changed", codeOf(err))
			}
			if len(events) < 4 || events[len(events)-1] != (RunObservation{PhaseRunReturned, tc.state}) {
				t.Fatal("missing actual terminal context", events)
			}
			n := len(events)
			if events[n-3].Phase != PhaseCleanupWaitBegin || events[n-2].Phase != PhaseCleanupWaitEnd {
				t.Fatal("failure child cleanup not observed", events)
			}
			for _, v := range events {
				if v.Phase == PhaseInvokeBegin {
					t.Fatal("failed handshake invented invocation")
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var events []RunObservation
	_, err := (Runner{ObservePhase: func(v RunObservation) { events = append(events, v) }}).Run(ctx, fixtureCommand(t, "blocked"), fixtureManifest(), "Fixture", "invoke", nil, nil)
	if codeOf(err) != "worker_start_failed" || events[len(events)-1] != (RunObservation{PhaseRunReturned, RunContextCanceled}) {
		t.Fatal("pre-canceled startup outcome changed")
	}
}

func TestRunnerObservationEarlyReturnAndEnums(t *testing.T) {
	var events []RunObservation
	_, err := (Runner{ObservePhase: func(v RunObservation) { events = append(events, v) }}).Run(nil, Command{}, fixtureManifest(), "Fixture", "invoke", nil, nil)
	if codeOf(err) != "invalid_invocation" || len(events) != 2 || events[1] != (RunObservation{PhaseRunReturned, RunContextUnavailable}) {
		t.Fatal("invalid invocation observation")
	}
	if RunPhase(255).String() != "unknown" || RunContextState(255).String() != "unavailable" {
		t.Fatal("unbounded enum representation")
	}
}
