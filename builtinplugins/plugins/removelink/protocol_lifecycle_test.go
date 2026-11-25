package removelink

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	plugins "light-media/builtinplugins/internal/sdk"
)

func TestActualWorkerColdNoReplayAndSeparateCloneState(t *testing.T) {
	source := startProtocolIdentity(t, false, "Source", 1)
	a, links := source.links(70)
	source.init(true)
	_ = os.Remove(a)
	source.call("test_event", map[string]any{"kind": "deleted", "path": a, "at": 1760000100})
	clone := startProtocolIdentity(t, false, "Clone", 1)
	ca, clinks := clone.links(70)
	clone.init(false)
	if e := source.client.CloseTransport(source.ctx); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(ca); e != nil {
		t.Fatal(e)
	}
	clone.call("test_event", map[string]any{"kind": "deleted", "path": ca, "at": 1760000100})
	for _, p := range links {
		if _, e := os.Stat(p); e != nil {
			t.Fatal("cold or clone replayed source queue", p, e)
		}
	}
	for _, p := range clinks {
		if _, e := os.Stat(p); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("clone failed independent work", p, e)
		}
	}
	cold := startProtocolIdentity(t, false, "Source", 2)
	cold.root = source.root
	cold.init(true)
	out := cold.call("test_snapshot", nil)
	var snapshot struct {
		Queue []Task
		State []Tracked
	}
	if e := json.Unmarshal(out.Result, &snapshot); e != nil {
		t.Fatal(e)
	}
	if len(snapshot.Queue) != 0 || len(snapshot.State) != 70 {
		t.Fatal("cold replayed missing event/persistent queue", string(out.Result))
	}
	cold.mu.Lock()
	effects := len(cold.trace)
	cold.mu.Unlock()
	if effects != 0 {
		t.Fatal("cold start wrote", effects)
	}
	t.Log("actual SDK cold process and different instance identities; not platform clone/install acceptance")
}

func TestActualWorkerProtectedHomePathUsesProcessHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	f := startProtocol(t)
	f.root = home
	a, links := f.links(2)
	f.call("init", map[string]any{"enabled": true, "monitor_dirs": filepath.Dir(a), "delayed_deletion": false, "exclude_dirs": "~/m/linked-000.mkv"})
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	f.call("test_event", map[string]any{"kind": "deleted", "path": a, "at": 1760000100})
	if _, e := os.Stat(links[0]); e != nil {
		t.Fatal("expanduser protected link removed", e)
	}
	if _, e := os.Stat(links[1]); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("unprotected same-inode link retained", e)
	}
}
func TestActualWorkerActiveReloadPublishesNewConfigBeforeObserverJoin(t *testing.T) {
	rawSource, e := os.ReadFile("reference/config-concurrency-source312.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Records []struct {
			Remaining []string         `json:"remaining_links"`
			Deleted   []string         `json:"deleted_links"`
			Trace     []map[string]any `json:"trace"`
		}
	}
	if json.Unmarshal(rawSource, &source) != nil || len(source.Records) != 1 {
		t.Fatal("invalid executed3.12 source concurrency evidence")
	}
	expected := source.Records[0]
	f := startProtocol(t)
	a, links := f.links(len(expected.Remaining) + len(expected.Deleted))
	f.init(false)
	_ = os.Remove(a)
	f.mu.Lock()
	f.block = plugins.StorageDeleteCapability
	f.mu.Unlock()
	deleted := make(chan error, 1)
	raw, _ := json.Marshal(map[string]any{"kind": "deleted", "path": a, "at": 1760000100})
	go func() {
		_, e := f.client.CallWithOptions(f.ctx, "test_event", raw, f.phase("owner:active-delete"), plugins.CallOptions{CallerDeadlineOnly: true})
		deleted <- e
	}()
	select {
	case <-f.entered:
	case <-f.ctx.Done():
		t.Fatal("write gate not reached")
	}
	reloaded := make(chan error, 1)
	config, _ := json.Marshal(map[string]any{"enabled": false, "monitor_dirs": filepath.Dir(a), "exclude_dirs": filepath.Dir(a), "delayed_deletion": false, "delete_history": false, "delete_torrents": false, "notify": false})
	go func() {
		_, e := f.client.CallWithOptions(f.ctx, "init", config, f.phase("owner:active-reload"), plugins.CallOptions{CallerDeadlineOnly: true})
		reloaded <- e
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		out := f.call("get_state", nil)
		if string(out.Result) == "false" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new config serialized behind old cleanup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case e := <-reloaded:
		t.Fatal("reload returned before old event owner joined", e)
	default:
	}
	close(f.release)
	for _, ch := range []chan error{deleted, reloaded} {
		select {
		case e := <-ch:
			if e != nil {
				t.Fatal(e)
			}
		case <-f.ctx.Done():
			t.Fatal("active reload did not join")
		}
	}
	remaining := 0
	for _, p := range links {
		if _, e := os.Stat(p); e == nil {
			remaining++
		}
	}
	if remaining != len(expected.Remaining) {
		t.Fatal("new source exclusions did not affect remaining links", remaining)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	events, history, notices := 0, 0, 0
	for _, x := range f.trace {
		switch x["op"] {
		case "event":
			events++
		case "history":
			history++
		case "notify":
			notices++
		}
	}
	wantEvents, wantHistory, wantNotices := 0, 0, 0
	for _, x := range expected.Trace {
		switch x["op"] {
		case "event":
			wantEvents++
		case "history":
			wantHistory++
		case "notify":
			wantNotices++
		}
	}
	if events != wantEvents || history != wantHistory || notices != wantNotices {
		t.Fatal("old cleanup flags used after live reload", events, history, notices)
	}
}
