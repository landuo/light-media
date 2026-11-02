package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestOriginalSystemNamedHooksAndRequestLifetime(t *testing.T) {
	a := newTestApp(t)
	if a.originalSystemCommandAvailable("/version") || a.originalSystemCommandAvailable("/restart") || !a.originalSystemCommandAvailable("/clear_cache") || a.originalSystemCommandAvailable("/transfer") {
		t.Fatal("unattached named whitelist")
	}
	var got originalSystemCommandInput
	err := a.attachOriginalSystemCommandHandlers(context.Background(), originalSystemCommandHandlers{restart: func(ctx context.Context, in originalSystemCommandInput) (bool, error) {
		if ctx.Err() != nil {
			t.Fatal("request disconnect reached accepted command")
		}
		got = in
		return true, nil
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if a.originalSystemCommandAvailable("/version") || !a.originalSystemCommandAvailable("/restart") {
		t.Fatal("missing provider fabricated")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in := originalCommandEvent{Text: "/restart ignored arguments", Channel: "Web", Source: "owned-source", User: "owned-user"}
	if accepted, err := a.dispatchOriginalSystemCommand(ctx, "/restart", in); err != nil || !accepted {
		t.Fatal(accepted, err)
	}
	if !reflect.DeepEqual(got, originalSystemCommandInput{Channel: "Web", Source: "owned-source", User: "owned-user"}) {
		t.Fatal(got)
	}
	if a.originalSystemRestartIntents() != nil {
		t.Fatal("unattached process authority appeared")
	}
	for _, name := range []string{"/version", "/transfer", "/shell", "restart"} {
		if accepted, err := a.dispatchOriginalSystemCommand(context.Background(), name, in); err != nil || accepted {
			t.Fatal(name, accepted, err)
		}
	}
}

func TestOriginalSystemIntentAfterCompletedWorkAndCoalesced(t *testing.T) {
	a := newTestApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	first := true
	err := a.attachOriginalSystemCommandHandlers(context.Background(), originalSystemCommandHandlers{restart: func(ctx context.Context, in originalSystemCommandInput) (bool, error) {
		if first {
			first = false
			close(entered)
			<-release
		}
		return true, nil
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := a.dispatchOriginalSystemCommand(context.Background(), "/restart", originalCommandEvent{})
		done <- err
	}()
	<-entered
	select {
	case <-a.originalSystemRestartIntents():
		t.Fatal("intent preceded completed command")
	default:
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.originalSystemRestartIntents():
	default:
		t.Fatal("completed intent missing")
	}
	if accepted, err := a.dispatchOriginalSystemCommand(context.Background(), "/restart", originalCommandEvent{}); err != nil || !accepted {
		t.Fatal(accepted, err)
	}
	select {
	case <-a.originalSystemRestartIntents():
		t.Fatal("duplicate generation intent")
	default:
	}
	select {
	case <-a.RestartRequested:
		t.Fatal("new intent entered legacy same-PID loop")
	default:
	}
	a.closeOriginalSystemCommands()
	if accepted, err := a.dispatchOriginalSystemCommand(context.Background(), "/restart", originalCommandEvent{}); err != nil || accepted {
		t.Fatal("closed command admitted", accepted, err)
	}
	if err = a.attachOriginalSystemCommandHandlers(context.Background(), originalSystemCommandHandlers{version: func(context.Context, originalSystemCommandInput) error { return nil }}, false); err == nil {
		t.Fatal("closed generation reattached")
	}
}

func TestOriginalSystemHandlerErrorAndLifetimeCancelCannotCommit(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		a := newTestApp(t)
		if err := a.attachOriginalSystemCommandHandlers(context.Background(), originalSystemCommandHandlers{restart: func(context.Context, originalSystemCommandInput) (bool, error) {
			return true, errors.New("owned failure")
		}}, true); err != nil {
			t.Fatal(err)
		}
		if accepted, err := a.dispatchOriginalSystemCommand(context.Background(), "/restart", originalCommandEvent{}); err != nil || !accepted {
			t.Fatal(accepted, err)
		}
		select {
		case <-a.originalSystemRestartIntents():
			t.Fatal("failure promoted to restart")
		default:
		}
	})
	t.Run("closing", func(t *testing.T) {
		a := newTestApp(t)
		entered, done := make(chan struct{}), make(chan error, 1)
		if err := a.attachOriginalSystemCommandHandlers(context.Background(), originalSystemCommandHandlers{restart: func(ctx context.Context, in originalSystemCommandInput) (bool, error) {
			close(entered)
			<-ctx.Done()
			return true, ctx.Err()
		}}, true); err != nil {
			t.Fatal(err)
		}
		go func() {
			_, err := a.dispatchOriginalSystemCommand(context.Background(), "/restart", originalCommandEvent{})
			done <- err
		}()
		<-entered
		a.closeOriginalSystemCommands()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		a.wg.Wait()
		select {
		case <-a.originalSystemRestartIntents():
			t.Fatal("stop lost to restart")
		default:
		}
	})
}
