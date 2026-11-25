package iyuuautoseed

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func metadataFixturePlugin() *Plugin {
	p := NewPlugin()
	r := p.current()
	r.core.runMu.Lock()
	defer r.core.runMu.Unlock()
	r.core.config.values["enabled"] = true
	r.core.config.values["cron"] = "0 0 * * *"
	r.core.config.values["token"] = "owned-metadata-token"
	r.core.config.values["downloaders"] = []any{"owned"}
	r.publishMetadata()
	return p
}
func TestPublicMetadataDoesNotWaitForNetworkPass(t *testing.T) {
	for _, method := range []string{"get_state", "get_service"} {
		t.Run(method, func(t *testing.T) {
			p := metadataFixturePlugin()
			r := p.current()
			// The real network pass owns this mutex for the entire IYUU operation.
			r.core.runMu.Lock()
			defer r.core.runMu.Unlock()
			type result struct {
				raw json.RawMessage
				err error
			}
			done := make(chan result, 1)
			go func() { raw, err := p.Handle(context.Background(), method, nil, nil); done <- result{raw, err} }()
			select {
			case out := <-done:
				if out.err != nil {
					t.Fatal(out.err)
				}
				if method == "get_state" && string(out.raw) != "true" {
					t.Fatalf("wrong configured state: %s", out.raw)
				}
				if method == "get_service" {
					var jobs []any
					if json.Unmarshal(out.raw, &jobs) != nil || len(jobs) != 1 {
						t.Fatalf("wrong jobs: %s", out.raw)
					}
				}
			case <-time.After(300 * time.Millisecond):
				t.Fatal("public metadata blocked behind a network pass")
			}
		})
	}
}
func TestPublicMetadataUpdatesAndReturnsIndependentBytes(t *testing.T) {
	p := metadataFixturePlugin()
	first, err := p.Handle(context.Background(), "get_service", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := append([]byte(nil), first...)
	for i := range first {
		first[i] = 'x'
	}
	next, err := p.Handle(context.Background(), "get_service", nil, nil)
	if err != nil || string(next) != string(expected) {
		t.Fatal("caller mutated the published declarations")
	}
	r := p.current()
	r.core.runMu.Lock()
	r.core.config.values["enabled"] = false
	r.publishMetadata()
	r.core.runMu.Unlock()
	state, _ := p.Handle(context.Background(), "get_state", nil, nil)
	services, _ := p.Handle(context.Background(), "get_service", nil, nil)
	if string(state) != "false" || string(services) != "[]" {
		t.Fatalf("snapshot did not reflect disabled config: %s %s", state, services)
	}
	r.core.runMu.Lock()
	r.core.config.values["enabled"] = true
	r.core.config.values["cron"] = "invalid-cron"
	r.publishMetadata()
	r.core.runMu.Unlock()
	if _, err = p.Handle(context.Background(), "get_service", nil, nil); err == nil {
		t.Fatal("invalid service declaration was silently accepted")
	}
}
func TestPublicMetadataConcurrentCacheMutation(t *testing.T) {
	p := metadataFixturePlugin()
	r := p.current()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			r.core.runMu.Lock()
			r.core.config.values["success_caches"] = []any{i}
			r.core.config.values["enabled"] = i%2 == 0
			r.publishMetadata()
			r.core.runMu.Unlock()
		}
	}()
	for i := 0; i < 100; i++ {
		for _, m := range []string{"get_state", "get_service"} {
			if _, err := p.Handle(context.Background(), m, nil, nil); err != nil {
				t.Error(err)
			}
		}
	}
	wg.Wait()
}
