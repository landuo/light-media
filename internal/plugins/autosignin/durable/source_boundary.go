package durable

import (
	"bytes"
	"context"
	"encoding/json"
	source "light-media/internal/plugins/autosignin"
	"sort"
	"strings"
	"time"
)

const (
	SourceProtocolVersion = 1
	SourceNormal          = "normal"
	SourceException       = "exception"
	SourceUnknown         = "unknown"
)

// SourceConfigIdentity is supplied only by the trusted host after checking the
// configuration admission chain. Digest is a private HMAC, not plaintext config.
type SourceConfigIdentity struct {
	Revision uint64
	Digest   string
}
type SourceIdentity struct {
	Version                                 int
	Scope                                   Scope
	Epoch, RunID, Lane, Kind, PayloadDigest string
	Index                                   int
	Config                                  SourceConfigIdentity
}
type SourceRequest struct {
	Identity SourceIdentity
	Payload  json.RawMessage
}
type SourceResult struct {
	Identity                   SourceIdentity
	Outcome                    string
	ErrorClass, Error, ProofID string
}
type SourceEnvelope struct {
	Version int
	Request SourceRequest
	Result  *SourceResult `json:",omitempty"`
}

// SourcePort is a trusted host capability, not a worker-selected callback.
// Invoke performs the synchronous source boundary, not downstream event work.
// Its error is NOT evidence of source outcome. ReadResult authenticates an
// independent persisted host proof AND current authority; a network receipt,
// journal checkpoint or outbox ACK is not proof. Bind runs only for NEW intents.
type SourcePort interface {
	Bind(context.Context, Binding, string) (SourceConfigIdentity, error)
	Invoke(context.Context, Binding, SourceRequest) error
	ReadResult(context.Context, Binding, SourceIdentity) (SourceResult, bool, error)
}

// A foreign host callback panic is unknown, never a synthetic source exception.
func readSourceResult(ctx context.Context, port SourcePort, b Binding, identity SourceIdentity) (result SourceResult, found bool, err error) {
	defer func() {
		if recover() != nil {
			result = SourceResult{}
			found = false
			err = ErrUncertain
		}
	}()
	return port.ReadResult(ctx, b, identity)
}

