package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachedReadCoalescesColdKeyAndDoesNotShareMutableModels(t *testing.T) {
	a := newTestApp(t)
	var calls atomic.Int32
	var wg sync.WaitGroup
	results := make([]map[string]any, 20)
	failures := make(chan error, 20)
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := a.cached(context.Background(), "same", time.Hour, &results[i], func() (any, error) {
				calls.Add(1)
				time.Sleep(20 * time.Millisecond)
				return map[string]any{"value": "original"}, nil
			})
			if err != nil {
				failures <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("fetches=%d", calls.Load())
	}
	results[0]["value"] = "changed"
	if results[1]["value"] != "original" {
		t.Fatal("shared mutable result")
	}
	if len(a.cacheReads.active) != 0 {
		t.Fatal("retained flight")
	}
}
func TestCachedReadCanceledWaiterDoesNotCancelLeader(t *testing.T) {
	a := newTestApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		var out any
		_, err := a.cached(context.Background(), "leader", time.Hour, &out, func() (any, error) { close(entered); <-release; return "ok", nil })
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out any
	if _, err := a.cached(ctx, "leader", time.Hour, &out, func() (any, error) { t.Error("canceled waiter fetched"); return nil, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestCachedReadCanceledLeaderAllowsLiveCallerToRetry(t *testing.T) {
	a := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	leader := make(chan error, 1)
	go func() {
		var out any
		_, err := a.cached(ctx, "retry", time.Hour, &out, func() (any, error) { close(entered); <-ctx.Done(); return nil, ctx.Err() })
		leader <- err
	}()
	<-entered
	live := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		var out string
		_, err := a.cached(context.Background(), "retry", time.Hour, &out, func() (any, error) { calls.Add(1); return "ok", nil })
		if err == nil && out != "ok" {
			err = errors.New("bad result")
		}
		live <- err
	}()
	cancel()
	if err := <-leader; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := <-live; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("live fetch missing")
	}
}
func TestCachedReadKeysDoNotBlockEachOtherAndStaleFallbackSurvives(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	old := time.Now().Add(-2 * time.Hour)
	if err := a.Store.Put(ctx, "cache", "stale", map[string]any{"at": old, "data": map[string]any{"value": "old"}}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var out any
		a.cached(ctx, "slow", time.Hour, &out, func() (any, error) { close(entered); <-release; return "ok", nil })
	}()
	<-entered
	defer func() { close(release); <-done }()
	fast, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var out map[string]any
	at, err := a.cached(fast, "stale", time.Hour, &out, func() (any, error) { return nil, errors.New("offline") })
	if err != nil || out["value"] != "old" || !at.Equal(old) {
		t.Fatalf("%v %v %v", at, out, err)
	}
}
func TestCachedReadPanicReleasesFlight(t *testing.T) {
	a := newTestApp(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("missing panic")
			}
		}()
		var out any
		a.cached(context.Background(), "panic", time.Hour, &out, func() (any, error) { panic("fixture") })
	}()
	var out string
	if _, err := a.cached(context.Background(), "panic", time.Hour, &out, func() (any, error) { return "recovered", nil }); err != nil || out != "recovered" {
		t.Fatalf("%s %v", out, err)
	}
}
func TestCachedReadWaitingDeadlineDoesNotCancelLeader(t *testing.T) {
	a := newTestApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		var out any
		_, err := a.cached(context.Background(), "waiting", time.Hour, &out, func() (any, error) { close(entered); <-release; return "ok", nil })
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var out any
	_, err := a.cached(ctx, "waiting", time.Hour, &out, func() (any, error) { t.Error("waiter fetched"); return nil, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("leader canceled")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestCachedReadDifferentFreshnessPoliciesAreIndependent(t *testing.T) {
	a := newTestApp(t)
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	done := make(chan error, 2)
	for _, ttl := range []time.Duration{time.Hour, 24 * time.Hour} {
		go func() {
			var out any
			_, err := a.cached(context.Background(), "different-ttl", ttl, &out, func() (any, error) { started <- struct{}{}; <-release; return "ok", nil })
			done <- err
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("different TTL joined")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

type observedCacheWaitContext struct {
	context.Context
	waiting chan<- struct{}
}

func (c observedCacheWaitContext) Done() <-chan struct{} { c.waiting <- struct{}{}; return nil }
func TestCachedReadSharesUpstreamTimeoutWithoutRetryingLiveLeader(t *testing.T) {
	a := newTestApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	waiting := make(chan struct{}, 10)
	results := make(chan error, 11)
	var calls atomic.Int32
	fetch := func() (any, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return nil, context.DeadlineExceeded
	}
	go func() {
		var out any
		_, err := a.cached(context.Background(), "upstream-timeout", time.Hour, &out, fetch)
		results <- err
	}()
	<-entered
	for i := 0; i < 10; i++ {
		go func() {
			var out any
			_, err := a.cached(observedCacheWaitContext{context.Background(), waiting}, "upstream-timeout", time.Hour, &out, fetch)
			results <- err
		}()
	}
	for i := 0; i < 10; i++ {
		select {
		case <-waiting:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("waiter did not join")
		}
	}
	close(release)
	for i := 0; i < 11; i++ {
		if err := <-results; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream retried %d", calls.Load())
	}
}
