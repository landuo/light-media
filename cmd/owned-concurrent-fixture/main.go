// owned-concurrent-fixture is an independent executable using the public SDK.
// It has no network, filesystem, or production business integration.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"light-media/internal/plugins"
)

type input struct {
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value"`
}
type object struct {
	calls           atomic.Int64
	uncooperative   atomic.Bool
	mu              sync.Mutex
	retained        plugins.HostCaller
	cleanupRetained plugins.HostCaller
	cleanupHost     bool
	failClose       bool
	release         chan struct{}
	releaseOnce     sync.Once
}

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "ConcurrentFixture", Name: "Concurrent SDK fixture", Version: "1.0.0", Runtime: "go-executable", Entry: "worker", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"invoke", "close", "stop_service", "stop"}, Capabilities: []string{"test.host"}}
}
func (o *object) handle(ctx context.Context, method string, payload json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	if method == "close" {
		o.mu.Lock()
		old := o.retained
		o.cleanupRetained = host
		o.mu.Unlock()
		oldCode := "none"
		if old != nil {
			_, oldErr := old(ctx, "test.host", json.RawMessage(`null`))
			var local *plugins.Error
			if errors.As(oldErr, &local) {
				oldCode = local.Code
			} else {
				oldCode = "UNEXPECTED_AUTHORITY"
			}
		}
		raw, _ := json.Marshal(map[string]string{"old_callback": oldCode})
		if _, err := host(ctx, "test.close", raw); err != nil {
			return nil, err
		}
		o.releaseOnce.Do(func() { close(o.release) })
		if o.failClose {
			return nil, errors.New("owned close failure")
		}
		return json.RawMessage(`null`), nil
	}
	if method == "stop_service" {
		o.mu.Lock()
		old := o.cleanupRetained
		o.mu.Unlock()
		oldCode := "none"
		if old != nil {
			_, oldErr := old(ctx, "test.stop_service", json.RawMessage(`null`))
			var local *plugins.Error
			if errors.As(oldErr, &local) {
				oldCode = local.Code
			} else {
				oldCode = "UNEXPECTED_AUTHORITY"
			}
		}
		raw, _ := json.Marshal(map[string]string{"old_callback": oldCode})
		return host(ctx, "test.stop_service", raw)
	}
	if method == "stop" {
		if o.cleanupHost {
			return host(ctx, "test.cleanup", json.RawMessage(`null`))
		}
		return json.RawMessage(`null`), nil
	}
	n := o.calls.Add(1)
	var in input
	if json.Unmarshal(payload, &in) != nil {
		return nil, errors.New("fixture input")
	}
	switch in.Op {
	case "host":
		return host(ctx, "test.host", in.Value)
	case "host_timeout":
		short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		defer cancel()
		return host(short, "test.host", in.Value)
	case "remember":
		o.mu.Lock()
		o.retained = host
		o.mu.Unlock()
		return host(ctx, "test.host", in.Value)
	case "reuse":
		o.mu.Lock()
		old := o.retained
		o.mu.Unlock()
		if old == nil {
			return nil, errors.New("no retained callback")
		}
		_, err := old(ctx, "test.host", in.Value)
		var local *plugins.Error
		if errors.As(err, &local) {
			return json.Marshal(local.Code)
		}
		return json.Marshal("UNEXPECTED_AUTHORITY")
	case "wait":
		if _, err := host(ctx, "test.host", in.Value); err != nil {
			return nil, err
		}
		<-ctx.Done()
		return nil, ctx.Err()
	case "wait_cleanup":
		o.mu.Lock()
		o.retained = host
		o.mu.Unlock()
		// The host-side barrier now guarantees this handler cannot exit until
		// close releases it, even if its preceding HostCaller is canceled.
		_, _ = host(ctx, "test.host", in.Value)
		<-o.release
		return json.RawMessage(`null`), nil
	case "uncooperative":
		if _, err := host(ctx, "test.host", in.Value); err != nil {
			return nil, err
		}
		o.uncooperative.Store(true)
		time.Sleep(60 * time.Second)
		return json.RawMessage(`null`), nil
	case "fail":
		return nil, errors.New("private business error")
	case "state":
		return json.Marshal(map[string]any{"pid": os.Getpid(), "calls": n, "uncooperative": o.uncooperative.Load()})
	default:
		return nil, errors.New("unknown fixture operation")
	}
}
func main() {
	o := &object{release: make(chan struct{})}
	legacy := false
	for _, arg := range os.Args[1:] {
		if arg == "--cleanup-host" {
			o.cleanupHost = true
		}
		if arg == "--fail-close" {
			o.failClose = true
		}
		if arg == "--legacy" {
			legacy = true
		}
	}
	serve := plugins.ServeConcurrent
	if legacy {
		serve = plugins.Serve
	}
	if err := serve(context.Background(), os.Stdin, os.Stdout, manifest(), o.handle); err != nil {
		os.Exit(1)
	}
}
