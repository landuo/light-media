package cleanuphost

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"time"

	"light-media/internal/plugins/removelink"
)

type request struct {
	ctx    context.Context
	fn     func() error
	result chan error
}
type Host struct {
	opts     Options
	binding  Binding
	ops      *localOps
	machine  *removelink.Machine
	config   json.RawMessage
	watcher  Watcher
	blocked  string
	commands chan request
	done     chan struct{}
	closing  bool
	running  bool
}

func Open(opts Options) (*Host, error) {
	if opts.Binding.InstanceID == "" || opts.Binding.Generation < 1 || opts.Binding.Revision < 1 || opts.Journal == nil || opts.Effects == nil || opts.Guard == nil {
		return nil, errors.New("trusted binding, journal, effects and lifecycle guard are required")
	}
	opts.Roots = append([]Root(nil), opts.Roots...)
	opts.MediaExtensions = append([]string(nil), opts.MediaExtensions...)
	if opts.AsyncTimeout <= 0 {
		opts.AsyncTimeout = 30 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Watch == nil {
		opts.Watch = NativeWatch
	}
	h := &Host{opts: opts, binding: opts.Binding, commands: make(chan request), done: make(chan struct{})}
	var e error
	h.ops, e = newLocal(h, opts.Roots)
	if e != nil {
		return nil, e
	}
	h.machine = removelink.New(h.ops)
	h.machine.Now = opts.Now
	saved, e := opts.Journal.Read(context.Background())
	if e != nil {
		return nil, e
	}
	if saved.Binding.InstanceID != "" {
		if saved.Binding.InstanceID != opts.Binding.InstanceID || saved.Binding.Generation != opts.Binding.Generation || !rootsEqual(saved.Roots, h.ops.roots) {
			return nil, ErrUnsafe
		}
		h.binding = saved.Binding
		h.config = append(json.RawMessage(nil), saved.Config...)
		if e = h.machine.Config.Apply(h.config); e != nil {
			return nil, e
		}
		h.machine.State = saved.Machine.State
		h.machine.Queue = saved.Machine.Queue
		h.machine.TimerDue = saved.Machine.TimerDue
		h.machine.Watches = saved.Machine.Watches
		h.ops.pins = saved.Pins
		h.ops.frozen = saved.Frozen
		h.ops.serial = saved.Serial
		h.blocked = saved.Blocked
		if saved.Active != nil {
			h.blocked = "interrupted " + saved.Active.Kind + " requires receipt reconciliation"
		}
		for _, r := range saved.Events {
			if r.State == "pending" || r.State == "planned" {
				h.blocked = "captured event requires identity reconciliation before recovery"
				break
			}
		}
	}
	go h.loop()
	return h, nil
}
func (h *Host) call(ctx context.Context, fn func() error) error {
	r := request{ctx: ctx, fn: fn, result: make(chan error, 1)}
	select {
	case <-h.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	case h.commands <- r:
	}
	select {
	case e := <-r.result:
		return e
	case <-ctx.Done():
		return ctx.Err()
	case <-h.done:
		select {
		case e := <-r.result:
			return e
		default:
			return ErrClosed
		}
	}
}
func (h *Host) loop() {
	defer close(h.done)
	for !h.closing {
		var events <-chan Event
		var failures <-chan error
		var tick <-chan time.Time
		var timer *time.Timer
		if h.running && h.watcher != nil && h.blocked == "" {
			events = h.watcher.Events()
			failures = h.watcher.Errors()
		}
		if h.running && h.machine.TimerDue != nil && h.blocked == "" {
			d := h.machine.TimerDue.Sub(h.opts.Now())
			if d < 0 {
				d = 0
			}
			timer = time.NewTimer(d)
			tick = timer.C
		}
		select {
		case r := <-h.commands:
			if r.ctx.Err() != nil {
				r.result <- r.ctx.Err()
			} else {
				r.result <- r.fn()
			}
		case e, ok := <-events:
			if !ok {
				h.block("watch channel closed unexpectedly")
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), h.opts.AsyncTimeout)
				err := h.process(ctx, e, false)
				cancel()
				if err != nil {
					h.block(err.Error())
				}
			}
		case e, ok := <-failures:
			if ok && e != nil {
				h.block("watch failed: " + e.Error())
			} else {
				h.block("watch error channel closed unexpectedly")
			}
		case <-tick:
			ctx, cancel := context.WithTimeout(context.Background(), h.opts.AsyncTimeout)
			e := h.tick(ctx, false)
			cancel()
			if e != nil {
				h.block(e.Error())
			}
		}
		if timer != nil {
			timer.Stop()
		}
	}
}
func (h *Host) guard(ctx context.Context, action string) (func(), error) {
	release, e := h.opts.Guard(ctx, h.binding, action)
	if e == nil && release == nil {
		return nil, ErrUnsafe
	}
	return release, e
}
func (h *Host) checkpoint(ctx context.Context, active *Event) error { return h.save(ctx, active, nil) }
func (h *Host) save(ctx context.Context, active *Event, extra func(*Durable)) error {
	return h.opts.Journal.Update(ctx, func(s *Durable) error {
		s.Binding = h.binding
		s.Roots = h.ops.roots
		s.Config = h.config
		s.Machine = MachineState{h.machine.State, h.machine.Queue, h.machine.TimerDue, h.machine.Watches}
		s.Pins = h.ops.pins
		s.Frozen = h.ops.frozen
		s.Serial = h.ops.serial
		s.Active = active
		s.Blocked = h.blocked
		if extra != nil {
			extra(s)
		}
		return nil
	})
}
func (h *Host) block(reason string) {
	h.blocked = reason
	h.running = false
	_ = h.closeWatch(context.Background(), false)
	_ = h.checkpoint(context.Background(), nil)
}
func (h *Host) Snapshot(ctx context.Context) (Durable, error) {
	var out Durable
	e := h.call(ctx, func() error {
		var e error
		out, e = h.opts.Journal.Read(ctx)
		if e == nil && out.Blocked == "" {
			out.Blocked = h.blocked
		}
		return e
	})
	return out, e
}
func (h *Host) watchSpecs(raw json.RawMessage) ([]removelink.Watch, error) {
	cfg := h.machine.Config
	if e := cfg.Apply(raw); e != nil {
		return nil, e
	}
	if !removelink.Truth(cfg.Enabled) {
		return nil, nil
	}
	out := []removelink.Watch{}
	for _, p := range strings.Split(cfg.Monitor, "\n") {
		p = strings.TrimSpace(p)
		if p == "" || removelink.Trickplay(p) {
			continue
		}
		if _, e := h.ops.root(p); e != nil {
			return nil, e
		}
		out = append(out, removelink.Watch{Path: p, Mode: "hardlink"})
	}
	if removelink.Truth(cfg.STRM) {
		for _, m := range removelink.ParseMappings(cfg.Mappings) {
			if m.Storage != "local" {
				return nil, ErrUnsupported
			}
			if _, e := h.ops.root(m.Prefix); e != nil {
				return nil, e
			}
			if _, e := h.ops.root(strings.TrimSuffix(m.Target, "/")); e != nil {
				return nil, e
			}
			if !removelink.Trickplay(m.Prefix) {
				out = append(out, removelink.Watch{Path: m.Prefix, Mode: "strm"})
			}
		}
	}
	return out, nil
}
func (h *Host) closeWatch(ctx context.Context, drain bool) error {
	w := h.watcher
	h.watcher = nil
	if w == nil {
		return nil
	}
	if e := w.Close(); e != nil {
		return e
	}
	for e := range w.Events() {
		if drain {
			if err := h.process(ctx, e, true); err != nil {
				return err
			}
		}
	}
	for e := range w.Errors() {
		if e != nil {
			return e
		}
	}
	return nil
}

