package durable

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrConflict  = errors.New("signin journal conflict")
	ErrStale     = errors.New("signin instance binding stale")
	ErrUncertain = errors.New("signin external operation requires reconciliation")
	ErrCapacity  = errors.New("signin journal capacity exceeded")
	ErrStore     = errors.New("signin durable store failed")
	ErrBusy      = errors.New("signin instance already owned")
)

type Scope struct {
	Instance             string
	Implementation       string
	Generation, Revision uint64
}
type Binding struct {
	Scope
	Epoch string
}
type Baseline struct {
	Runtime, History, Inventory, Config json.RawMessage
	Timezone                            string
	Keys                                []string
}
type Entry struct {
	Kind, Digest, Phase string
	Value               json.RawMessage
	Error               string
	ErrorClass          string
	Guard               string
}
type Run struct {
	ID, Action, Epoch, Phase string
	Plan                     json.RawMessage
	Baseline                 Baseline
	Lanes                    map[string][]Entry
	Scope                    Scope
	FinalRuntime             json.RawMessage
	Result                   json.RawMessage
}
type OutboxItem struct {
	ID, Kind, Phase string
	Payload         json.RawMessage
	RunID, Lane     string
	Index           int
}
type Statistic struct {
	Success, Fail int
	Seconds       *int
	LastState     int
	LastDate      string
	Note          map[string]int
}
type State struct {
	Schema          int
	ActiveRun       string
	RuntimeEpoch    string
	Scope           Scope
	Epoch           string
	Config, Runtime json.RawMessage
	Services        json.RawMessage
	History         map[string]json.RawMessage
	Keys            []string
	Cache           map[string][]byte
	CacheReady      map[string]bool
	Stats           map[string]Statistic
	Runs            map[string]*Run
	Blocks          map[string]string
	Outbox          []OutboxItem
	Jobs            map[string]json.RawMessage
}
type Mutation struct {
	Kind, Key string
	Value     json.RawMessage
}
type Effect struct {
	RunID, Lane, Kind, Digest string
	Index                     int
	Value                     json.RawMessage
	Mutation                  Mutation
	KnownError, ErrorClass    string
}
type NetworkIntent struct {
	RunID, Lane, Kind, Digest, Guard string
	Index                            int
}
type Journal interface {
	FinishSourceFailure(context.Context, Binding, string, json.RawMessage, json.RawMessage, SourceIdentity) error
	BeginSource(context.Context, Binding, SourceRequest) (Entry, bool, error)
	CompleteSource(context.Context, Binding, SourceRequest, SourceResult) error
	UnknownSource(context.Context, Binding, SourceRequest) error
	AcquireExecution(context.Context, Binding) (func(), error)
	State(context.Context, Binding) (State, error)
	Start(context.Context, Binding, string, string, json.RawMessage, Baseline) (Run, error)
	Apply(context.Context, Binding, Effect) (Entry, error)
	BeginNetwork(context.Context, Binding, NetworkIntent) (Entry, bool, error)
	CompleteNetwork(context.Context, Binding, NetworkIntent, json.RawMessage, string) error
	Unknown(context.Context, Binding, NetworkIntent) error
	Finish(context.Context, Binding, string, json.RawMessage, json.RawMessage, string) error
	Reconcile(context.Context, Binding, string, string, int, json.RawMessage) error
}

func marshal(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("non JSON internal fixture value")
	}
	return raw
}
func copyJSON[T any](value T) T {
	raw := marshal(value)
	var out T
	if json.Unmarshal(raw, &out) != nil {
		panic("invalid internal copy")
	}
	return out
}

func at(r *Run, lane string, index int) (Entry, bool, error) {
	entries := r.Lanes[lane]
	if index < 0 || index > len(entries) {
		return Entry{}, false, ErrConflict
	}
	if index < len(entries) {
		return entries[index], true, nil
	}
	return Entry{}, false, nil
}
