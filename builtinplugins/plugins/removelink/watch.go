package removelink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	plugins "light-media/builtinplugins/internal/sdk"
)

type fileEvent struct {
	Kind, Path, Dest, Mode string
	Directory              bool
	Err                    error
}
type observer interface {
	Events() <-chan fileEvent
	Close()
}

// Native watchdog has one serial dispatcher per observer. The timer is a
// different dispatcher. Each active callback receives a real independent SDK
// caller; only this small live coordinator waits without retaining effects.
type observerLane struct {
	id                      uint64
	spec                    Watch
	observer                observer
	queue                   []fileEvent
	active, pending, closed bool
	token                   uint64
	event                   fileEvent
	pumpDone                chan struct{}
	closeOnce               sync.Once
}
type watchRuntime struct {
	id                            uint64
	mu                            sync.Mutex
	wake, stop, done              chan struct{}
	once, finishOne               sync.Once
	closed, pumpsClosed, started  bool
	lanes                         []*observerLane
	pumps                         sync.WaitGroup
	calls                         int // reserved/accepted callback bodies, including the coordinator
	activeObservers               int
	nextLane                      int
	controlToken                  uint64
	controlPending, controlActive bool
	timerToken                    uint64
	timerPending, timerActive     bool
	life                          context.Context
	cancel                        context.CancelFunc
}

// Bound actual effect-bearing callbacks, not configured roots or native pumps.
// A live callback normally has its owner plus one current continuation. Leave
// headroom for the independent timer, coordinator, init/stop and IPC handoffs.
// The SDK remains the authority for its separate 32-call admission/drain limit.
const maxObserverCallbacks = 6