// Configure is a trusted lifecycle call. App must bind raw to the persisted
// installed invocation; a worker can only request configure with {}.
func (h *Host) Configure(ctx context.Context, binding Binding, raw json.RawMessage) error {
	return h.call(ctx, func() error {
		if h.blocked != "" {
			return ErrUncertain
		}
		if binding.InstanceID != h.binding.InstanceID || binding.Generation != h.binding.Generation || binding.Revision < h.binding.Revision {
			return ErrUnsafe
		}
		old := h.binding
		h.binding = binding
		release, e := h.guard(ctx, "configure")
		if e != nil {
			h.binding = old
			return e
		}
		defer release()
		specs, e := h.watchSpecs(raw)
		if e != nil {
			h.binding = old
			return e
		}
		if e = h.machine.Config.Apply(raw); e != nil {
			return e
		}
		h.config = append(json.RawMessage(nil), raw...)
		h.running = false
		if e = h.drainPending(ctx); e != nil {
			h.block(e.Error())
			return e
		}
		if e = h.closeWatch(ctx, true); e != nil {
			h.block(e.Error())
			return e
		}
		if e = h.drainPending(ctx); e != nil {
			h.block(e.Error())
			return e
		}
		event := Event{ID: "configure:" + digest([]any{binding, string(raw)}), Kind: "configure", At: h.opts.Now()}
		h.ops.ctx = ctx
		h.ops.scope = event.ID
		h.ops.errors = nil
		if e = h.checkpoint(ctx, &event); e != nil {
			return e
		}
		if len(specs) > 0 {
			h.watcher, e = h.opts.Watch(context.Background(), specs, h.ops.openWatchDir)
			if e != nil {
				h.block(e.Error())
				return e
			}
		}
		// Watches exist before initial scan. Their bounded event channel retains
		// changes while the serial owner builds the baseline.
		before := len(h.machine.Errors)
		e = h.machine.Init(raw)
		if e == nil && len(h.machine.Errors) > before {
			e = h.machine.Errors[before]
		}
		if e == nil && len(h.ops.errors) > 0 {
			e = h.ops.errors[0]
		}
		if e != nil {
			_ = h.closeWatch(ctx, false)
			h.block(e.Error())
			return e
		}
		if len(h.machine.Queue) == 0 {
			h.ops.frozen = map[string]Pin{}
		}
		if err := h.checkpoint(ctx, nil); err != nil {
			h.block(err.Error())
			return err
		}
		h.running = removelink.Truth(h.machine.Config.Enabled)
		return nil
	})
}

