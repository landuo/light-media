package removelink

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	plugins "light-media/builtinplugins/internal/sdk"
)

func TestActualWorkerFreshStopFlushesUnenteredTaskWithoutReplayingUnknownNotice(t *testing.T) {
	f := startProtocol(t)
	a, links := f.links(1)
	b := filepath.Join(filepath.Dir(a), "second.mkv")
	linked := filepath.Join(filepath.Dir(a), "second-linked.mkv")
	if e := os.WriteFile(b, []byte("owned independent inode"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(b, linked); e != nil {
		t.Fatal(e)
	}
	f.init(true)
	for i, p := range []string{a, b} {
		if e := os.Remove(p); e != nil {
			t.Fatal(e)
		}
		f.call("test_event", map[string]any{"kind": "deleted", "path": p, "at": 1760000100 + i*10})
	}
	f.mu.Lock()
	f.block = plugins.PostMessageCapability
	f.blockAfter = true
	f.mu.Unlock()
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() {
		_, e := f.client.CallWithOptions(ctx, "test_tick", json.RawMessage(`1760200000`), f.phase("owner:notice-unknown"), plugins.CallOptions{CallerDeadlineOnly: true})
		done <- e
	}()
	select {
	case <-f.entered:
	case <-f.ctx.Done():
		t.Fatal("unknown notice gate not reached")
	}
	cancel()
	select {
	case <-f.canceled:
	case <-f.ctx.Done():
		t.Fatal("actual notice host authority not canceled")
	}
	close(f.release)
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("unknown notice became success")
		}
	case <-f.ctx.Done():
		t.Fatal("unknown owner did not finish")
	}
	out := f.call("test_snapshot", nil)
	var snapshot struct{ Queue []Task }
	_ = json.Unmarshal(out.Result, &snapshot)
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].Processed {
		t.Fatal("unentered task lost", string(out.Result))
	}
	if _, e := os.Stat(linked); e != nil {
		t.Fatal("later task executed after uncertainty", e)
	}
	f.call("stop_service", nil)
	for _, p := range []string{links[0], linked} {
		if _, e := os.Stat(p); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("fresh stop did not finish unentered task", p, e)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	notices, writes := 0, 0
	for _, x := range f.trace {
		if x["op"] == "notify" {
			notices++
		}
		if x["op"] == "delete" {
			writes++
		}
	}
	if notices != 2 || writes != 2 {
		t.Fatal("unknown completed task replayed or untouched task dropped", notices, writes)
	}
}
