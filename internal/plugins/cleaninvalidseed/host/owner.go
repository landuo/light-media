package seedhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	seed "light-media/internal/plugins/cleaninvalidseed"
	"light-media/internal/plugins/schedule"
	"sort"
	"sync"
	"time"
	"unicode/utf8"
)

type Owner struct {
	opts          Options
	gate          chan struct{}
	runtimeConfig json.RawMessage
}
type execution struct {
	serviceErr      error
	skipped         string
	nativeSnapshots map[string]map[string]json.RawMessage
	proofs          map[string]json.RawMessage
	o               *Owner
	inv             Invocation
	ctx             context.Context
	cancel          context.CancelFunc
	machine         *seed.Machine
	mu              sync.Mutex
	hostErr         error
	readFailure     bool
	lists           map[string]bool
	selected        map[string]Downloader
	ordinal         int
	effective       Binding
	configReceipt   string
	pins            map[string]Pin
	readable        map[string]bool
	releaseAction   func()
}

func Open(opts Options) (*Owner, error) {
	if opts.Binding.InstanceID == "" || opts.Binding.Generation < 1 || opts.Binding.Revision < 1 || opts.Journal == nil || opts.Core == nil {
		return nil, ErrUnsafe
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Timezone == "" {
		opts.Timezone = "UTC"
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, d := range opts.Downloaders {
		if d.ID == "" || d.Name == "" || d.Revision < 1 || d.ConfigSHA == "" || ids[d.ID] || names[d.Name] || d.client == nil || d.seal != digest([]any{d.ID, d.Name, d.Kind, d.Revision, d.ConfigSHA}) || (d.Kind != "qbittorrent" && d.Kind != "transmission") {
			return nil, ErrUnsafe
		}
		ids[d.ID] = true
		names[d.Name] = true
	}
	if err := validateRoots(opts.Roots); err != nil {
		return nil, err
	}
	opts.Roots = append([]Root(nil), opts.Roots...)
	opts.Downloaders = append([]Downloader(nil), opts.Downloaders...)
	return &Owner{opts: opts, gate: make(chan struct{}, 1)}, nil
}
func digest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func decodeConfig(b json.RawMessage) (map[string]any, error) {
	if !utf8.Valid(b) {
		return nil, ErrUnsafe
	}
	var m map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&m); err != nil || m == nil || d.Decode(new(any)) != io.EOF {
		return nil, ErrUnsafe
	}
	return m, nil
}
func (o *Owner) enter(ctx context.Context, inv Invocation, config json.RawMessage) (*execution, error) {
	select {
	case o.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ok := false
	var releaseAction func()
	defer func() {
		if !ok {
			if releaseAction != nil {
				releaseAction()
			}
			<-o.gate
		}
	}()
	if inv.Binding.InstanceID != o.opts.Binding.InstanceID || inv.Binding.Generation != o.opts.Binding.Generation || inv.RunID == "" || inv.InputSHA == "" {
		return nil, ErrUnsafe
	}
	if o.opts.BeforeRun != nil {
		next, release, err := o.opts.BeforeRun(ctx, inv)
		releaseAction = release
		if err != nil {
			return nil, err
		}
		if release == nil || next.BeforeRun == nil || next.Binding != inv.Binding || !sameControlBackend(next.Core, o.opts.Core) || !sameControlBackend(next.Journal, o.opts.Journal) {
			return nil, ErrUnsafe
		}
		validated, err := Open(next)
		if err != nil {
			return nil, err
		}
		o.opts = validated.opts
	}
	proof, release, err := o.opts.Core.Guard(ctx, inv, Effect{Kind: "begin", Binding: inv.Binding, Authority: inv.Binding, Args: config})
	_ = proof
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, ErrUnsafe
	}
	defer release()
	var saved State
	err = o.opts.Journal.Update(ctx, func(tx *sql.Tx, s *State) error {
		for _, prior := range s.Runs {
			if prior.Phase == "started" || prior.Phase == "unknown" {
				return ErrUnknown
			}
		}
		for _, effect := range s.Effects {
			if effect.Phase == "started" || effect.Phase == "unknown" || effect.Phase == "fs_confirmed" {
				return ErrUnknown
			}
		}
		if _, exists := s.Runs[inv.RunID]; exists {
			return ErrAlready
		}
		if s.Binding != (Binding{}) && (s.Binding.InstanceID != inv.Binding.InstanceID || s.Binding.Generation != inv.Binding.Generation || s.Binding.Revision > inv.Binding.Revision) {
			return ErrUnsafe
		}
		if inv.Action != "init" && inv.Action != "reload" && s.Binding != inv.Binding {
			return ErrUnsafe
		}
		s.Runs[inv.RunID] = Run{Invocation: inv, Effective: inv.Binding, Phase: "started", Config: append(json.RawMessage(nil), config...)}
		saved = *s
		return nil
	})
	if err != nil {
		return nil, err
	}
	child, cancel := context.WithCancel(ctx)
	e := &execution{o: o, inv: inv, effective: inv.Binding, ctx: child, cancel: cancel, selected: map[string]Downloader{}, lists: map[string]bool{}, pins: map[string]Pin{}, readable: map[string]bool{}, releaseAction: releaseAction}
	e.proofs = map[string]json.RawMessage{}
	e.nativeSnapshots = map[string]map[string]json.RawMessage{}
	e.machine = seed.New(e)
	e.machine.Now = o.opts.Now
	e.machine.Timezone = o.opts.Timezone
	runtimeConfig := saved.Config
	if len(o.runtimeConfig) > 0 {
		runtimeConfig = o.runtimeConfig
	}
	if len(runtimeConfig) > 0 && inv.Action != "reload" && !(inv.Action == "init" && len(o.runtimeConfig) == 0) {
		m, err := decodeConfig(runtimeConfig)
		if err != nil {
			cancel()
			return nil, err
		}
		e.machine.Config = m
	}
	ok = true
	return e, nil
}
func (e *execution) scoped(effect Effect) Effect {
	effect.Binding, effect.Authority, effect.ConfigReceipt = e.inv.Binding, e.effective, e.configReceipt
	effect.RunID = e.inv.RunID
	return effect
}
func (e *execution) finish(sourceErr error) error {
	// Original temporary command flags have no finally: preserve the live object
	// even when a later source call fails, without inventing a public config save.
	e.o.runtimeConfig = raw(e.machine.Config)
	defer e.cancel()
	defer func() { <-e.o.gate }()
	if e.releaseAction != nil {
		defer e.releaseAction()
	}
	e.mu.Lock()
	hostErr := e.hostErr
	e.mu.Unlock()
	result := Result{Effects: e.ordinal, Skipped: e.skipped}
	if e.serviceErr != nil {
		result.ServiceError = fmt.Sprint(seed.ErrorType(e.serviceErr))
	}
	if sourceErr != nil {
		result.SourceError = fmt.Sprint(seed.ErrorType(sourceErr))
	}
	if hostErr != nil {
		result.HostError = "HOST_ACTION_UNCONFIRMED"
	}
	finalErr := errors.Join(sourceErr, hostErr, e.serviceErr)
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(e.ctx), 3*time.Second)
	defer cancel()
	_, release, guardErr := e.o.opts.Core.Guard(finishCtx, e.inv, e.scoped(Effect{Kind: "finalize"}))
	if guardErr != nil {
		return errors.Join(finalErr, ErrUnknown, guardErr)
	}
	if release == nil {
		return ErrUnknown
	}
	defer release()
	err := e.o.opts.Journal.Update(finishCtx, func(tx *sql.Tx, s *State) error {
		r, ok := s.Runs[e.inv.RunID]
		if !ok || r.Phase != "started" || r.Invocation != e.inv || r.Effective != e.effective || r.ConfigReceipt != e.configReceipt {
			return ErrUnsafe
		}
		r.Phase = "confirmed"
		if finalErr != nil {
			r.Phase = "failed"
			r.ErrorCode = "SOURCE_FAILED"
			if hostErr != nil {
				r.ErrorCode = "HOST_ACTION_DENIED"
				uncertain := errors.Is(hostErr, ErrUnknown)
				for _, effect := range s.Effects {
					if effect.RunID == e.inv.RunID && (effect.Phase == "started" || effect.Phase == "unknown" || effect.Phase == "fs_confirmed") {
						uncertain = true
					}
				}
				if uncertain {
					r.Phase = "unknown"
					r.ErrorCode = "HOST_ACTION_UNCONFIRMED"
				}
			}
		}
		if hostErr != nil {
			// The native journal distinguishes a proven rejection from a lost
			// outcome. Do not describe an exact not-issued receipt as unknown.
			result.HostError = r.ErrorCode
		}
		r.Result = raw(result)
		s.Runs[e.inv.RunID] = r
		if e.inv.Action == "init" || e.inv.Action == "reload" {
			if sourceErr == nil || e.configReceipt != "" {
				s.Binding = e.effective
				s.Config = raw(e.machine.Config)
			}
			// onlyonce date+flag publication happened at its source SaveConfig boundary.
			// get_service is a separate source method: cron failure does not erase it.
			if sourceErr == nil && e.serviceErr == nil {
				spec, triggerErr := e.machine.ServiceTrigger()
				if triggerErr != nil {
					return triggerErr
				}
				if spec != nil {
					cron, err := schedule.ParseCrontab(spec.Cron, spec.Timezone)
					if err != nil {
						return err
					}
					id := "service:" + e.inv.RunID
					s.Triggers[id] = Trigger{ID: id, Kind: "service", Phase: "ready", Binding: e.effective, Spec: spec, Due: cron.First(e.o.opts.Now())}
				}
			}
		}
		if e.inv.EventID != "" {
			t, ok := s.Triggers[e.inv.EventID]
			if !ok || t.QueuedRunID != e.inv.RunID {
				return ErrUnsafe
			}
			t.Phase = "confirmed"
			if e.skipped != "" {
				t.Phase = e.skipped
			}
			if finalErr != nil {
				t.Phase = "failed"
			}
			s.Triggers[t.ID] = t
		}
		return nil
	})
	if err != nil {
		return errors.Join(finalErr, ErrUnknown, err)
	}
	return finalErr
}

