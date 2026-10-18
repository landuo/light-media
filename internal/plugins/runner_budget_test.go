package plugins

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"sync"
	"testing"
	"time"
)

func budgetFixture(t *testing.T, scenario string) Command {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{Path: path, Args: []string{"-test.run=^TestRunnerBudgetWorkerFixture$", "--", "runner-budget", scenario}}
}

// A real SDK child, including negotiated transport and stop/process cleanup.
func TestRunnerBudgetWorkerFixture(t *testing.T) {
	idx := slices.Index(os.Args, "runner-budget")
	if idx < 0 {
		return
	}
	scenario := os.Args[idx+1]
	err := Serve(context.Background(), os.Stdin, os.Stdout, fixtureManifest(), func(ctx context.Context, method string, _ json.RawMessage, host HostCaller) (json.RawMessage, error) {
		if method == "stop" {
			return json.RawMessage(`null`), nil
		}
		for i := 0; i < 3; i++ {
			if scenario == "cumulative" {
				time.Sleep(250 * time.Millisecond)
			}
			if _, err := host(ctx, "data.read", json.RawMessage(`{}`)); err != nil {
				return nil, err
			}
		}
		return json.RawMessage(`{"done":true}`), nil
	})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestRunnerBudgetExcludesHostButRetainsLegacyDeadline(t *testing.T) {
	for _, exclude := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "exclude-host"}[exclude], func(t *testing.T) {
			calls := 0
			start := time.Now()
			// The real race-instrumented worker also spends up to 1s in the
			// runtime's exit hook; that remains charged to the worker budget.
			out, err := (Runner{Timeout: 2 * time.Second, ExcludeHostTime: exclude}).Run(context.Background(), budgetFixture(t, "normal"), fixtureManifest(), "Fixture", "invoke", nil, func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
				calls++
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(900 * time.Millisecond):
					return json.RawMessage(`null`), nil
				}
			})
			if exclude {
				if err != nil || calls != 3 || out.HostCalls != 3 || time.Since(start) < 2700*time.Millisecond {
					t.Fatal("native host time consumed worker budget", err, calls, time.Since(start))
				}
			} else if codeOf(err) != "execution_canceled" {
				t.Fatal("legacy deadline changed", err, calls)
			}
		})
	}
}

func TestRunnerBudgetDoesNotResetAtHostCalls(t *testing.T) {
	calls := 0
	_, err := (Runner{Timeout: 650 * time.Millisecond, ExcludeHostTime: true}).Run(context.Background(), budgetFixture(t, "cumulative"), fixtureManifest(), "Fixture", "invoke", nil, func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		time.Sleep(30 * time.Millisecond)
		return json.RawMessage(`null`), nil
	})
	if codeOf(err) != "execution_canceled" || calls < 1 || calls >= 3 {
		t.Fatal("each callback granted a new worker budget", err, calls)
	}
}

func TestRunnerBudgetParentCancellationDuringHost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := (Runner{Timeout: time.Second, ExcludeHostTime: true}).Run(ctx, budgetFixture(t, "normal"), fixtureManifest(), "Fixture", "invoke", nil, func(ctx context.Context, _ Identity, _ string, _ json.RawMessage) (json.RawMessage, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("child did not enter host")
	}
	cancel()
	select {
	case err := <-done:
		if codeOf(err) != "execution_canceled" {
			t.Fatal("parent cancellation lost", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled host/child did not finish")
	}
}

func TestRunnerBudgetTimerPauseResumeCloseRaces(t *testing.T) {
	for i := 0; i < 100; i++ {
		ctx, cancel, b := runnerContext(context.Background(), time.Millisecond, true)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			if b.pause() {
				time.Sleep(time.Millisecond)
				b.resume()
			}
		}()
		go func() { defer wg.Done(); time.Sleep(time.Millisecond); cancel() }()
		go func() { defer wg.Done(); time.Sleep(time.Millisecond); b.close() }()
		wg.Wait()
		select {
		case <-ctx.Done():
		default:
			t.Fatal("close did not cancel")
		}
	}
	ctx, cancel, b := runnerContext(context.Background(), 30*time.Millisecond, true)
	defer cancel()
	if !b.pause() {
		t.Fatal("initial pause failed")
	}
	time.Sleep(60 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("stale timer canceled paused host")
	}
	b.resume()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("resumed worker has no deadline")
	}
}
