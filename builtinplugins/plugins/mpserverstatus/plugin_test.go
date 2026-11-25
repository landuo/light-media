package mpserverstatus

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

const stubStatus = "Active connections: 12\nserver accepts handled requests\n1000 995 12345\nReading: 2 Writing: 3 Waiting: 7\n"

type gatedNetwork struct {
	entered, release chan struct{}
	once             sync.Once
	dns, tls         atomic.Int32
}

func (n *gatedNetwork) Status(ctx context.Context, _ string, _ time.Duration) (*Response, error) {
	n.once.Do(func() { close(n.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-n.release:
	}
	return &Response{Status: 200, Text: stubStatus, Protocol: 11}, nil
}
func (n *gatedNetwork) DNS(context.Context, string, int) ([]string, error) {
	n.dns.Add(1)
	return []string{"192.0.2.1"}, nil
}
func (n *gatedNetwork) TLS(context.Context, string, int, time.Duration) (Certificate, error) {
	n.tls.Add(1)
	return Certificate{Version: "TLSv1.3", NotAfter: "Nov 30 00:00:00 2026 GMT"}, nil
}
func sourceHost(t *testing.T, logs *[]sdk.LogEntryV1, mu *sync.Mutex) sdk.HostCaller {
	return func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if method != sdk.LogWriteCapability {
			return nil, errors.New("unexpected resource " + method)
		}
		var entry sdk.LogEntryV1
		if e := json.Unmarshal(raw, &entry); e != nil {
			return nil, e
		}
		mu.Lock()
		*logs = append(*logs, entry)
		mu.Unlock()
		return json.RawMessage(`null`), nil
	}
}
func TestPersistentInitNoopStopAndInflightCaches(t *testing.T) {
	p := NewPlugin()
	n := &gatedNetwork{entered: make(chan struct{}), release: make(chan struct{})}
	p.network = func(sdk.HostCaller) Network { return n }
	var logs []sdk.LogEntryV1
	var mu sync.Mutex
	host := sourceHost(t, &logs, &mu)
	call := func(method string, raw string) json.RawMessage {
		t.Helper()
		out, e := p.Handle(context.Background(), method, json.RawMessage(raw), host)
		if e != nil {
			t.Fatal(method, e)
		}
		return out
	}
	call("init", `{"enable":"false","onlyonce":true}`)
	if string(call("get_state", `null`)) != "true" {
		t.Fatal("Python truthy enable lost")
	}
	done := make(chan error, 1)
	go func() {
		out, e := p.Handle(context.Background(), "get_page", nil, host)
		if e == nil && !strings.Contains(string(out), "活跃连接") {
			e = errors.New("inflight source page lost")
		}
		done <- e
	}()
	<-n.entered
	call("stop_service", `null`)
	call("init", `{"enable":false}`)
	if p.machine.last() != nil || p.machine.cached(false) != nil {
		t.Fatal("init failed to clear caches")
	}
	close(n.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if p.machine.last() == nil || p.machine.cached(false) == nil || p.machine.cached(true) == nil {
		t.Fatal("source inflight request must repopulate reset state")
	}
	if n.dns.Load() != 1 || n.tls.Load() != 1 {
		t.Fatal("source details omitted")
	}
	if got := string(call("get_page", `null`)); !strings.Contains(got, "插件未启用") {
		t.Fatal(got)
	}
	// Source dashboard does not share the page's enable gate, and it skips
	// DNS/TLS. Stop is a literal no-op and does not clear the new sample.
	call("get_dashboard", `null`)
	if n.dns.Load() != 1 || n.tls.Load() != 1 || p.machine.last() == nil {
		t.Fatal("dashboard/stop policy changed")
	}
	before := p.machine.last()
	if _, e := p.Handle(context.Background(), "init", json.RawMessage(`[1]`), host); e == nil {
		t.Fatal("source nonmapping accepted")
	}
	if p.machine.last() != before {
		t.Fatal("failed source init mutated caches")
	}
	if e := Manifest().Validate(); e != nil {
		t.Fatal(e)
	}
	if len(Manifest().APIs) != 0 || len(Manifest().BroadcastHandlers) != 0 || len(Manifest().ChainHandlers) != 0 || len(Manifest().Dashboards) != 0 {
		t.Fatal("invented source registrations")
	}
	if len(logs) != 4 || logs[0].Message != "请求服务器状态 https://movie-pilot.org/status..." || logs[1].Level != sdk.LogInfo {
		t.Fatal("source business logs", logs)
	}
}
func TestScopeRevocationStopsNetworkDetails(t *testing.T) {
	p := NewPlugin()
	n := &gatedNetwork{entered: make(chan struct{}), release: make(chan struct{})}
	p.network = func(sdk.HostCaller) Network { return n }
	var logs []sdk.LogEntryV1
	var mu sync.Mutex
	host := sourceHost(t, &logs, &mu)
	_, _ = p.Handle(context.Background(), "init", json.RawMessage(`{"enable":true}`), host)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := p.Handle(ctx, "get_page", nil, host); done <- e }()
	<-n.entered
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if n.dns.Load() != 0 || n.tls.Load() != 0 || p.machine.last() != nil {
		t.Fatal("revoked call continued")
	}
	failing := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("owned scope rejection")
	}
	if _, e := p.Handle(context.Background(), "get_dashboard", nil, failing); e == nil || !strings.Contains(e.Error(), "scope rejection") {
		t.Fatal("guard error swallowed", e)
	}
}
func TestDynamicFormAndEmptySourceMethods(t *testing.T) {
	p := NewPlugin()
	for _, raw := range []string{`null`, `false`, `0`, `[]`, `""`} {
		if _, e := p.Handle(context.Background(), "init", json.RawMessage(raw), nil); e != nil {
			t.Fatal(e)
		}
	}
	_, _ = p.Handle(context.Background(), "init", json.RawMessage(`{"enable":[1]}`), nil)
	out, e := p.Handle(context.Background(), "get_form", nil, nil)
	if e != nil || !strings.HasSuffix(string(out), `{"enable":true}]`) {
		t.Fatal(string(out), e)
	}
	for _, method := range []string{"get_command", "get_api"} {
		out, e = p.Handle(context.Background(), method, nil, nil)
		if e != nil || string(out) != "[]" {
			t.Fatal(method, string(out), e)
		}
	}
	for _, method := range []string{"get_service", "stop_service", "close"} {
		out, e = p.Handle(context.Background(), method, nil, nil)
		if e != nil || string(out) != "null" {
			t.Fatal(method, string(out), e)
		}
	}
}
