package plugins

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func delayedSessionHost(delay time.Duration) HostHandler {
	return func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			return json.RawMessage(`"complete"`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func TestPersistentSessionCallOptionsTimeoutIsPerInvocation(t *testing.T) {
	c := startSession(t, Runner{Timeout: 500 * time.Millisecond}, "state")
	limits := c.session.limits
	out, err := c.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"host"`), delayedSessionHost(700*time.Millisecond), CallOptions{Timeout: 2 * time.Second})
	if err != nil || string(out.Result) != `"complete"` {
		t.Fatalf("longer action budget not applied: %+v %v", out, err)
	}
	if c.runner.Timeout != 500*time.Millisecond || c.runner.ExcludeHostTime || c.session.limits != limits {
		t.Fatal("call options mutated instance defaults or wire limits")
	}
	out, err = c.Call(context.Background(), "invoke", json.RawMessage(`"host"`), delayedSessionHost(time.Second))
	if codeOf(err) != "execution_canceled" || out.Result != nil {
		t.Fatalf("default Call borrowed previous action budget: %+v %v", out, err)
	}
	if _, err = c.CallWithOptions(context.Background(), "invoke", nil, nil, CallOptions{Timeout: 2 * time.Second}); err == nil {
		t.Fatal("canceled worker was restarted")
	}
}

func TestPersistentSessionCallOptionsHostExclusionCanBeEnabled(t *testing.T) {
	c := startSession(t, Runner{Timeout: time.Second}, "state")
	out, err := c.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"host"`), delayedSessionHost(300*time.Millisecond), CallOptions{Timeout: 100 * time.Millisecond, ExcludeHostTime: true})
	if err != nil || string(out.Result) != `"complete"` {
		t.Fatalf("fresh host-excluded budget failed: %+v %v", out, err)
	}
	if c.runner.ExcludeHostTime {
		t.Fatal("call changed default host policy")
	}
	out, err = c.Call(context.Background(), "invoke", nil, nil)
	if err != nil || string(out.Result) != "2" {
		t.Fatalf("live object not retained: %+v %v", out, err)
	}
}

func TestPersistentSessionCallOptionsHostExclusionCanBeDisabled(t *testing.T) {
	c := startSession(t, Runner{Timeout: time.Second, ExcludeHostTime: true}, "state")
	out, err := c.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"host"`), delayedSessionHost(300*time.Millisecond), CallOptions{Timeout: 100 * time.Millisecond, ExcludeHostTime: false})
	if codeOf(err) != "execution_canceled" || out.Result != nil {
		t.Fatalf("explicit false inherited Start host exclusion: %+v %v", out, err)
	}
}

func TestPersistentSessionCallOptionsValidateBeforeDispatch(t *testing.T) {
	c := startSession(t, Runner{Timeout: time.Second, ExcludeHostTime: true}, "state")
	for _, timeout := range []time.Duration{-time.Second, time.Microsecond, 31 * time.Minute} {
		if _, err := c.CallWithOptions(context.Background(), "invoke", nil, nil, CallOptions{Timeout: timeout}); codeOf(err) != "invalid_limits" {
			t.Fatalf("timeout=%s: %v", timeout, err)
		}
	}
	out, err := c.CallWithOptions(context.Background(), "invoke", nil, nil, CallOptions{})
	if err != nil || string(out.Result) != "1" {
		t.Fatalf("invalid options dispatched or killed object: %+v %v", out, err)
	}
	if c.runner.Timeout != time.Second || !c.runner.ExcludeHostTime {
		t.Fatal("zero options overwrote Start defaults")
	}
}
