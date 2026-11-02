package app

import (
	"context"
	"sync"
)

// Only trusted channel/user/source reach these named native hooks. Text, event
// names, arbitrary method names and plugin-supplied executable data do not.
type originalSystemCommandInput struct {
	Channel any
	Source  any
	User    any
}

type originalSystemCommandHandlers struct {
	version func(context.Context, originalSystemCommandInput) error
	finish  func(context.Context) error // Private normal/safe successful-startup completion only.
	// prepared is an intent AFTER the actual restart effects have completed.
	// It is not a successful process restart and must not be persisted/replayed.
	restart func(context.Context, originalSystemCommandInput) (prepared bool, err error)
}

type originalSystemCommandRuntime struct {
	mu              sync.Mutex
	attached        bool
	closed          bool
	lifetime        context.Context
	cancel          context.CancelFunc
	handlers        originalSystemCommandHandlers
	intent          chan struct{}
	intentCommitted bool
	listenerReady   chan struct{}
	listenerMarked  bool
	finishClaimed   bool
}

// This is a private construction seam, not a public HTTP, SDK or plugin API.
// The trusted self-executable child controller alone passes processAuthority=true.
// It owns one generation and waits for actual child exit before replacement.
// A missing version hook remains unavailable; no fake provider is installed.
func (a *App) attachOriginalSystemCommandHandlers(parent context.Context, handlers originalSystemCommandHandlers, processAuthority bool) error {
	if parent == nil || (handlers.version == nil && handlers.restart == nil) {
		return originalSystemAdapterBoundary("incomplete system-command host attachment")
	}
	if err := parent.Err(); err != nil {
		return err
	}
	state := &a.pluginOriginalSystemCommands
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed || state.attached {
		return originalSystemAdapterBoundary("system-command host cannot be attached again")
	}
	state.lifetime, state.cancel = context.WithCancel(parent)
	state.handlers = handlers
	state.attached = true
	if handlers.finish != nil {
		state.listenerReady = make(chan struct{})
	}
	if processAuthority {
		state.intent = make(chan struct{}, 1)
	}
	return nil
}

func (a *App) originalSystemCommandAvailable(name string) bool {
	if name == "/clear_cache" {
		return true
	}
	state := &a.pluginOriginalSystemCommands
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.attached || state.closed || state.lifetime.Err() != nil {
		return false
	}
	switch name {
	case "/version":
		return state.handlers.version != nil
	case "/restart":
		return state.handlers.restart != nil
	default:
		return false
	}
}

// The plugin-command overlay is resolved before this function is reached.
// Its exact two names do not broaden the original clear_cache whitelist to
// other system APIs or media presets. Extra slash arguments are not forwarded.
func (a *App) dispatchOriginalSystemCommand(ctx context.Context, name string, input originalCommandEvent) (bool, error) {
	state := &a.pluginOriginalSystemCommands
	state.mu.Lock()
	if !state.attached || state.closed || state.lifetime.Err() != nil {
		state.mu.Unlock()
		return false, nil
	}
	handlers, lifetime := state.handlers, state.lifetime
	available := name == "/version" && handlers.version != nil || name == "/restart" && handlers.restart != nil
	state.mu.Unlock()
	if !available {
		return false, nil
	}
	if !a.admitOriginalBackground() {
		return false, nil
	}
	// The command survives request disconnect, but not its owning App lifetime.
	// Work and source-style error reporting finish before the admission token is
	// released. A controller may only observe the intent AFTER that release.
	prepared := func() bool {
		defer a.wg.Done()
		work, cancel := context.WithCancel(context.WithoutCancel(ctx))
		stop := context.AfterFunc(lifetime, cancel)
		defer cancel()
		defer stop()
		if err := lifetime.Err(); err != nil {
			cancel()
		}
		args := originalSystemCommandInput{Channel: input.Channel, Source: input.Source, User: input.User}
		requested := false
		err := work.Err()
		if err == nil {
			if name == "/version" {
				err = handlers.version(work, args)
			} else {
				requested, err = handlers.restart(work, args)
			}
		}
		if err == nil {
			err = work.Err()
		}
		if err != nil {
			a.reportOriginalPluginExecutionError(work, "system", "执行命令 "+name+" 出错", nil, err)
			return false
		}
		return requested
	}()
	if prepared {
		a.commitOriginalSystemRestartIntent()
	}
	return true, nil
}

func (a *App) commitOriginalSystemRestartIntent() {
	state := &a.pluginOriginalSystemCommands
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.attached || state.closed || state.lifetime.Err() != nil || state.intent == nil || state.intentCommitted {
		return
	}
	state.intentCommitted = true
	state.intent <- struct{}{}
}

func (a *App) originalSystemRestartIntents() <-chan struct{} {
	state := &a.pluginOriginalSystemCommands
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.intent
}

// Close seals the command lifetime before stopping sessions and waiting on wg.
// Never synchronously Close from a command holding its own admission token.
// A queued notification marker is NOT read here and never auto-restarts a host.
func (a *App) closeOriginalSystemCommands() {
	state := &a.pluginOriginalSystemCommands
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closed = true
	if state.cancel != nil {
		state.cancel()
	}
}
