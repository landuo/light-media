package iyuuautoseed

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	sdk "light-media/builtinplugins/internal/sdk"
)

const maxPhaseCalls = 48

type passPhaseRequest struct {
	Pass  uint64 `json:"pass"`
	Phase uint64 `json:"phase"`
}

type passCall struct {
	ctx         context.Context
	method      string
	body        json.RawMessage
	reply       chan passReply
	fresh, same bool
}

type passReply struct {
	body json.RawMessage
	err  error
}

// A pass keeps the source's ordinary call stack and its exact effect order.
// The only values handed through Submit are process-local admission IDs. Each
// admitted callback supplies its own caller and runs the requests it receives.
type hostPass struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	id      uint64
	calls   chan passCall
	mu      sync.Mutex
	pending uint64
	next    uint64
	closed  bool
	active  sync.WaitGroup
}

func (p *Plugin) withHostPass(ctx context.Context, caller sdk.HostCaller, run func(context.Context, sdk.HostCaller) error) error {
	owner, cancel := context.WithCancelCause(ctx)
	pass := &hostPass{ctx: owner, cancel: cancel, calls: make(chan passCall, 1)}
	p.mu.Lock()
	p.nextPass++
	pass.id = p.nextPass
	p.passes[pass.id] = pass
	p.mu.Unlock()
	defer func() {
		cancel(nil)
		pass.mu.Lock()
		pass.closed = true
		pass.mu.Unlock()
		p.mu.Lock()
		delete(p.passes, pass.id)
		p.mu.Unlock()
		pass.active.Wait()
	}()
	if err := pass.submit(ctx, caller); err != nil {
		return err
	}
	if err := run(owner, pass.call); err != nil {
		return err
	}
	return context.Cause(owner)
}

func (p *Plugin) continueHostPass(ctx context.Context, caller sdk.HostCaller, input passPhaseRequest) error {
	p.mu.RLock()
	pass := p.passes[input.Pass]
	p.mu.RUnlock()
	if pass == nil {
		return nil
	}
	pass.mu.Lock()
	if pass.closed || pass.ctx.Err() != nil || input.Phase == 0 || input.Phase != pass.pending {
		pass.mu.Unlock()
		return nil
	}
	pass.pending = 0
	pass.active.Add(1)
	pass.mu.Unlock()
	defer pass.active.Done()

	phase, cancel := context.WithCancel(ctx)
	stopOwner := context.AfterFunc(pass.ctx, cancel)
	defer func() { stopOwner(); cancel() }()

	for issued := 0; ; {
		select {
		case <-phase.Done():
			if pass.ctx.Err() == nil {
				pass.cancel(phase.Err())
			}
			return nil
		case request := <-pass.calls:
			if err := context.Cause(request.ctx); err != nil {
				request.reply <- passReply{err: err}
				continue
			}
			if err := context.Cause(pass.ctx); err != nil {
				request.reply <- passReply{err: err}
				return nil
			}
			full := phaseNeedsRenewal(phase, issued, len(request.body))
			if issued > 0 && !request.same && (request.fresh || full) {
				if err := pass.submit(phase, caller); err != nil {
					pass.cancel(err)
					return nil
				}
				// Forward only the request that has not been dispatched. The
				// previous phase never replays an effect or its marker cleanup.
				select {
				case pass.calls <- request:
				case <-pass.ctx.Done():
				}
				return nil
			}
			if issued > 0 && request.same && full {
				request.reply <- passReply{err: errors.New("IYUU material redirect exceeds the current scoped request budget")}
				continue
			}
			operation, stop := context.WithCancel(phase)
			stopRequest := context.AfterFunc(request.ctx, stop)
			if err := context.Cause(request.ctx); err != nil {
				stopRequest()
				stop()
				request.reply <- passReply{err: err}
				continue
			}
			issued++
			body, err := caller(operation, request.method, request.body)
			stopRequest()
			stop()
			request.reply <- passReply{body: body, err: err}
		}
	}
}

func (s *hostPass) submit(ctx context.Context, caller sdk.HostCaller) error {
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return context.Cause(s.ctx)
	}
	s.next++
	input := passPhaseRequest{Pass: s.id, Phase: s.next}
	s.pending = input.Phase
	s.mu.Unlock()
	return (sdk.Manager{Host: caller}).Submit(ctx, "pass_continue", input)
}

func (s *hostPass) call(ctx context.Context, method string, body json.RawMessage) (json.RawMessage, error) {
	request := passCall{
		ctx: ctx, method: method, body: append(json.RawMessage(nil), body...),
		reply: make(chan passReply, 1),
	}
	if method == sdk.SitesSourceRequestCapability {
		var input struct {
			OriginProfile string `json:"origin_profile"`
		}
		if err := json.Unmarshal(body, &input); err != nil {
			return nil, err
		}
		switch input.OriginProfile {
		case "iyuu-mteam":
			// Its successful response arms one material URL in this exact
			// reverse callback. All subsequent alias GETs keep that scope.
			request.fresh = true
		case "iyuu-mteam-material-api", "iyuu-mteam-material-cdn":
			request.same = true
		}
	}
	select {
	case s.calls <- request:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-s.ctx.Done():
		return nil, context.Cause(s.ctx)
	}
	select {
	case result := <-request.reply:
		return result.body, result.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-s.ctx.Done():
		return nil, context.Cause(s.ctx)
	}
}

func phaseNeedsRenewal(ctx context.Context, issued, requestBytes int) bool {
	if issued >= maxPhaseCalls {
		return true
	}
	budget, ok := sdk.CurrentHostCallBudget(ctx)
	if !ok {
		return false
	}
	if budget.MaxHostCalls-budget.HostCallsCharged < 2 {
		return true
	}
	// Reserve a host call for Submit, the next request (including encoded
	// frames), and up to one full-size logical reply plus framing.
	const handoffBytes = 8 << 10
	return budget.MaxWorkerToHostWireBytes-budget.WorkerToHostWireBytesCharged < 2*requestBytes+handoffBytes ||
		budget.MaxHostToWorkerWireBytes-budget.HostToWorkerWireBytesCharged < 2*budget.LogicalMessageBytes+handoffBytes ||
		budget.CumulativeChunksPerDirection-budget.WorkerToHostChunksCharged < budget.ChunksPerLogicalMessage+1 ||
		budget.CumulativeChunksPerDirection-budget.HostToWorkerChunksCharged < budget.ChunksPerLogicalMessage+1
}
