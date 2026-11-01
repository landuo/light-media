package plugins

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func specificBudgetManifest() Manifest {
	manifest := fixtureManifest()
	manifest.Capabilities = append(append([]string{}, manifest.Capabilities...), QBittorrentGetIDByTagCapability)
	return manifest
}

// Real SDK child of this same compiled test program. No hidden compiler,
// downloader/network, timing env override or special production hook.
func TestSpecificHostBudgetWorkerFixture(t *testing.T) {
	index := slices.Index(os.Args, "specific-host-budget")
	if index < 0 {
		return
	}
	concurrent := len(os.Args) > index+1 && os.Args[index+1] == "concurrent"
	count := 0
	handle := func(ctx context.Context, method string, raw json.RawMessage, host HostCaller) (json.RawMessage, error) {
		if method == "stop" {
			return json.RawMessage(`null`), nil
		}
		count++
		var scenario string
		if err := json.Unmarshal(raw, &scenario); err != nil {
			return nil, err
		}
		call := func(capability string) error { _, err := host(ctx, capability, json.RawMessage(`{}`)); return err }
		switch scenario {
		case "target":
			if err := call(QBittorrentGetIDByTagCapability); err != nil {
				return nil, err
			}
		case "other":
			if err := call("data.read"); err != nil {
				return nil, err
			}
		case "cumulative":
			for i := 0; i < 3; i++ {
				time.Sleep(250 * time.Millisecond) // Worker work, not excluded host time.
				if err := call(QBittorrentGetIDByTagCapability); err != nil {
					return nil, err
				}
			}
		case "state":
		}
		return json.Marshal(count)
	}
	var err error
	if concurrent {
		err = ServeConcurrent(context.Background(), os.Stdin, os.Stdout, specificBudgetManifest(), handle)
	} else {
		err = Serve(context.Background(), os.Stdin, os.Stdout, specificBudgetManifest(), handle)
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func startSpecificBudget(t *testing.T, concurrent bool) *Client {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestSpecificHostBudgetWorkerFixture$", "--", "specific-host-budget"}
	if concurrent {
		args = append(args, "concurrent")
	}
	runner := Runner{Timeout: 3 * time.Second, ConcurrentCalls: true}
	client, err := runner.Start(context.Background(), Command{Path: path, Args: args}, specificBudgetManifest(), sessionBinding())
	if err != nil {
		t.Fatal(err)
	}
	if client.SupportsConcurrent() != concurrent {
		t.Fatal("actual negotiated fixture mode differs")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = client.Stop(ctx)
	})
	return client
}

func TestSerialSpecificHostBudgetSelectedAndFreshCall(t *testing.T) {
	client := startSpecificBudget(t, false)
	originalLimits := client.session.limits
	options := CallOptions{Timeout: 400 * time.Millisecond, ExcludeHostMethod: QBittorrentGetIDByTagCapability}
	start := time.Now()
	out, err := client.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"target"`), delayedSessionHost(900*time.Millisecond), options)
	if err != nil || string(out.Result) != "1" || out.HostCalls != 1 || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("selected callback consumed worker budget: %+v %v elapsed=%v", out, err, time.Since(start))
	}
	if client.runner.Timeout != 3*time.Second || client.runner.ExcludeHostTime || client.session.limits != originalLimits || client.session.hostBudget != nil || client.session.hostBudgetMethod != "" {
		t.Fatal("per-call selector leaked into defaults/next invocation/wire limits")
	}
	out, err = client.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"state"`), nil, CallOptions{Timeout: 400 * time.Millisecond})
	if err != nil || string(out.Result) != "2" {
		t.Fatalf("same serial object not retained: %+v/%v", out, err)
	}
	// The SAME callback on the next invocation must again be charged unless
	// this trusted call explicitly opts in. No stale selector inheritance.
	out, err = client.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"target"`), delayedSessionHost(900*time.Millisecond), CallOptions{Timeout: 400 * time.Millisecond})
	if codeOf(err) != "execution_canceled" || out.Result != nil {
		t.Fatalf("next call inherited exclusion: %+v/%v", out, err)
	}
}

func TestSerialSpecificHostBudgetOtherAndCumulativeWorker(t *testing.T) {
	t.Run("other-capability-still-charged", func(t *testing.T) {
		client := startSpecificBudget(t, false)
		out, err := client.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"other"`), delayedSessionHost(900*time.Millisecond), CallOptions{Timeout: 400 * time.Millisecond, ExcludeHostMethod: QBittorrentGetIDByTagCapability})
		if codeOf(err) != "execution_canceled" || out.Result != nil || out.HostCalls != 1 {
			t.Fatalf("other capability gained host time: %+v/%v", out, err)
		}
	})
	t.Run("cumulative-worker-not-reset", func(t *testing.T) {
		client := startSpecificBudget(t, false)
		var calls atomic.Int32
		out, err := client.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"cumulative"`), func(ctx context.Context, _ Identity, method string, _ json.RawMessage) (json.RawMessage, error) {
			if method != QBittorrentGetIDByTagCapability {
				t.Error("unexpected selected capability")
			}
			calls.Add(1)
			return delayedSessionHost(80*time.Millisecond)(ctx, Identity{}, method, nil)
		}, CallOptions{Timeout: 650 * time.Millisecond, ExcludeHostMethod: QBittorrentGetIDByTagCapability})
		if codeOf(err) != "execution_canceled" || out.Result != nil || calls.Load() < 1 || calls.Load() >= 3 {
			t.Fatalf("callback reset worker remainder: %+v/%v calls=%d", out, err, calls.Load())
		}
	})
}

func TestSerialSpecificHostBudgetLifeCancellation(t *testing.T) {
	client := startSpecificBudget(t, false)
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := client.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"target"`), func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}, CallOptions{Timeout: time.Second, ExcludeHostMethod: QBittorrentGetIDByTagCapability})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("real serial worker did not enter selected host")
	}
	client.cancel() // Existing session/application life, not an HTTP-policy change.
	select {
	case err := <-done:
		if codeOf(err) != "execution_canceled" {
			t.Fatal("life cancellation lost", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("paused timer blocked life cancellation")
	}
	select {
	case <-client.done:
	default:
		t.Fatal("actual worker Wait was not joined")
	}
}

func TestSpecificHostBudgetOptionsRejectBeforeDispatch(t *testing.T) {
	serial := startSpecificBudget(t, false)
	invalid := []CallOptions{
		{Timeout: time.Second, ExcludeHostMethod: QBittorrentGetIDByTagCapability, ExcludeHostTime: true},
		{CallerDeadlineOnly: true, ExcludeHostMethod: QBittorrentGetIDByTagCapability},
		{Timeout: time.Second, ExcludeHostMethod: "transport.concurrent"},
		{Timeout: time.Second, ExcludeHostMethod: "not a method"},
	}
	for _, options := range invalid {
		if _, err := serial.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"state"`), nil, options); codeOf(err) != "invalid_limits" {
			t.Fatalf("invalid policy admitted: %#v/%v", options, err)
		}
	}
	out, err := serial.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"state"`), nil, CallOptions{})
	if err != nil || string(out.Result) != "1" {
		t.Fatalf("invalid policy dispatched/changed serial state: %+v/%v", out, err)
	}
	// Timing opt-in does not supply a HostHandler/grant. This is an actual
	// denied SDK callback, not a fabricated successful native result. App
	// declaration/current-authority checks remain separate and unchanged.
	out, err = serial.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"target"`), nil, CallOptions{Timeout: time.Second, ExcludeHostMethod: QBittorrentGetIDByTagCapability})
	if codeOf(err) != "plugin_operation_failed" || out.Result != nil || out.HostCalls != 1 {
		t.Fatalf("timing option created missing host authority: %+v/%v", out, err)
	}
	out, err = serial.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"state"`), nil, CallOptions{})
	if err != nil || string(out.Result) != "3" {
		t.Fatalf("denied callback damaged serial object: %+v/%v", out, err)
	}

	// This explicitly concurrent child proves only policy rejection, not the
	// ordinary serial behavior covered by all three preceding roots.
	concurrent := startSpecificBudget(t, true)
	options := CallOptions{Timeout: time.Second, ExcludeHostMethod: QBittorrentGetIDByTagCapability}
	if _, err := concurrent.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"state"`), nil, options); codeOf(err) != "invalid_limits" {
		t.Fatal("concurrent Call silently accepted serial-only policy", err)
	}
	if pending, err := concurrent.StartCall(context.Background(), "invoke", json.RawMessage(`"state"`), nil, options); codeOf(err) != "invalid_limits" || pending != nil {
		t.Fatal("concurrent StartCall accepted serial-only policy", pending, err)
	}
	out, err = concurrent.CallWithOptions(context.Background(), "invoke", json.RawMessage(`"state"`), nil, CallOptions{CallerDeadlineOnly: true})
	if err != nil || string(out.Result) != "1" {
		t.Fatalf("concurrent normal mode changed: %+v/%v", out, err)
	}
}
