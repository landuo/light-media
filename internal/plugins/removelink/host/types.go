package cleanuphost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"light-media/internal/plugins/removelink"
)

var (
	ErrUnsafe      = errors.New("cleanup path or identity is no longer authorized")
	ErrUncertain   = errors.New("cleanup side effect requires reconciliation; not retried")
	ErrUnsupported = errors.New("cleanup storage or watcher is not implemented")
	ErrClosed      = errors.New("cleanup host is closed")
	ErrOrder       = errors.New("older cleanup event must commit first")
	ErrTransition  = errors.New("cleanup lifecycle transition is pending")
)

type Binding struct {
	InstanceID           string
	Generation, Revision int64
}
type Root struct{ ID, Path, Role string }
type Stamp struct {
	Dev, Inode  uint64
	Size, ModNS int64
	Mode        uint32
}
type Pin struct {
	RootID, ParentPath string
	Parent             Stamp
	File               Stamp
	Epoch              uint64
}
type RootStamp struct {
	Root Root
	File Stamp
}
type Event struct {
	ID, Kind, Path, Dest, Mode string
	Directory                  bool
	At                         time.Time
}
type Effect struct {
	ID, Scope, Kind, Path, Role string
	Binding                     Binding
	Expected                    Pin
	Stage, State, Error         string
	Payload                     json.RawMessage
	Result                      bool
}
type EventReceipt struct {
	Event        Event
	Binding      Binding
	Sequence     uint64
	State, Error string
}
type MachineState struct {
	State    []removelink.Tracked
	Queue    []*removelink.Task
	TimerDue *time.Time
	Watches  []removelink.Watch
}
type Durable struct {
	Binding     Binding
	Roots       []RootStamp
	Config      json.RawMessage
	Machine     MachineState
	Pins        map[string]Pin
	Frozen      map[string]Pin
	Serial      uint64
	EventSerial uint64
	Events      map[string]EventReceipt
	Effects     map[string]Effect
	Active      *Event
	Blocked     string
	Transition  *Transition
}

// Update must atomically preserve machine state and receipts. Implementations
// must not report success before a durable commit. App can map this to SQLite.
type Journal interface {
	Read(context.Context) (Durable, error)
	Update(context.Context, func(*Durable) error) error
}

// Effects must commit an idempotency receipt together with each core mutation
// or durable outbox insertion. Sending a notification is not an outbox commit.
// Unknown outcomes return ErrUncertain, never success and never blind replay.
type Effects interface {
	Commit(context.Context, Effect) (bool, error)
}
type Watcher interface {
	Events() <-chan Event
	Errors() <-chan error
	Close() error
}
type WatchFactory func(context.Context, []removelink.Watch, func(string) (*os.File, error)) (Watcher, error)
type Options struct {
	Binding Binding
	Roots   []Root
	Journal Journal
	Effects Effects
	Guard   func(context.Context, Binding, string) (func(), error)
	// AfterAction runs before successful completion is checkpointed, while the
	// exact Guard lease is still held. App owners must provide it to reconcile
	// confirmed filesystem facts, including when all optional effects are off.
	// Nil preserves the standalone host contract for non-App consumers.
	AfterAction     func(context.Context, Binding, string) error
	Watch           WatchFactory
	Now             func() time.Time
	MediaExtensions []string
	AsyncTimeout    time.Duration
	// Fault injection is private to isolated fixture tests and not worker input.
	fault func(string, string) error
}

func initDurable(s *Durable) {
	if s.Pins == nil {
		s.Pins = map[string]Pin{}
	}
	if s.Frozen == nil {
		s.Frozen = map[string]Pin{}
	}
	if s.Events == nil {
		s.Events = map[string]EventReceipt{}
	}
	if s.Effects == nil {
		s.Effects = map[string]Effect{}
	}
}
