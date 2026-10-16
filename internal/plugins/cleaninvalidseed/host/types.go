// Package seedhost implements the bound local cleanup host; application
// authorization, lifecycle publication and SQL persistence are separate adapters.
package seedhost

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/download"
	seed "light-media/internal/plugins/cleaninvalidseed"
	"light-media/internal/plugins/schedule"
)

var (
	ErrUnsafe  = errors.New("cleanup scope denied")
	ErrUnknown = errors.New("cleanup effect outcome requires reconciliation")
	ErrAlready = errors.New("cleanup invocation already entered; execution forbidden")
)

type Binding struct {
	InstanceID           string
	Generation, Revision int64
}
type Invocation struct {
	Binding                          Binding
	RunID, Action, EventID, InputSHA string
}
type Downloader struct {
	ID, Name, Kind, ConfigSHA string
	Revision                  int64
	Inactive                  bool
	client                    *download.Client
	seal                      string
}
type Root struct {
	ID, Path, Role string
	Stamp          Stamp
}
type Stamp struct {
	Dev, Inode  uint64
	Size, ModNS int64
	Mode        uint32
}
type Pin struct {
	RootID, Path, Parent string
	File, ParentFile     Stamp
}
type FileFact struct {
	Pin   Pin
	Proof json.RawMessage
}
type Effect struct {
	Queued                                                                    []Invocation
	NativeSnapshot                                                            json.RawMessage
	ExpectedCore                                                              map[string]json.RawMessage
	ID, RunID, Kind, Phase, Operation, DownloaderID, DownloaderSHA, ErrorCode string
	Binding                                                                   Binding
	Authority                                                                 Binding
	ConfigReceipt                                                             string
	DownloaderRevision                                                        int64
	Args                                                                      json.RawMessage
	Pin                                                                       *Pin
	Manifest                                                                  []Pin
	Proof                                                                     json.RawMessage
	Stage, StageFrom                                                          string
	Facts                                                                     []FileFact
	Result                                                                    json.RawMessage
}
type Run struct {
	Invocation       Invocation
	Effective        Binding
	ConfigReceipt    string
	Phase, ErrorCode string
	Config           json.RawMessage
	Result           json.RawMessage
}
type ConfigPublication struct {
	Binding Binding
	Rebound []Invocation
}
type Trigger struct {
	// OccurrenceID is an opaque private Core schedule proof, never worker input.
	OccurrenceID                         string
	Queued                               *Invocation
	ID, Kind, Action, Phase, QueuedRunID string
	Binding                              Binding
	Due                                  time.Time
	Spec                                 *schedule.Spec
	Command                              map[string]any
}
type Notice struct {
	ID, RunID, State string
	Payload          json.RawMessage
}
type ConfigWrite struct {
	RunID         string
	Before, After json.RawMessage
}
type State struct {
	Binding       Binding
	Config        json.RawMessage
	Runs          map[string]Run
	Effects       map[string]Effect
	Triggers      map[string]Trigger
	Notices       map[string]Notice
	ConfigHistory []ConfigWrite
}

func initialize(s *State) {
	if s.Runs == nil {
		s.Runs = map[string]Run{}
	}
	if s.Effects == nil {
		s.Effects = map[string]Effect{}
	}
	if s.Triggers == nil {
		s.Triggers = map[string]Trigger{}
	}
	if s.Notices == nil {
		s.Notices = map[string]Notice{}
	}
}

type Journal interface {
	Read(context.Context) (State, error)
	Update(context.Context, func(*sql.Tx, *State) error) error
}

// Guard must acquire real lifecycle/file-write leases and verify the actual
// running invocation, current core configuration and exact downloader/root plan.
// Proof is frozen before the action. Commit executes in the journal SQLite tx;
// it may not delete files, call daemons or send messages itself.
type Core interface {
	Pin(context.Context, Invocation, Pin) (json.RawMessage, error)
	Guard(context.Context, Invocation, Effect) (proof json.RawMessage, release func(), err error)
	Commit(context.Context, *sql.Tx, Invocation, Effect) error
	Config(context.Context, *sql.Tx, Invocation, Effect) (ConfigPublication, error)
	Notice(context.Context, *sql.Tx, Invocation, Notice) error
}
type Options struct {
	// BeforeRun is trusted native admission, called only after the internal
	// action gate is held. It refreshes clients/roots while retaining the live
	// source object. release retains the actual Core action lease through finish;
	// worker JSON can neither provide the callback nor construct its lease.
	BeforeRun   func(context.Context, Invocation) (Options, func(), error)
	Binding     Binding
	Journal     Journal
	Core        Core
	Downloaders []Downloader
	Roots       []Root
	Now         func() time.Time
	Timezone    string
}
type Result struct {
	Skipped      string `json:"skipped,omitempty"`
	ServiceError string `json:"service_error,omitempty"`
	SourceError  string `json:"source_error,omitempty"`
	HostError    string `json:"host_error,omitempty"`
	Effects      int    `json:"effects"`
}

var _ seed.Host = (*execution)(nil)

// BindDownloader is the sole constructor and must receive the exact trusted
// configured instance. No worker can pass a URL, credentials or Client pointer.
func BindDownloader(name string, revision int64, fingerprint string, cfg download.Config) (Downloader, error) {
	client, err := download.New(cfg)
	if err != nil {
		return Downloader{}, err
	}
	kind := cfg.Kind
	if kind == "qb" {
		kind = "qbittorrent"
	}
	if kind == "tr" {
		kind = "transmission"
	}
	d := Downloader{ID: cfg.ID, Name: name, Kind: kind, Revision: revision, ConfigSHA: fingerprint, client: client}
	d.seal = digest([]any{d.ID, d.Name, d.Kind, d.Revision, d.ConfigSHA})
	return d, nil
}