// stopSchedules records source StopService before Init can fail. The platform
// service plan is also stopped here; no callback performs cleanup work.
func (e *execution) stopSchedules() error {
	_, release, err := e.o.opts.Core.Guard(e.ctx, e.inv, e.scoped(Effect{Kind: "schedule_stop"}))
	if err != nil {
		return err
	}
	if release == nil {
		return ErrUnsafe
	}
	defer release()
	return e.o.opts.Journal.Update(e.ctx, func(_ *sql.Tx, s *State) error {
		for id, t := range s.Triggers {
			if t.Phase == "ready" && (t.Kind == "once" || t.Kind == "service") {
				t.Phase = "canceled"
				s.Triggers[id] = t
			}
		}
		return nil
	})
}
func (o *Owner) Configure(ctx context.Context, inv Invocation, config json.RawMessage) error {
	if inv.Action != "init" && inv.Action != "reload" {
		return ErrUnsafe
	}
	m, err := decodeConfig(config)
	if err != nil {
		return err
	}
	e, err := o.enter(ctx, inv, config)
	if err != nil {
		return err
	}
	if err = e.stopSchedules(); err != nil {
		return e.finish(err)
	}
	initErr := e.machine.Init(e.ctx, m)
	if initErr == nil {
		_, e.serviceErr = e.machine.ServiceTrigger()
	}
	return e.finish(initErr)
}
func (o *Owner) Execute(ctx context.Context, inv Invocation) error {
	s, err := o.opts.Journal.Read(ctx)
	if err != nil {
		return err
	}
	e, err := o.enter(ctx, inv, s.Config)
	if err != nil {
		return err
	}
	if inv.Action == "run" {
		if inv.EventID != "" {
			return e.finish(ErrUnsafe)
		}
		return e.finish(e.machine.Clean(e.ctx))
	}
	t, ok := s.Triggers[inv.EventID]
	if !ok || t.QueuedRunID != inv.RunID || t.Queued == nil || *t.Queued != inv || t.Binding != inv.Binding || t.Phase != "queued" {
		return e.finish(ErrUnsafe)
	}
	switch t.Kind {
	case "manual_service":
		// The trusted platform's manual service action calls the same source
		// function directly. It is not a date/cron job and has no misfire grace.
		if t.Action != "clean_invalid_seed" || t.OccurrenceID == "" {
			return e.finish(ErrUnsafe)
		}
		err = e.machine.Clean(e.ctx)
	case "once", "service":
		if o.opts.Now().Before(t.Due) {
			return e.finish(ErrUnsafe)
		}
		if o.opts.Now().Sub(t.Due) > time.Second {
			e.skipped = "missed"
			return e.finish(nil)
		}
		err = e.machine.Clean(e.ctx)
	case "command":
		err = e.machine.HandleCommand(e.ctx, t.Command)
	default:
		err = ErrUnsafe
	}
	return e.finish(err)
}