// Recover restores persisted deadlines without source Init, which would
// immediately drain them. Interrupted effects fail closed pending reconciliation.
func (h *Host) Recover(ctx context.Context, binding Binding) error {
	return h.call(ctx, func() error {
		if binding != h.binding || h.blocked != "" {
			return ErrUncertain
		}
		release, e := h.guard(ctx, "recover")
		if e != nil {
			return e
		}
		defer release()
		specs, e := h.watchSpecs(h.config)
		if e != nil {
			return e
		}
		if h.watcher != nil {
			return nil
		}
		if len(specs) > 0 {
			h.watcher, e = h.opts.Watch(context.Background(), specs, h.ops.openWatchDir)
			if e != nil {
				return e
			}
		}
		h.ops.ctx = ctx
		h.ops.scope = "recover:" + digest([]any{binding, h.opts.Now()})
		h.ops.errors = nil
		old := map[string]removelink.Tracked{}
		for _, r := range h.machine.State {
			old[r.Path] = r
		}
		current := map[string]removelink.Tracked{}
		var order []string
		now := h.opts.Now()
		for _, w := range specs {
			rows, e := h.ops.Scan(w.Path)
			if e != nil {
				_ = h.closeWatch(ctx, false)
				h.block(e.Error())
				return e
			}
			for _, r := range rows {
				if _, exists := current[r.Path]; !exists {
					order = append(order, r.Path)
				}
				if prior, ok := old[r.Path]; ok && prior.Identity.Dev == r.Identity.Dev && prior.Identity.Inode == r.Identity.Inode {
					r.Identity.Added = prior.Identity.Added
				} else {
					r.Identity.Added = now
				}
				current[r.Path] = r
			}
		}
		// Keep original insertion/added times for surviving tracked entities.
		// New offline files receive a fresh observation time, protecting them
		// from pending deletions as the original recent-reorganization guard.
		merged := []removelink.Tracked{}
		for _, r := range h.machine.State {
			if fresh, ok := current[r.Path]; ok {
				merged = append(merged, fresh)
				delete(current, r.Path)
			}
		}
		for _, path := range order {
			if r, ok := current[path]; ok {
				merged = append(merged, r)
				delete(current, path)
			}
		}
		h.machine.State = merged
		// Source Stop clears Watches. A cleanly stopped checkpoint still has
		// enabled config, so recovery must restore the same authorization specs
		// as the observer before it can deliver any event to allowedEvent.
		h.machine.Watches = append([]removelink.Watch(nil), specs...)
		if e = h.checkpoint(ctx, nil); e != nil {
			h.block(e.Error())
			return e
		}
		h.running = removelink.Truth(h.machine.Config.Enabled)
		return nil
	})
}
func (h *Host) freezeTargets() {
	for _, task := range h.machine.Queue {
		for _, r := range h.machine.State {
			if r.Identity.Dev == task.Identity.Dev && r.Identity.Inode == task.Identity.Inode {
				if _, ok := h.ops.frozen[r.Path]; !ok {
					h.ops.frozen[r.Path] = h.ops.pins[r.Path]
				}
			}
		}
	}
}
func (h *Host) observeGone(ctx context.Context, e Event) (bool, error) {
	pin, ok := h.ops.pins[e.Path]
	if !ok {
		return false, nil
	}
	if _, err := h.ops.capture(e.Path); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	id := digest([]any{h.binding, "observed_delete", e.Mode, e.Path, pin})
	s, err := h.opts.Journal.Read(ctx)
	if err != nil {
		return false, err
	}
	if old, ok := s.Effects[id]; ok && old.State == "confirmed" {
		return true, nil
	}
	r, err := h.ops.root(e.Path)
	if err != nil {
		return false, err
	}
	effect := Effect{ID: id, Scope: e.ID, Kind: "observed_delete", Path: e.Path, Role: r.Root.Role, Binding: h.binding, Expected: pin, State: "confirmed"}
	return false, h.opts.Journal.Update(ctx, func(s *Durable) error { s.Effects[id] = effect; return nil })
}
func (h *Host) process(ctx context.Context, event Event, guarded bool) (err error) {
	if h.blocked != "" {
		return ErrUncertain
	}
	if !guarded {
		if !h.running {
			return ErrUnsafe
		}
		release, e := h.guard(ctx, "event")
		if e != nil {
			return e
		}
		defer release()
	}
	if event.ID == "" || event.Path == "" || !validPath(event.Path) || (event.Mode != "hardlink" && event.Mode != "strm") {
		return ErrUnsafe
	}
	if !h.allowedEvent(event) {
		return ErrUnsafe
	}
	if _, e := h.ops.root(event.Path); e != nil {
		return e
	}
	saved, e := h.opts.Journal.Read(ctx)
	if e != nil {
		return e
	}
	sequence := saved.EventSerial + 1
	if old, ok := saved.Events[event.ID]; ok {
		sequence = old.Sequence
	}
	for id, r := range saved.Events {
		if id != event.ID && (r.State == "pending" || r.State == "planned") && r.Sequence < sequence {
			return ErrOrder
		}
	}
	if old, ok := saved.Events[event.ID]; ok {
		if !sameEvent(old.Event, event) {
			return ErrUnsafe
		}
		if old.State == "confirmed" {
			return nil
		}
		if old.State != "pending" {
			return ErrUncertain
		}
		if old.Binding.InstanceID != h.binding.InstanceID || old.Binding.Generation != h.binding.Generation || (!guarded && old.Binding != h.binding) {
			return ErrUnsafe
		}
	}
	if e = h.opts.Journal.Update(ctx, func(s *Durable) error {
		r, ok := s.Events[event.ID]
		if !ok {
			s.EventSerial++
			r = EventReceipt{Event: event, Binding: h.binding, Sequence: s.EventSerial}
		}
		r.State = "planned"
		s.Events[event.ID] = r
		s.Active = &event
		return nil
	}); e != nil {
		return e
	}
	h.ops.ctx = ctx
	h.ops.scope = event.ID
	h.ops.errors = nil
	before := len(h.machine.Errors)
	if event.Mode != "hardlink" && event.Mode != "strm" {
		return ErrUnsafe
	}
	// Freeze full snapshots at selection, after normal earlier file writes but
	// before any delayed cleanup can consume a concurrently changed target.
	if event.Kind == "deleted" || event.Kind == "moved" {
		if origin, ok := h.ops.pins[event.Path]; ok {
			for _, tracked := range h.machine.State {
				if tracked.Identity.Dev == origin.File.Dev && tracked.Identity.Inode == origin.File.Inode {
					if _, frozen := h.ops.frozen[tracked.Path]; !frozen {
						if current, e := h.ops.capture(tracked.Path); e == nil && sameEntity(current.File, origin.File) {
							current.Epoch = h.ops.pins[tracked.Path].Epoch
							h.ops.pins[tracked.Path] = current
						}
					}
				}
			}
		}
	}
	switch event.Kind {
	case "created":
		err = h.machine.Created(event.Path, event.Directory)
	case "moved":
		if !validPath(event.Dest) {
			err = ErrUnsafe
		} else if _, e := h.ops.root(event.Dest); e != nil {
			err = e
		} else {
			if !removelink.Trickplay(event.Path) && !removelink.Trickplay(event.Dest) {
				if _, e := h.ops.capture(event.Dest); e != nil && !errors.Is(e, fs.ErrNotExist) {
					err = e
				}
			}
			if err == nil {
				err = h.machine.Moved(event.Path, event.Dest, event.Mode, event.Directory)
			}
		}
	case "deleted":
		var duplicate bool
		duplicate, err = h.observeGone(ctx, event)
		if err == nil && !duplicate {
			err = h.machine.Deleted(event.Path, event.Mode, event.Directory)
		}
	default:
		err = errors.New("unsupported bound watch event")
	}
	h.freezeTargets()
	if err == nil && len(h.machine.Errors) > before {
		err = h.machine.Errors[before]
	}
	if err == nil && len(h.ops.errors) > 0 {
		err = h.ops.errors[0]
	}
	state := "confirmed"
	if err != nil {
		state = "failed"
		h.blocked = err.Error()
	}
	if e = h.save(ctx, nil, func(s *Durable) {
		r := s.Events[event.ID]
		r.State = state
		if err != nil {
			r.Error = err.Error()
		}
		s.Events[event.ID] = r
	}); e != nil {
		h.blocked = e.Error()
		return e
	}
	return err
}