func sourceKind(kind string) bool { return strings.HasPrefix(kind, "source.") }
func ValidateSourceRequest(r SourceRequest) error {
	i := r.Identity
	if i.Version != SourceProtocolVersion || (i.Kind != "source.notify" && i.Kind != "source.emit") || i.Scope.Instance == "" || i.Scope.Implementation == "" || i.Scope.Generation == 0 || i.Scope.Revision == 0 || i.Epoch == "" || i.RunID == "" || i.Lane == "" || i.Index < 0 || i.PayloadDigest == "" || i.Config.Revision < i.Scope.Revision || i.Config.Digest == "" || !json.Valid(r.Payload) {
		return ErrConflict
	}
	return nil
}
func ValidateSourceResult(request SourceRequest, result SourceResult) error {
	if ValidateSourceRequest(request) != nil || result.Identity != request.Identity || result.ProofID == "" {
		return ErrConflict
	}
	switch result.Outcome {
	case SourceNormal:
		if result.ErrorClass != "" || result.Error != "" {
			return ErrConflict
		}
	case SourceException:
		if result.ErrorClass == "" {
			return ErrConflict
		}
	case SourceUnknown:
		if result.ErrorClass != "" || result.Error != "" {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	return nil
}
func sourceGuard(i SourceIdentity) string { return "source:" + string(marshal(i)) }
func sourceEntry(r SourceRequest) Entry {
	return Entry{Kind: r.Identity.Kind, Digest: r.Identity.PayloadDigest, Guard: sourceGuard(r.Identity), Phase: "intent", Value: marshal(SourceEnvelope{Version: SourceProtocolVersion, Request: r})}
}
func sourceEnvelope(e Entry) (SourceEnvelope, error) {
	var v SourceEnvelope
	d := json.NewDecoder(bytes.NewReader(e.Value))
	d.DisallowUnknownFields()
	if !json.Valid(e.Value) || d.Decode(&v) != nil || v.Version != SourceProtocolVersion || ValidateSourceRequest(v.Request) != nil {
		return v, ErrStore
	}
	expected := sourceEntry(v.Request)
	if e.Kind != expected.Kind || e.Digest != expected.Digest || e.Guard != expected.Guard || e.Error != "" || e.ErrorClass != "" {
		return v, ErrStore
	}
	switch e.Phase {
	case "intent", "uncertain":
		if v.Result != nil {
			return v, ErrStore
		}
	case "receipt":
		if v.Result == nil || ValidateSourceResult(v.Request, *v.Result) != nil || v.Result.Outcome == SourceUnknown {
			return v, ErrStore
		}
	default:
		return v, ErrStore
	}
	return v, nil
}
func validateSourceRun(r *Run, request SourceRequest) error {
	i := request.Identity
	if ValidateSourceRequest(request) != nil || r.ID != i.RunID || r.Scope != i.Scope || r.Epoch != i.Epoch {
		return ErrConflict
	}
	return nil
}
func (j *StateJournal) BeginSource(ctx context.Context, b Binding, request SourceRequest) (Entry, bool, error) {
	var out Entry
	exists := false
	err := j.backend.Update(ctx, b, func(s *State) error {
		r, err := getRun(s, b, request.Identity.RunID)
		if err != nil {
			return err
		}
		if err = validateSourceRun(r, request); err != nil {
			return err
		}
		old, found, err := at(r, request.Identity.Lane, request.Identity.Index)
		if err != nil {
			return err
		}
		if found {
			v, err := sourceEnvelope(old)
			if err != nil {
				return ErrConflict
			}
			if !bytes.Equal(marshal(v.Request), marshal(request)) {
				return ErrConflict
			}
			out, exists = copyJSON(old), true
			return ErrNoChange
		}
		if s.ActiveRun != r.ID || !activeRunPhase(r.Phase) {
			return ErrConflict
		}
		out = sourceEntry(request)
		if s.Blocks[out.Guard] != "" {
			return ErrUncertain
		}
		r.Lanes[request.Identity.Lane] = append(r.Lanes[request.Identity.Lane], out)
		return nil
	})
	return out, exists, err
}

// CompleteSource accepts only results obtained by the trusted controller from
// SourcePort.ReadResult. Like the other Journal writers this is host-private,
// never an externally exposed reconciliation endpoint.
func (j *StateJournal) CompleteSource(ctx context.Context, b Binding, request SourceRequest, result SourceResult) error {
	if ValidateSourceResult(request, result) != nil || result.Outcome == SourceUnknown {
		return ErrConflict
	}
	return j.backend.Update(ctx, b, func(s *State) error {
		r, err := getRun(s, b, request.Identity.RunID)
		if err != nil {
			return err
		}
		if err = validateSourceRun(r, request); err != nil {
			return err
		}
		old, found, err := at(r, request.Identity.Lane, request.Identity.Index)
		if err != nil || !found {
			return ErrConflict
		}
		v, err := sourceEnvelope(old)
		if err != nil || !bytes.Equal(marshal(v.Request), marshal(request)) {
			return ErrConflict
		}
		if old.Phase == "receipt" {
			if !bytes.Equal(marshal(v.Result), marshal(result)) {
				return ErrConflict
			}
			return ErrNoChange
		}
		if s.ActiveRun != r.ID || !activeRunPhase(r.Phase) {
			return ErrConflict
		}
		v.Result = &result
		old.Phase = "receipt"
		old.Value = marshal(v)
		r.Lanes[request.Identity.Lane][request.Identity.Index] = old
		delete(s.Blocks, old.Guard)
		return nil
	})
}
func (j *StateJournal) UnknownSource(ctx context.Context, b Binding, request SourceRequest) error {
	return j.backend.Update(ctx, b, func(s *State) error {
		r, err := getRun(s, b, request.Identity.RunID)
		if err != nil {
			return err
		}
		if err = validateSourceRun(r, request); err != nil {
			return err
		}
		old, found, err := at(r, request.Identity.Lane, request.Identity.Index)
		if err != nil || !found {
			return ErrConflict
		}
		v, err := sourceEnvelope(old)
		if err != nil || !bytes.Equal(marshal(v.Request), marshal(request)) {
			return ErrConflict
		}
		if old.Phase == "receipt" {
			return ErrNoChange
		}
		if s.ActiveRun != r.ID || !activeRunPhase(r.Phase) {
			return ErrConflict
		}
		if old.Phase == "uncertain" {
			return ErrNoChange
		}
		old.Phase = "uncertain"
		r.Lanes[request.Identity.Lane][request.Identity.Index] = old
		s.Blocks[old.Guard] = r.ID
		return nil
	})
}

// Source preserves the Engine's synchronous call order. A journal receipt is
// checked against the independent host proof even when replaying.
func (l *Lane) Source(ctx context.Context, kind string, payload any, port SourcePort) error {
	if err := l.Fault(); err != nil {
		return err
	}
	if port == nil {
		return l.stop(ErrStale)
	}
	e := l.execution
	request := SourceRequest{Identity: SourceIdentity{Version: SourceProtocolVersion, Scope: e.Binding.Scope, Epoch: e.Epoch, RunID: e.RunID, Lane: l.Name, Index: l.index, Kind: kind, PayloadDigest: l.digest(kind, payload)}, Payload: marshal(payload)}
	old, exists, err := readEntry(ctx, e.Store, e.Binding, e.RunID, l.Name, l.index)
	if err != nil {
		return l.stop(err)
	}
	if exists {
		v, err := sourceEnvelope(old)
		if err != nil {
			return l.stop(ErrConflict)
		}
		i := v.Request.Identity
		if i.Scope != request.Identity.Scope || i.Epoch != request.Identity.Epoch || i.RunID != e.RunID || i.Lane != l.Name || i.Index != l.index || i.Kind != kind || i.PayloadDigest != request.Identity.PayloadDigest || !bytes.Equal(v.Request.Payload, request.Payload) {
			return l.stop(ErrConflict)
		}
		request = v.Request
	} else {
		config, err := port.Bind(ctx, e.Binding, e.RunID)
		if err != nil {
			return l.stop(err)
		}
		request.Identity.Config = config
	}
	old, replay, err := e.Store.BeginSource(ctx, e.Binding, request)
	if err != nil {
		return l.stop(err)
	}
	unknown := func() error {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = e.Store.UnknownSource(cleanup, e.Binding, request)
		return l.stop(ErrUncertain)
	}
	if !replay {
		panicked := true
		func() {
			defer func() { _ = recover() }()
			_ = port.Invoke(ctx, e.Binding, request)
			panicked = false
		}()
		if panicked {
			return unknown()
		}
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	result, found, err := readSourceResult(readCtx, port, e.Binding, request.Identity)
	if err != nil || !found || ValidateSourceResult(request, result) != nil || result.Outcome == SourceUnknown {
		return unknown()
	}
	if replay && old.Phase == "receipt" {
		v, err := sourceEnvelope(old)
		if err != nil || !bytes.Equal(marshal(v.Result), marshal(result)) {
			return unknown()
		}
	}
	if err = e.Store.CompleteSource(readCtx, e.Binding, request, result); err != nil {
		return unknown()
	}
	// Proof is not authority to continue after cancellation.
	if ctx.Err() != nil {
		return l.stop(ctx.Err())
	}
	l.index++
	if result.Outcome == SourceException {
		failure := &source.SourceFailure{Class: result.ErrorClass, Message: result.Error}
		e.mu.Lock()
		e.sourceFailure = failure
		e.sourceFailureIdentity = request.Identity
		e.mu.Unlock()
		return failure
	}
	return nil
}

// SourceFailureFinal is stored only in Run.Result for failed_source. Keeping
// this tagged payload avoids changing old run-head or Entry canonical cells.
type SourceFailureFinal struct {
	Version int
	Result  json.RawMessage
	Failure SourceIdentity
}

func (j *StateJournal) FinishSourceFailure(ctx context.Context, b Binding, id string, runtime, result json.RawMessage, identity SourceIdentity) error {
	return j.backend.Update(ctx, b, func(s *State) error {
		r, err := getRun(s, b, id)
		if err != nil {
			return err
		}
		if identity.RunID != id || identity.Scope != r.Scope || identity.Epoch != r.Epoch || identity.Index != len(r.Lanes[identity.Lane])-1 {
			return ErrConflict
		}
		entry, found, err := at(r, identity.Lane, identity.Index)
		if err != nil || !found {
			return ErrConflict
		}
		v, err := sourceEnvelope(entry)
		if err != nil || entry.Phase != "receipt" || v.Request.Identity != identity || v.Result.Outcome != SourceException {
			return ErrConflict
		}
		final := marshal(SourceFailureFinal{Version: SourceProtocolVersion, Result: result, Failure: identity})
		if r.Phase == "failed_source" {
			if bytes.Equal(r.Result, final) && bytes.Equal(r.FinalRuntime, runtime) {
				return ErrNoChange
			}
			return ErrConflict
		}
		if s.ActiveRun != id || !activeRunPhase(r.Phase) {
			return ErrConflict
		}
		for _, entries := range r.Lanes {
			for _, e := range entries {
				if e.Phase == "intent" || e.Phase == "uncertain" {
					return ErrUncertain
				}
			}
		}
		r.Phase = "failed_source"
		r.Result = final
		r.FinalRuntime = runtime
		s.Runtime = runtime
		s.RuntimeEpoch = b.Epoch
		s.ActiveRun = ""
		return nil
	})
}

// ReplaySourceFailure revalidates the independent proof before returning the
// exact typed source exception. It never invokes the source boundary again.
func ReplaySourceFailure(ctx context.Context, j Journal, b Binding, run Run, port SourcePort) (json.RawMessage, error) {
	if run.Phase != "failed_source" || port == nil {
		return nil, ErrUncertain
	}
	var final SourceFailureFinal
	d := json.NewDecoder(bytes.NewReader(run.Result))
	d.DisallowUnknownFields()
	if !json.Valid(run.Result) || d.Decode(&final) != nil || final.Version != SourceProtocolVersion || !json.Valid(final.Result) || final.Failure.RunID != run.ID || final.Failure.Scope != run.Scope || final.Failure.Epoch != run.Epoch {
		return nil, ErrStore
	}
	i := final.Failure
	entry, found, err := readEntry(ctx, j, b, run.ID, i.Lane, i.Index)
	if err != nil || !found {
		return nil, ErrUncertain
	}
	v, err := sourceEnvelope(entry)
	if err != nil || entry.Phase != "receipt" || v.Request.Identity != i || v.Result.Outcome != SourceException {
		return nil, ErrUncertain
	}
	result, found, err := readSourceResult(ctx, port, b, i)
	if err != nil || !found || ValidateSourceResult(v.Request, result) != nil || !bytes.Equal(marshal(v.Result), marshal(result)) {
		return nil, ErrUncertain
	}
	return final.Result, &source.SourceFailure{Class: result.ErrorClass, Message: result.Error}
}

// A terminal snapshot is not independent proof that its source calls returned.
// Validate every source receipt, without rebinding, invoking, or rewriting any
// historical journal entry. Legacy completed runs remain historical snapshots;
// their old outbox facts are never promoted to this protocol.
func verifyCompletedSourceResults(ctx context.Context, b Binding, run Run, port SourcePort) error {
	if run.Phase != "completed" || run.Scope != b.Scope || run.Lanes == nil {
		return ErrUncertain
	}
	lanes := make([]string, 0, len(run.Lanes))
	for name := range run.Lanes {
		lanes = append(lanes, name)
	}
	sort.Strings(lanes)
	for _, lane := range lanes {
		for index, entry := range run.Lanes[lane] {
			if !sourceKind(entry.Kind) {
				continue
			}
			if port == nil || entry.Phase != "receipt" {
				return ErrUncertain
			}
			v, err := sourceEnvelope(entry)
			if err != nil || v.Request.Identity.Scope != run.Scope || v.Request.Identity.Epoch != run.Epoch || v.Request.Identity.RunID != run.ID || v.Request.Identity.Lane != lane || v.Request.Identity.Index != index || v.Result.Outcome != SourceNormal {
				return ErrUncertain
			}
			result, found, err := readSourceResult(ctx, port, b, v.Request.Identity)
			if err != nil || !found || ValidateSourceResult(v.Request, result) != nil || !bytes.Equal(marshal(v.Result), marshal(result)) {
				return ErrUncertain
			}
		}
	}
	return nil
}