// PublishCommand is called by the trusted inbound-command producer. enqueue
// inserts its actual plugin_run in this same SQLite transaction, not a new job.
func (o *Owner) PublishCommand(ctx context.Context, t Trigger, enqueue func(*sql.Tx, Trigger) (Invocation, error)) error {
	if t.ID == "" || t.Kind != "command" || t.Command == nil {
		return ErrUnsafe
	}
	return o.opts.Journal.Update(ctx, func(tx *sql.Tx, s *State) error {
		if _, exists := s.Triggers[t.ID]; exists || t.Binding != s.Binding {
			return ErrUnsafe
		}
		inv, err := enqueue(tx, t)
		if err != nil {
			return err
		}
		if inv.EventID != t.ID || inv.Binding != t.Binding || inv.RunID == "" {
			return ErrUnsafe
		}
		t.QueuedRunID, t.Phase = inv.RunID, "queued"
		copyInv := inv
		t.Queued = &copyInv
		s.Triggers[t.ID] = t
		return nil
	})
}

// latestDue is the frozen platform coalescing helper, reused rather than
// reinterpreting APScheduler 3.11.3's trigger calculation in this candidate.
func latestDue(trigger *schedule.Trigger, due, at time.Time) (time.Time, time.Time) {
	next := trigger.Next(due)
	if next.IsZero() || next.After(at) {
		return due, next
	}
	best, low, high := next, next, at
	for !low.After(high) {
		seconds := high.Unix() - low.Unix()
		middle := time.Unix(low.Unix()+seconds/2, (int64(low.Nanosecond())+int64(high.Nanosecond()))/2+(seconds%2)*int64(time.Second)/2).UTC()
		candidate := trigger.First(middle)
		if !candidate.IsZero() && !candidate.After(at) {
			if candidate.After(best) {
				best = candidate
			}
			low = candidate.Add(time.Nanosecond)
		} else {
			high = middle.Add(-time.Nanosecond)
		}
	}
	return best, trigger.Next(best)
}

