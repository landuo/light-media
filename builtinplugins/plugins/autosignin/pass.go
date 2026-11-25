package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	sdk "light-media/builtinplugins/internal/sdk"
)

// A sign-in pass holds only an active source invocation's stack and ordinary
// locals. It is process memory, never a persisted job or configurable program.
// Requests come only from this plugin's compiled source algorithms. SDK98
// serializes reverse calls within a phase; true ThreadPool network overlap is
// pending the explicitly negotiated shared protocol, never asserted here.
const signPhaseRPCs = 48

type signRPC struct {
	ctx    context.Context
	method string
	input  json.RawMessage
	reply  chan signReply
}
type signReply struct {
	value json.RawMessage
	err   error
}
type signPhase struct {
	token  uint64
	active bool
}
type signPass struct {
	mu                       sync.Mutex
	id, next                 uint64
	owner                    context.Context
	cancel                   context.CancelFunc
	requests                 chan signRPC
	businessDone             chan struct{}
	done                     chan struct{}
	phases                   map[uint64]*signPhase
	businessFinished, closed bool
	err                      error
}

func (s *signPass) fail(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	// A revoked manager may discard admitted work before its callback starts.
	// Such a token owns no caller and must not hold the public invocation open.
	for token, phase := range s.phases {
		if !phase.active {
			delete(s.phases, token)
		}
	}
	s.mu.Unlock()
	s.cancel()
}
func (s *signPass) call(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if s.owner.Err() != nil {
		s.mu.Lock()
		err := s.err
		s.mu.Unlock()
		if err == nil {
			err = s.owner.Err()
		}
		return nil, err
	}
	request := signRPC{ctx: ctx, method: method, input: append(json.RawMessage(nil), raw...), reply: make(chan signReply, 1)}
	select {
	case s.requests <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.owner.Done():
		return nil, s.owner.Err()
	}
	select {
	case result := <-request.reply:
		return result.value, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.owner.Done():
		return nil, s.owner.Err()
	}
}
func (p *Plugin) finishPass(s *signPass) {
	s.mu.Lock()
	ready := s.businessFinished && len(s.phases) == 0 && !s.closed
	if ready {
		s.closed = true
	}
	s.mu.Unlock()
	if !ready {
		return
	}
	p.mu.Lock()
	if p.passes[s.id] == s {
		delete(p.passes, s.id)
	}
	p.mu.Unlock()
	close(s.done)
}
func (p *Plugin) submitPass(ctx context.Context, host sdk.HostCaller, s *signPass) error {
	s.mu.Lock()
	if s.closed || s.owner.Err() != nil {
		s.mu.Unlock()
		return errors.New("sign-in invocation closed")
	}
	s.next++
	token := s.next
	s.phases[token] = &signPhase{token: token}
	s.mu.Unlock()
	err := (sdk.Manager{Host: host}).Submit(ctx, "signin_continue", map[string]uint64{"pass": s.id, "token": token})
	if err != nil {
		s.mu.Lock()
		delete(s.phases, token)
		s.mu.Unlock()
		s.fail(err)
	}
	return err
}
func (p *Plugin) runPass(ctx context.Context, host sdk.HostCaller, run func(context.Context, sdk.HostCaller) error) error {
	owner, cancel := context.WithCancel(ctx)
	s := &signPass{owner: owner, cancel: cancel, requests: make(chan signRPC), businessDone: make(chan struct{}), done: make(chan struct{}), phases: map[uint64]*signPhase{}}
	p.mu.Lock()
	p.nextPass++
	s.id = p.nextPass
	p.passes[s.id] = s
	p.mu.Unlock()
	submitErr := p.submitPass(ctx, host, s)
	go func() {
		var err error
		defer func() {
			if value := recover(); value != nil {
				switch v := value.(type) {
				case *boundaryFailure:
					err = v.cause
				case *sourceException:
					err = v.cause
				case error:
					err = v
				default:
					err = fmt.Errorf("source sign-in panic: %v", v)
				}
			}
			if err != nil {
				s.fail(err)
			}
			s.mu.Lock()
			s.businessFinished = true
			s.mu.Unlock()
			close(s.businessDone)
			p.finishPass(s)
		}()
		if submitErr != nil {
			err = submitErr
			return
		}
		err = run(owner, s.call)
	}()
	select {
	case <-s.done:
	case <-ctx.Done():
		s.fail(ctx.Err())
		<-s.done
	}
	cancel()
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	return err
}
func (p *Plugin) continuePass(ctx context.Context, raw json.RawMessage, host sdk.HostCaller) error {
	var input struct{ Pass, Token uint64 }
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
	}
	p.mu.Lock()
	s := p.passes[input.Pass]
	p.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	phase := s.phases[input.Token]
	if phase == nil || phase.active || s.closed {
		s.mu.Unlock()
		return nil
	}
	phase.active = true
	s.mu.Unlock()
	call, cancel := context.WithCancel(ctx)
	stopOwner := context.AfterFunc(s.owner, cancel)
	var inflight sync.WaitGroup
	defer func() {
		// Keep this callback and its caller alive until its admitted operations
		// have actually returned. Handoff alone never revokes the old phase.
		inflight.Wait()
		stopOwner()
		cancel()
		s.mu.Lock()
		delete(s.phases, input.Token)
		s.mu.Unlock()
		p.finishPass(s)
	}()
	issued := 0
	for issued < signPhaseRPCs {
		select {
		case <-s.businessDone:
			return nil
		case <-call.Done():
			s.fail(call.Err())
			return nil
		case request := <-s.requests:
			if request.ctx.Err() != nil {
				request.reply <- signReply{err: request.ctx.Err()}
				continue
			}
			if call.Err() != nil {
				s.fail(call.Err())
				request.reply <- signReply{err: call.Err()}
				return nil
			}
			issued++
			inflight.Add(1)
			go func(request signRPC) {
				defer inflight.Done()
				operation, stop := context.WithCancel(call)
				stopRequest := context.AfterFunc(request.ctx, stop)
				defer func() { stopRequest(); stop() }()
				value, err := host(operation, request.method, request.input)
				if err != nil {
					s.fail(err)
				}
				request.reply <- signReply{value: value, err: err}
			}(request)
		}
	}
	// Handoff before joining old in-flight calls preserves source queue_cnt
	// overlap once parallel reverse calls are negotiated. This phase remains live
	// until every operation it admitted joins, so no ended HostCaller is retained.
	select {
	case <-s.businessDone:
		return nil
	default:
	}
	if err := p.submitPass(call, host, s); err != nil {
		s.fail(err)
	}
	return nil
}