// Observe is host-only: native watcher/domain-outbox adapters generate this
// payload. A worker-facing bridge must accept only its persisted event ID.
func (h *Host) Observe(ctx context.Context, e Event) error {
	return h.call(ctx, func() error { return h.process(ctx, e, false) })
}
func (h *Host) tick(ctx context.Context, guarded bool) error {
	if h.blocked != "" {
		return ErrUncertain
	}
	if !guarded {
		release, e := h.guard(ctx, "timer")
		if e != nil {
			return e
		}
		defer release()
	}
	if len(h.machine.Queue) == 0 {
		return nil
	}
	event := Event{ID: "timer:" + digest([]any{h.binding, h.machine.Queue}), Kind: "timer", At: h.opts.Now()}
	if e := h.checkpoint(ctx, &event); e != nil {
		h.blocked = e.Error()
		return e
	}
	h.ops.ctx = ctx
	h.ops.scope = event.ID
	h.ops.errors = nil
	before := len(h.machine.Errors)
	h.machine.Tick()
	if len(h.machine.Queue) == 0 {
		h.ops.frozen = map[string]Pin{}
	}
	var err error
	if len(h.machine.Errors) > before {
		err = h.machine.Errors[before]
	}
	if err == nil && len(h.ops.errors) > 0 {
		err = h.ops.errors[0]
	}
	if err != nil {
		h.blocked = err.Error()
	}
	if e := h.checkpoint(ctx, nil); e != nil {
		h.blocked = e.Error()
		return e
	}
	return err
}
func (h *Host) Tick(ctx context.Context) error {
	return h.call(ctx, func() error { return h.tick(ctx, false) })
}
func (h *Host) Stop(ctx context.Context, reason string) error {
	return h.call(ctx, func() error {
		release, e := h.guard(ctx, "stop:"+reason)
		if e != nil {
			return e
		}
		defer release()
		h.running = false
		if h.blocked == "" {
			if e = h.drainPending(ctx); e != nil {
				h.block(e.Error())
				return e
			}
		}
		if e = h.closeWatch(ctx, h.blocked == ""); e != nil {
			return e
		}
		if h.blocked != "" {
			return ErrUncertain
		}
		event := Event{ID: "stop:" + digest([]any{h.binding, reason, h.machine.Queue}), Kind: "stop", At: h.opts.Now()}
		if e = h.checkpoint(ctx, &event); e != nil {
			return e
		}
		h.ops.ctx = ctx
		h.ops.scope = event.ID
		h.ops.errors = nil
		before := len(h.machine.Errors)
		h.machine.Stop()
		if len(h.machine.Errors) > before {
			e = h.machine.Errors[before]
		}
		if e == nil && len(h.ops.errors) > 0 {
			e = h.ops.errors[0]
		}
		if e != nil {
			h.blocked = e.Error()
		}
		if e == nil {
			h.ops.frozen = map[string]Pin{}
		}
		save := h.checkpoint(ctx, nil)
		if save != nil {
			return save
		}
		return e
	})
}
func (h *Host) WorkerStop(context.Context) error { return nil }

