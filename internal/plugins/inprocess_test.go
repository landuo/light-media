package plugins

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

func TestInProcessShutdownAdmissionAndFinalJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		release := make(chan struct{})
		var stops atomic.Int32
		manifest := privateSDKManifest(true, false)
		binding := SessionBinding{Identity: Identity{PluginID: manifest.PluginID, InstanceID: manifest.PluginID, Version: manifest.Version}, Generation: 1}
		session, err := NewInProcess(binding, InProcessOptions{Manifest: manifest, Concurrent: true}, func(_ context.Context, method string, _ json.RawMessage, _ HostCaller) (json.RawMessage, error) {
			switch method {
			case "invoke":
				<-release
			case "stop":
				stops.Add(1)
			}
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		pending, err := session.StartCall(ctx, "invoke", nil, nil, CallOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := session.BeginShutdown(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := session.StartCall(ctx, "invoke", nil, nil, CallOptions{}); err == nil {
			t.Fatal("ordinary call admitted after shutdown")
		}
		if _, err := session.CallCleanup(ctx, "close", nil, nil); err != nil {
			t.Fatalf("reserved cleanup during shutdown: %v", err)
		}

		done := make(chan error, 2)
		go func() { done <- session.Stop(ctx) }()
		synctest.Wait()
		go func() { done <- session.CloseTransport(ctx) }()
		synctest.Wait()
		if got := stops.Load(); got != 1 {
			t.Fatalf("stop handler called %d times, want 1", got)
		}
		if _, err := session.CallCleanup(ctx, "close", nil, nil); err == nil {
			t.Fatal("cleanup admitted after final stop")
		}
		select {
		case <-done:
			t.Fatal("final stop returned before the admitted handler finished")
		default:
		}
		close(release)
		if _, err := pending.Wait(); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
	})
}
