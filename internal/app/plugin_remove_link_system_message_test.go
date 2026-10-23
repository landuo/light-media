package app

import (
	"context"
	"testing"
	"time"
)

func TestRemoveLinkInitSystemMessageCancellationAndClosedQueue(t *testing.T) {
	for _, state := range []string{"cancelled", "closed", "live"} {
		t.Run(state, func(t *testing.T) {
			// Pure producer test; no DB, runtime owners or filesystem effects.
			a := &App{}
			o := &pluginRemoveLinkOwner{a: a}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if state == "cancelled" {
				cancel()
			}
			if state == "closed" {
				a.systemMessages.close()
			}
			notice := Notification{Title: "媒体文件清理", Message: "owned fixture"}
			o.publishInitSystemMessage(ctx, notice)
			raw := a.systemMessages.get("")
			if (len(raw) != 0) != (state == "live") {
				t.Fatalf("state=%s raw=%s", state, raw)
			}
		})
	}
}

func TestRemoveLinkInitSystemMessageCancelledWhileQueueBusy(t *testing.T) {
	a := &App{}
	o := &pluginRemoveLinkOwner{a: a}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.systemMessages.mu.Lock()
	started, done := make(chan struct{}), make(chan struct{})
	go func() {
		close(started)
		o.publishInitSystemMessage(ctx, Notification{Title: "媒体文件清理", Message: "owned fixture"})
		close(done)
	}()
	<-started
	cancel()
	a.systemMessages.mu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled producer blocked")
	}
	if raw := a.systemMessages.get(""); len(raw) != 0 {
		t.Fatalf("cancelled producer leaked: %s", raw)
	}
}
