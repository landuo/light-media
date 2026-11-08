package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The source's per-site pool requires overlap. The existing SDK's budgets and
// incoming slots belong to individual invocations, not to a whole source pass.
func TestIndependentParallelWindowIsPerIncomingAndIDsStayDistinct(t *testing.T) {
	c := pStart(t, Runner{MaxHostCalls: 2}, "parallel")
	entered := make(chan int, 8)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls []*PendingCall
	var active, peak atomic.Int64
	var mu sync.Mutex
	ids := map[string]bool{}
	for phase := 0; phase < 4; phase++ {
		phase := phase
		pending, e := c.StartCall(pCtx(t), "invoke", pRaw(parallelInput{Count: 2, Offset: phase * 2}), func(ctx context.Context, id Identity, m string, b json.RawMessage) (json.RawMessage, error) {
			if id != c.binding.Identity || m != "test.host" {
				return nil, fmt.Errorf("scope identity/method changed")
			}
			var n int
			if json.Unmarshal(b, &n) != nil || n/2 != phase {
				return nil, fmt.Errorf("phase payload crossed")
			}
			mu.Lock()
			ids[InvocationID(ctx)] = true
			mu.Unlock()
			nactive := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); nactive > old && !peak.CompareAndSwap(old, nactive); old = peak.Load() {
			}
			entered <- n
			<-release
			return pRaw(1000 + n), nil
		}, CallOptions{CallerDeadlineOnly: true})
		if e != nil {
			t.Fatal("independent incoming admission", phase, e)
		}
		calls = append(calls, pending)
	}
	for i := 0; i < 8; i++ {
		select {
		case <-entered:
		case <-time.After(4 * time.Second):
			t.Fatal("window accidentally session-wide", i)
		}
	}
	c.concurrent.mu.Lock()
	entries := len(c.concurrent.calls)
	c.concurrent.mu.Unlock()
	mu.Lock()
	distinct := len(ids)
	mu.Unlock()
	if peak.Load() != 8 || entries != 4 || distinct != 4 {
		t.Fatal("wrong domain/identity", peak.Load(), entries, distinct)
	}
	once.Do(func() { close(release) })
	for phase, p := range calls {
		out, e := p.Wait()
		if e != nil {
			t.Fatal(e)
		}
		var r parallelResult
		if json.Unmarshal(out.Result, &r) != nil {
			t.Fatal("result decode")
		}
		if out.HostCalls != 2 || r.After.HostCallsCharged != 2 || r.After.MaxParallelHostCalls != 2 {
			t.Fatal("per incoming accounting", phase, out.HostCalls, r.After)
		}
		for i := 0; i < 2; i++ {
			if r.Errors[i] != "" || string(r.Results[i]) != fmt.Sprint(1000+phase*2+i) {
				t.Fatal("cross-delivered result", phase, i, r.Errors, r.Results)
			}
		}
	}
	t.Log("physical8 callbacks under negotiated N2 each; four distinct incoming IDs; exact8 result payloads; each charged2")
}

// Pending.Wait cancellation is not a physical drain receipt. The host table
// must remain full until callbacks that ignore cancellation actually return.
func TestIndependentParallelCanceledPhysicalHostsRetainAll32IncomingSlots(t *testing.T) {
	c := pStart(t, Runner{}, "parallel")
	entered := make(chan struct{}, 32)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var running atomic.Int64
	var calls []*PendingCall
	for i := 0; i < 32; i++ {
		p, e := c.StartCall(pCtx(t), "invoke", pRaw(parallelInput{Count: 1}), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
			running.Add(1)
			defer running.Add(-1)
			entered <- struct{}{}
			<-release
			return json.RawMessage(`1`), nil
		}, CallOptions{CallerDeadlineOnly: true})
		if e != nil {
			t.Fatal("before full incoming limit", i, e)
		}
		calls = append(calls, p)
	}
	for i := 0; i < 32; i++ {
		select {
		case <-entered:
		case <-time.After(4 * time.Second):
			t.Fatal("physical entry", i)
		}
	}
	for _, p := range calls {
		p.Cancel()
	}
	for _, p := range calls {
		if _, e := p.Wait(); e == nil {
			t.Fatal("cancel became success")
		}
	}
	replyDeadline := time.Now().Add(4 * time.Second)
	for {
		c.concurrent.mu.Lock()
		snapshot := make([]*concurrentHostCall, 0, len(c.concurrent.calls))
		for _, call := range c.concurrent.calls {
			snapshot = append(snapshot, call)
		}
		c.concurrent.mu.Unlock()
		if len(snapshot) != 32 {
			t.Fatal("physical slot removed before callback drain", len(snapshot))
		}
		received := 0
		for _, call := range snapshot {
			call.mu.Lock()
			if call.received {
				received++
			}
			call.mu.Unlock()
		}
		if received == 32 {
			break
		}
		if time.Now().After(replyDeadline) {
			t.Fatal("worker completion barrier missing", received)
		}
		time.Sleep(time.Millisecond)
	}

	c.concurrent.mu.Lock()
	held := len(c.concurrent.calls)
	c.concurrent.mu.Unlock()
	if held != 32 || running.Load() != 32 {
		t.Fatal("table lost physical ownership", held, running.Load())
	}
	p, e := c.StartCall(pCtx(t), "invoke", pRaw(parallelInput{Count: 1}), nil, CallOptions{CallerDeadlineOnly: true})
	if e == nil || p != nil || e.Error() != "plugin: concurrent_call_limit" {
		t.Fatal("canceled Wait fabricated slot permission", p, e)
	}
	once.Do(func() { close(release) })
	deadline := time.Now().Add(4 * time.Second)
	for {
		c.concurrent.mu.Lock()
		left := len(c.concurrent.calls)
		c.concurrent.mu.Unlock()
		if left == 0 && running.Load() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical drain did not finish", left, running.Load())
		}
		time.Sleep(time.Millisecond)
	}
	out, r := pCall(t, c, parallelInput{Count: 1}, func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`9`), nil
	})
	if out.HostCalls != 1 || len(r.Results) != 1 || string(r.Results[0]) != "9" {
		t.Fatal("fresh scope after real drain failed", out.HostCalls, r)
	}
	t.Log("32 canceled Wait results retained32 host slots until32 physical callbacks returned; 33rd denied; fresh callback then succeeds")
}
