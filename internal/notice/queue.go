package notice

import (
	"context"
	"sync"
	"time"
)

// Call is the original queue's args/kwargs envelope; values must be JSON-shaped.
type Call struct {
	Args   []any  `json:"args"`
	Kwargs Object `json:"kwargs"`
}
type Attempt struct {
	Queued        bool
	Attempted     bool
	CallbackError error
}
type SendCallback func(context.Context, Call) error

// Queue models source FIFO/window behavior without creating a daemon or sender.
// The trusted host must own ticking, cancellation, persistence and restart policy.
// Attempted is not delivered: source catches sender exceptions and consumes rows.
type Queue struct {
	mu       sync.Mutex
	drain    sync.Mutex
	periods  []Period
	pending  []Call
	running  bool
	now      func() time.Time
	stopped  func() bool
	callback SendCallback
}

func NewQueue(periods []Period, now func() time.Time, stopped func() bool, callback SendCallback) (*Queue, error) {
	p, err := copyPeriods(periods)
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	if stopped == nil {
		stopped = func() bool { return false }
	}
	return &Queue{periods: p, running: true, now: now, stopped: stopped, callback: callback, pending: []Call{}}, nil
}
func cloneCall(in Call) (Call, error) {
	v, err := jsonCopy(in)
	if err != nil {
		return Call{}, err
	}
	o := v.(map[string]any)
	args, _ := o["args"].([]any)
	if args == nil {
		args = []any{}
	}
	kw, _ := o["kwargs"].(map[string]any)
	if kw == nil {
		kw = Object{}
	}
	return Call{args, kw}, nil
}
func (q *Queue) SetPeriods(periods []Period) error {
	p, err := copyPeriods(periods)
	if err != nil {
		return err
	}
	q.mu.Lock()
	q.periods = p
	q.mu.Unlock()
	return nil
}
func (q *Queue) allowed(at time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return Allowed(q.periods, at)
}
func (q *Queue) Send(ctx context.Context, call Call) (Attempt, error) {
	call, err := cloneCall(call)
	if err != nil {
		return Attempt{}, err
	}
	immediate := call.Kwargs["immediately"]
	delete(call.Kwargs, "immediately")
	if truth(immediate) || q.allowed(q.now()) {
		return q.send(ctx, call), nil
	}
	q.mu.Lock()
	q.pending = append(q.pending, call)
	q.mu.Unlock()
	return Attempt{Queued: true}, nil
}

// AsyncSend has the same decision semantics as actual async_send_message; Go's
// caller goroutine supplies concurrency rather than a Python executor.
func (q *Queue) AsyncSend(ctx context.Context, call Call) (Attempt, error) { return q.Send(ctx, call) }
func (q *Queue) send(ctx context.Context, call Call) Attempt {
	if q.callback == nil {
		return Attempt{}
	}
	return Attempt{Attempted: true, CallbackError: q.callback(ctx, call)}
}

// Tick is one monitor iteration; sender errors do not retry or stop FIFO drain.
// No clock wait occurs here. Stop stops monitoring, like the source stop event;
// explicit Send after Stop remains source-compatible and can still attempt send.
func (q *Queue) Tick(ctx context.Context) []Attempt {
	q.drain.Lock()
	defer q.drain.Unlock()
	out := []Attempt{}
	q.mu.Lock()
	running := q.running
	q.mu.Unlock()
	if !running || !q.allowed(q.now()) {
		return out
	}
	for {
		q.mu.Lock()
		nonempty := len(q.pending) > 0
		q.mu.Unlock()
		if !nonempty || q.stopped() || !q.allowed(q.now()) {
			break
		}
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.mu.Unlock()
			break
		}
		call := q.pending[0]
		q.pending[0] = Call{}
		q.pending = q.pending[1:]
		q.mu.Unlock()
		out = append(out, q.send(ctx, call))
	}
	return out
}
func (q *Queue) Pending() []Call {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Call, len(q.pending))
	for i, c := range q.pending {
		out[i], _ = cloneCall(c)
	}
	return out
}
func (q *Queue) Stop() { q.mu.Lock(); q.running = false; q.mu.Unlock() }
