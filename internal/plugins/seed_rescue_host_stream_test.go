package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSeedRescueParallelHostStream(t *testing.T) {
	const (
		child       = "seed-rescue-parallel-stream-child"
		detail      = "seed_rescue.detail"
		release     = `"release-first"`
		callbacks   = 300
		maxMessage  = 64 << 10
		maxPhysical = 8 << 10
	)
	manifest := Manifest{
		SchemaVersion: 1, PluginID: "SeedRescue", Name: "SeedRescue stream fixture",
		Version: "1.6.3", Runtime: "go-executable", Entry: "fixture",
		HostAPIMin: 1, HostAPIMax: 1, ManagedPublicServices: true, ManagedPrivateScheduler: true,
		Methods: []string{"scan", "stop_service", "stop"}, Capabilities: []string{detail, BackgroundSubmitCapability},
	}
	if os.Args[len(os.Args)-1] == child {
		handle := func(ctx context.Context, method string, _ json.RawMessage, host HostCaller) (json.RawMessage, error) {
			if method != "scan" {
				return nil, nil
			}
			oversize := pRaw(strings.Repeat("a", maxMessage))
			if _, err := host(ctx, detail, oversize); err == nil {
				return nil, errors.New("oversize message was published")
			}

			var group sync.WaitGroup
			var releaseOnce sync.Once
			var mismatches atomic.Int32
			for i := range callbacks {
				group.Go(func() {
					payload := json.RawMessage(strconv.Itoa(i))
					if i == callbacks-1 {
						payload = pRaw(strings.Repeat("a", 2*maxPhysical))
					}
					out, err := host(ctx, detail, payload)
					if err != nil || !bytes.Equal(out, payload) {
						mismatches.Add(1)
						return
					}
					// The first published callback can return only after another
					// caller has already received a reply and sent this marker.
					releaseOnce.Do(func() {
						out, err := host(ctx, detail, json.RawMessage(release))
						if err != nil || string(out) != release {
							mismatches.Add(1)
						}
					})
				})
			}
			group.Wait()
			return pRaw(mismatches.Load()), nil
		}
		if err := ServeConcurrentWithParallelHostCalls(context.Background(), os.Stdin, os.Stdout, manifest, handle); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}

	proposal := concurrentProposal{
		Version: 1, MaxCalls: maxConcurrentCalls, MaxHostCalls: 256,
		MaxParallelHostCalls: maxParallelHostCalls, SequentialHostStream: true,
	}
	for _, method := range []string{"scan", "scan_background", "http", "get_service"} {
		if !proposal.streamsHostCalls(method) {
			t.Fatalf("missing streamed phase %s", method)
		}
	}
	other := manifest
	other.PluginID = "ParallelFixture"
	if proposal.streamsHostCalls("init") || proposal.streamsHostCalls("stop_service") || !proposal.valid(manifest) || proposal.valid(other) {
		t.Fatal("combined proposal admitted a foreign plugin or an unrelated phase")
	}

	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner := Runner{SequentialHostStream: true, MaxHostCalls: 256, MaxFrameBytes: maxPhysical, MaxMessageBytes: maxMessage}
	binding := SessionBinding{Identity: Identity{PluginID: manifest.PluginID, InstanceID: manifest.PluginID, Version: manifest.Version}, Generation: 1}
	client, err := runner.Start(context.Background(),
		Command{Path: binary, Args: []string{"-test.run=^TestSeedRescueParallelHostStream$", "--", child}}, manifest, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.Stop(ctx); err != nil {
			t.Error("worker shutdown", err)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var reads atomic.Int32
	releaseFirst := make(chan struct{})
	result, err := client.Call(ctx, "scan", nil, func(ctx context.Context, _ Identity, method string, payload json.RawMessage) (json.RawMessage, error) {
		if method != detail {
			return nil, errors.New("unexpected callback")
		}
		if reads.Add(1) == 1 {
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if string(payload) == release {
			close(releaseFirst)
		}
		return payload, nil
	})
	if err != nil || string(result.Result) != "0" || reads.Load() != callbacks+1 || result.HostCalls != callbacks+1 {
		t.Fatalf("combined stream lost or serialized calls: result=%s reads=%d callbacks=%d err=%v", result.Result, reads.Load(), result.HostCalls, err)
	}
	if result.SentChunks == 0 || result.ReadChunks == 0 {
		t.Fatal("interleaved large callback did not exercise chunked transport in both directions")
	}
}