// Suspend models an abrupt process exit in fixtures: quiesce watchers and owner
// without source stop_service or a fake successful drain.
func (h *Host) Suspend(ctx context.Context) error {
	return h.call(ctx, func() error { e := h.closeWatch(ctx, false); h.closing = true; return e })
}
func (h *Host) Close(ctx context.Context) error {
	e := h.Stop(ctx, "shutdown")
	_ = h.Suspend(ctx)
	return e
}

func sameEvent(a, b Event) bool {
	return a.ID == b.ID && a.Kind == b.Kind && a.Path == b.Path && a.Dest == b.Dest && a.Mode == b.Mode && a.Directory == b.Directory && a.At.Equal(b.At)
}

// RecordEvent and Apply are the bridge for a future App durable listener/job.
// RecordEvent is host-only; the SDK bridge may only submit the recorded ID.
func (h *Host) RecordEvent(ctx context.Context, e Event) error {
	return h.call(ctx, func() error {
		release, err := h.guard(ctx, "record")
		if err != nil {
			return err
		}
		defer release()
		if !h.running || h.blocked != "" || e.ID == "" || !validPath(e.Path) || (e.Mode != "hardlink" && e.Mode != "strm") {
			return ErrUnsafe
		}
		if !h.allowedEvent(e) {
			return ErrUnsafe
		}
		if _, err = h.ops.root(e.Path); err != nil {
			return err
		}
		return h.opts.Journal.Update(ctx, func(s *Durable) error {
			if old, ok := s.Events[e.ID]; ok {
				if !sameEvent(old.Event, e) {
					return ErrUnsafe
				}
				return nil
			}
			s.EventSerial++
			s.Events[e.ID] = EventReceipt{Event: e, Binding: h.binding, Sequence: s.EventSerial, State: "pending"}
			return nil
		})
	})
}
func (h *Host) Apply(ctx context.Context, binding Binding, eventID string) error {
	return h.call(ctx, func() error {
		if !h.running || binding != h.binding {
			return ErrUnsafe
		}
		release, err := h.guard(ctx, "apply")
		if err != nil {
			return err
		}
		defer release()
		s, err := h.opts.Journal.Read(ctx)
		if err != nil {
			return err
		}
		record, ok := s.Events[eventID]
		if !ok || record.Binding != binding {
			return ErrUnsafe
		}
		return h.process(ctx, record.Event, true)
	})
}

func (h *Host) drainPending(ctx context.Context) error {
	s, e := h.opts.Journal.Read(ctx)
	if e != nil {
		return e
	}
	var pending []EventReceipt
	for _, r := range s.Events {
		if r.State == "pending" {
			pending = append(pending, r)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Sequence < pending[j].Sequence })
	for _, r := range pending {
		if e = h.process(ctx, r.Event, true); e != nil {
			return e
		}
	}
	return nil
}

func (h *Host) allowedEvent(e Event) bool {
	if e.Kind != "created" && e.Kind != "deleted" && e.Kind != "moved" {
		return false
	}
	for _, w := range h.machine.Watches {
		if w.Mode == e.Mode && inside(w.Path, e.Path) {
			if e.Kind != "moved" || inside(w.Path, e.Dest) {
				return true
			}
		}
	}
	return false
}