func newWatchRuntime(id uint64) *watchRuntime {
	ctx, cancel := context.WithCancel(context.Background())
	return &watchRuntime{id: id, wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}), life: ctx, cancel: cancel}
}
func (w *watchRuntime) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *watchRuntime) finish() { w.finishOne.Do(func() { close(w.done) }) }
func (w *watchRuntime) maybeFinishLocked() {
	if w.closed && w.pumpsClosed && w.calls == 0 {
		w.finish()
	}
}
func (w *watchRuntime) push(lane *observerLane, event fileEvent) {
	w.mu.Lock()
	if !w.closed && !lane.closed {
		lane.queue = append(lane.queue, event)
		w.signal()
	}
	w.mu.Unlock()
}
func (lane *observerLane) close() {
	lane.closeOnce.Do(func() { lane.observer.Close(); <-lane.pumpDone })
}
func (w *watchRuntime) close() {
	w.once.Do(func() {
		w.mu.Lock()
		w.closed = true
		close(w.stop)
		lanes := append([]*observerLane{}, w.lanes...)
		w.mu.Unlock()
		for _, lane := range lanes {
			lane.close()
		}
		w.pumps.Wait()
		w.mu.Lock()
		w.pumpsClosed = true
		w.maybeFinishLocked()
		w.mu.Unlock()
	})
}
func (w *watchRuntime) abort() { w.cancel(); w.close() }
func (p *Plugin) addObserver(w *watchRuntime, spec Watch, ops *sdkOps) error {
	native, err := p.newObserver(spec)
	if err != nil {
		return err
	}
	lane := &observerLane{spec: spec, observer: native, pumpDone: make(chan struct{})}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		native.Close()
		return context.Canceled
	}
	lane.id = uint64(len(w.lanes) + 1)
	w.lanes = append(w.lanes, lane)
	w.pumps.Add(1)
	first := !w.started
	if first {
		w.started = true
	}
	w.mu.Unlock()
	go func() {
		defer w.pumps.Done()
		defer close(lane.pumpDone)
		for event := range native.Events() {
			event.Mode = spec.Mode
			w.push(lane, event)
		}
	}()
	// observer.start precedes the initial bulk state scan. A fresh real callback
	// can enter immediately; it is not hidden behind the whole initializer.
	if first {
		if err := p.submitWatch(context.Background(), ops.call, w); err != nil {
			w.abort()
			return err
		}
	}
	return nil
}
func (p *Plugin) stopObservers(ctx context.Context) error {
	p.mu.Lock()
	w := p.watch
	p.mu.Unlock()
	if w == nil {
		return nil
	}
	w.close()
	select {
	case <-w.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	w.cancel()
	p.mu.Lock()
	if p.watch == w {
		p.watch = nil
	}
	p.mu.Unlock()
	return nil
}
func (p *Plugin) runtime(raw json.RawMessage) (*watchRuntime, uint64, uint64, error) {
	var in struct{ Generation, Lane, Token uint64 }
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, 0, 0, err
	}
	p.mu.Lock()
	w := p.watch
	p.mu.Unlock()
	if w == nil || w.id != in.Generation {
		return nil, 0, 0, nil
	}
	return w, in.Lane, in.Token, nil
}
func (p *Plugin) submitWatch(ctx context.Context, host plugins.HostCaller, w *watchRuntime) error {
	w.mu.Lock()
	if w.closed || w.controlPending || w.controlActive {
		w.mu.Unlock()
		return nil
	}
	w.controlToken++
	token := w.controlToken
	w.controlPending = true
	w.calls++
	w.mu.Unlock()
	err := (plugins.Manager{Host: host}).Submit(ctx, "private_watch", map[string]uint64{"generation": w.id, "token": token})
	if err != nil {
		w.mu.Lock()
		if w.controlToken == token && w.controlPending {
			w.controlPending = false
			w.calls--
			w.maybeFinishLocked()
		}
		w.mu.Unlock()
	}
	return err
}
func (w *watchRuntime) callbackContext(ctx context.Context) (context.Context, func()) {
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(w.life, cancel)
	return call, func() { stop(); cancel() }
}
func (p *Plugin) runWatch(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) (err error) {
	w, _, token, err := p.runtime(raw)
	if err != nil || w == nil {
		return err
	}
	w.mu.Lock()
	if !w.controlPending || token != w.controlToken {
		w.mu.Unlock()
		return nil
	}
	w.controlPending = false
	w.controlActive = true
	w.mu.Unlock()
	ctx, closeCall := w.callbackContext(ctx)
	defer closeCall()
	defer func() {
		if err != nil {
			w.abort()
		}
		w.mu.Lock()
		if token == w.controlToken {
			w.controlActive = false
		}
		w.calls--
		w.maybeFinishLocked()
		w.mu.Unlock()
	}()
	for {
		w.mu.Lock()
		closed := w.closed
		w.mu.Unlock()
		if closed {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		due := p.timerDue()
		w.mu.Lock()
		ready := []*observerLane{}
		start := w.nextLane
		for offset := 0; offset < len(w.lanes); offset++ {
			if w.activeObservers >= maxObserverCallbacks {
				break
			}
			index := (start + offset) % len(w.lanes)
			lane := w.lanes[index]
			if !lane.closed && !lane.active && len(lane.queue) > 0 {
				lane.event = lane.queue[0]
				lane.queue[0] = fileEvent{}
				lane.queue = lane.queue[1:]
				lane.token++
				lane.active, lane.pending = true, true
				w.activeObservers++
				w.calls++
				ready = append(ready, lane)
				w.nextLane = (index + 1) % len(w.lanes)
			}
		}
		timer := due != nil && !due.After(p.machine.Now()) && !w.timerActive
		timerToken := uint64(0)
		if timer {
			w.timerToken++
			timerToken = w.timerToken
			w.timerActive, w.timerPending = true, true
			w.calls++
		}
		timerActive := w.timerActive
		w.mu.Unlock()
		for readyIndex, lane := range ready {
			if err = (plugins.Manager{Host: host}).Submit(ctx, "private_observer", map[string]uint64{"generation": w.id, "lane": lane.id, "token": lane.token}); err != nil {
				w.mu.Lock()
				if lane.pending {
					lane.pending, lane.active = false, false
					w.activeObservers--
					w.calls--
				}
				for _, notSent := range ready[readyIndex+1:] {
					if notSent != lane && notSent.pending {
						notSent.pending, notSent.active = false, false
						w.activeObservers--
						w.calls--
					}
				}
				if timer && w.timerPending {
					w.timerPending, w.timerActive = false, false
					w.calls--
				}
				w.mu.Unlock()
				return err
			}
		}
		if timer {
			if err = (plugins.Manager{Host: host}).Submit(ctx, "private_timer", map[string]uint64{"generation": w.id, "token": timerToken}); err != nil {
				w.mu.Lock()
				if w.timerPending && timerToken == w.timerToken {
					w.timerPending, w.timerActive = false, false
					w.calls--
				}
				w.mu.Unlock()
				return err
			}
		}
		if len(ready) > 0 || timer {
			// At most7 actual submits plus this handoff. The next coordination callback
			// has a fresh64 budget even after arbitrarily many native events/ticks.
			w.mu.Lock()
			if token == w.controlToken {
				w.controlActive = false
			}
			w.mu.Unlock()
			return p.submitWatch(ctx, host, w)
		}
		var alarm <-chan time.Time
		var clock *time.Timer
		if due != nil && !timerActive {
			clock = time.NewTimer(due.Sub(p.machine.Now()))
			alarm = clock.C
		}
		select {
		case <-w.wake:
		case <-alarm:
		case <-w.stop:
			if clock != nil {
				clock.Stop()
			}
			return nil
		case <-ctx.Done():
			if clock != nil {
				clock.Stop()
			}
			return ctx.Err()
		}
		if clock != nil {
			clock.Stop()
		}
	}
}
func (p *Plugin) runObserver(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) (err error) {
	w, id, token, err := p.runtime(raw)
	if err != nil || w == nil {
		return err
	}
	w.mu.Lock()
	if id == 0 || id > uint64(len(w.lanes)) {
		w.mu.Unlock()
		return nil
	}
	lane := w.lanes[id-1]
	if !lane.pending || token != lane.token {
		w.mu.Unlock()
		return nil
	}
	lane.pending = false
	event := lane.event
	closed := w.closed || lane.closed
	w.mu.Unlock()
	defer func() { p.finishObserver(w, lane, err) }()
	if closed {
		return nil
	}
	if event.Err != nil {
		return event.Err
	}
	call, done := w.callbackContext(ctx)
	defer done()
	return p.executeObserver(call, host, lane.spec, event)
}
func (p *Plugin) finishObserver(w *watchRuntime, lane *observerLane, err error) {
	if err != nil {
		var source sourceAttributeError
		var home sourceHomeError
		if errors.As(err, &source) || errors.As(err, &home) {
			fmt.Fprintln(os.Stderr, err)
			w.mu.Lock()
			lane.closed = true
			lane.queue = nil
			w.mu.Unlock()
			lane.close()
		} else {
			w.abort()
		}
	}
	w.mu.Lock()
	lane.active = false
	w.activeObservers--
	w.calls--
	w.maybeFinishLocked()
	w.mu.Unlock()
	w.signal()
}
func (p *Plugin) executeObserver(ctx context.Context, host plugins.HostCaller, spec Watch, event fileEvent) error {
	return p.run(ctx, host, func(op *operation) error {
		if err := op.Ops.(*sdkOps).authorizeDir(spec.Path); err != nil {
			return err
		}
		switch event.Kind {
		case "created":
			return op.Created(event.Path, event.Directory)
		case "moved":
			return op.Moved(event.Path, event.Dest, event.Mode, event.Directory)
		case "deleted":
			return op.Deleted(event.Path, event.Mode, event.Directory)
		}
		return errors.New("invalid local observer event")
	})
}
func (p *Plugin) runTimer(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) (err error) {
	w, _, token, err := p.runtime(raw)
	if err != nil || w == nil {
		return err
	}
	w.mu.Lock()
	if !w.timerPending || token != w.timerToken {
		w.mu.Unlock()
		return nil
	}
	w.timerPending = false
	closed := w.closed
	specs := make([]Watch, len(w.lanes))
	for i, lane := range w.lanes {
		specs[i] = lane.spec
	}
	w.mu.Unlock()
	defer func() {
		if err != nil {
			w.abort()
		}
		w.mu.Lock()
		w.timerActive = false
		w.calls--
		w.maybeFinishLocked()
		w.mu.Unlock()
		w.signal()
	}()
	if closed {
		return nil
	}
	call, done := w.callbackContext(ctx)
	defer done()
	return p.run(call, host, func(op *operation) error {
		for _, spec := range specs {
			if err := op.Ops.(*sdkOps).authorizeDir(spec.Path); err != nil {
				return err
			}
		}
		op.Tick()
		return nil
	})
}