func (o *Owner) ClaimDue(ctx context.Context, at time.Time, enqueue func(*sql.Tx, Trigger) (Invocation, error)) error {
	return o.opts.Journal.Update(ctx, func(tx *sql.Tx, s *State) error {
		ids := []string{}
		for id, t := range s.Triggers {
			if t.Phase == "ready" && !t.Due.After(at) {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			t := s.Triggers[id]
			spec := schedule.Spec{Kind: "date", Timezone: o.opts.Timezone, RunAt: schedule.FormatTime(t.Due)}
			if t.Kind == "service" {
				if t.Spec == nil {
					return ErrUnsafe
				}
				spec = *t.Spec
			} else if t.Kind != "once" {
				return ErrUnsafe
			}
			trigger, err := schedule.New(spec, t.Due)
			if err != nil {
				return err
			}
			latest, nextDue := latestDue(trigger, t.Due, at)
			t.Due = latest
			t.Phase = "queued"
			if at.Sub(latest) > time.Second {
				t.Phase = "missed"
			}
			if t.Phase == "queued" {
				for oldID, old := range s.Triggers {
					if oldID != id && old.Kind == t.Kind && old.Phase == "queued" {
						t.Phase = "skipped_busy"
					}
				}
			}
			if t.Phase == "queued" {
				for _, old := range s.Runs {
					if old.Phase == "unknown" || old.Phase == "started" {
						t.Phase = "skipped_busy"
					}
				}
			}
			if t.Phase == "queued" {
				inv, err := enqueue(tx, t)
				if err != nil {
					return err
				}
				if inv.EventID != t.ID || inv.Binding != t.Binding || inv.RunID == "" || inv.InputSHA == "" {
					return ErrUnsafe
				}
				t.QueuedRunID = inv.RunID
				copyInv := inv
				t.Queued = &copyInv
			}
			s.Triggers[id] = t
			if !nextDue.IsZero() {
				next := t
				next.Due = nextDue
				next.ID = "service:" + digest([]any{id, nextDue})
				next.Phase, next.QueuedRunID = "ready", ""
				next.Queued = nil
				s.Triggers[next.ID] = next
			}
		}
		return nil
	})
}
func (o *Owner) Stop(ctx context.Context) error {
	select {
	case o.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-o.gate }()
	return o.opts.Journal.Update(ctx, func(_ *sql.Tx, s *State) error {
		for id, t := range s.Triggers {
			if t.Phase == "ready" {
				t.Phase = "canceled"
				s.Triggers[id] = t
			}
		}
		return nil
	})
}
func (o *Owner) Outcome(ctx context.Context, inv Invocation) (json.RawMessage, bool, error) {
	s, err := o.opts.Journal.Read(ctx)
	if err != nil {
		return nil, false, err
	}
	r, ok := s.Runs[inv.RunID]
	if !ok || r.Invocation != inv || r.Phase != "confirmed" {
		return nil, false, nil
	}
	return append(json.RawMessage(nil), r.Result...), true, nil
}
func (e *execution) fault(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	e.hostErr = errors.Join(e.hostErr, err)
	e.mu.Unlock()
}

// SaveConfig is a real source effect boundary. Later notices, native or file
// failures never roll back an already committed source configuration rewrite.
func (e *execution) SaveConfig(ctx context.Context, c map[string]any) error {
	e.ordinal++
	effect := e.scoped(Effect{ID: e.inv.RunID + ":config:" + fmt.Sprint(e.ordinal), Kind: "config_write", Phase: "confirmed", Args: raw(c)})
	_, release, err := e.o.opts.Core.Guard(ctx, e.inv, effect)
	if err != nil {
		e.fault(err)
		return err
	}
	if release == nil {
		return ErrUnsafe
	}
	defer release()
	publication := ConfigPublication{}
	next := Binding{}
	err = e.o.opts.Journal.Update(ctx, func(tx *sql.Tx, s *State) error {
		r, ok := s.Runs[e.inv.RunID]
		if !ok || r.Invocation != e.inv || r.Phase != "started" || r.Effective != e.effective || r.ConfigReceipt != e.configReceipt {
			return ErrUnsafe
		}
		if _, exists := s.Effects[effect.ID]; exists {
			return ErrAlready
		}
		for _, tr := range s.Triggers {
			if tr.Phase == "queued" && tr.QueuedRunID != e.inv.RunID && tr.Binding == e.effective {
				if tr.Queued == nil || tr.Queued.RunID != tr.QueuedRunID || tr.Queued.Binding != tr.Binding {
					return ErrUnsafe
				}
				if _, entered := s.Runs[tr.QueuedRunID]; entered {
					return ErrUnsafe
				}
				effect.Queued = append(effect.Queued, *tr.Queued)
			}
		}
		sort.Slice(effect.Queued, func(i, j int) bool { return effect.Queued[i].RunID < effect.Queued[j].RunID })
		var err error
		publication, err = e.o.opts.Core.Config(ctx, tx, e.inv, effect)
		next = publication.Binding
		if err != nil {
			return err
		}
		if next.InstanceID != e.effective.InstanceID || next.Generation != e.effective.Generation || next.Revision <= e.effective.Revision {
			return ErrUnsafe
		}
		s.ConfigHistory = append(s.ConfigHistory, ConfigWrite{RunID: e.inv.RunID, Before: append(json.RawMessage(nil), s.Config...), After: append(json.RawMessage(nil), effect.Args...)})
		s.Config = append(json.RawMessage(nil), effect.Args...)
		s.Binding = next
		r.Effective, r.ConfigReceipt = next, effect.ID
		s.Runs[e.inv.RunID] = r
		if len(publication.Rebound) != len(effect.Queued) {
			return ErrUnsafe
		}
		rebound := map[string]Invocation{}
		for _, ri := range publication.Rebound {
			if _, exists := rebound[ri.RunID]; exists {
				return ErrUnsafe
			}
			rebound[ri.RunID] = ri
		}
		for _, before := range effect.Queued {
			after, ok := rebound[before.RunID]
			want := before
			want.Binding = next
			if !ok || after != want {
				return ErrUnsafe
			}
		}
		for id, tr := range s.Triggers {
			if ri, ok := rebound[tr.QueuedRunID]; ok {
				copyInv := ri
				tr.Binding = next
				tr.Queued = &copyInv
				s.Triggers[id] = tr
			}
		}
		effect.Result = raw(publication)
		s.Effects[effect.ID] = effect
		for id, tr := range s.Triggers {
			if tr.Phase == "ready" && tr.Binding == e.effective {
				tr.Binding = next
				s.Triggers[id] = tr
			}
		}
		if e.inv.Action == "init" || e.inv.Action == "reload" {
			for _, scheduler := range e.machine.Schedulers {
				for _, job := range scheduler.Jobs {
					if job.Trigger == "date" {
						id := "once:" + e.inv.RunID
						s.Triggers[id] = Trigger{ID: id, Kind: "once", Phase: "ready", Binding: next, Due: time.Unix(0, int64(job.RunAt*1e9)).UTC()}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		// A commit may have succeeded even if its reply was lost. Read back only this
		// immutable effect; never issue the configuration mutation again.
		checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		state, readErr := e.o.opts.Journal.Read(checkCtx)
		saved, ok := state.Effects[effect.ID]
		if readErr != nil || !ok || saved.Kind != "config_write" || saved.Phase != "confirmed" || saved.Binding != e.inv.Binding || saved.Authority != e.effective || saved.ConfigReceipt != e.configReceipt || !bytes.Equal(saved.Args, effect.Args) || json.Unmarshal(saved.Result, &publication) != nil {
			e.fault(ErrUnknown)
			return errors.Join(ErrUnknown, err)
		}
	}
	next = publication.Binding
	e.effective, e.configReceipt = next, effect.ID
	return nil
}

// Notice means durable local acceptance only, never external delivery. A later
// source failure cannot erase or retroactively block an accepted prefix notice.
func (e *execution) Notice(ctx context.Context, n map[string]any) error {
	e.ordinal++
	effect := e.scoped(Effect{ID: e.inv.RunID + ":notice:" + fmt.Sprint(e.ordinal), Kind: "notice", Phase: "confirmed", Args: raw(n)})
	notice := Notice{ID: effect.ID, RunID: e.inv.RunID, State: "pending", Payload: effect.Args}
	_, release, err := e.o.opts.Core.Guard(ctx, e.inv, effect)
	if err != nil {
		return err
	}
	if release == nil {
		return ErrUnsafe
	}
	defer release()
	return e.o.opts.Journal.Update(ctx, func(tx *sql.Tx, s *State) error {
		r, ok := s.Runs[e.inv.RunID]
		if !ok || r.Invocation != e.inv || r.Phase != "started" || r.Effective != e.effective || r.ConfigReceipt != e.configReceipt {
			return ErrUnsafe
		}
		if _, ok := s.Effects[effect.ID]; ok {
			return ErrAlready
		}
		if err := e.o.opts.Core.Notice(ctx, tx, e.inv, notice); err != nil {
			return err
		}
		s.Notices[notice.ID] = notice
		s.Effects[effect.ID] = effect
		return nil
	})
}
func (e *execution) Log(context.Context, string, string) {} // No raw tracker body/name/credential logging.

func (e *execution) persist(fn func(*sql.Tx, *State) error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(e.ctx), 3*time.Second)
	defer cancel()
	return e.o.opts.Journal.Update(ctx, fn)
}
