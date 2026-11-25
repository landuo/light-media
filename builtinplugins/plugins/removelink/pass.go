package removelink

import (
	"context"
	"encoding/json"
	"errors"
	"iter"

	plugins "light-media/builtinplugins/internal/sdk"
)

// cleanupPass retains only RemoveLink's synchronous function/loop locals.
// It is volatile and belongs to one live owner invocation. It cannot replay
// after a cold start or select an operation supplied in a payload.
type cleanupPass struct {
	id, token      uint64
	owner          context.Context
	done           chan struct{}
	active, sealed bool
	err            error
	result         error
	ops            *sdkOps
	next           func() (struct{}, bool)
	stop           func()
}

func (p *Plugin) run(ctx context.Context, host plugins.HostCaller, body func(*operation) error) error {
	pass := &cleanupPass{owner: ctx, done: make(chan struct{}), ops: &sdkOps{}}
	sequence := func(yield func(struct{}) bool) {
		defer capture(&pass.result)
		pass.ops.yield = func() bool { return yield(struct{}{}) }
		if e := pass.ops.failure(); e != nil {
			pass.result = e
			return
		}
		// Every invocation has its own effect pair and failure barrier. No
		// timer/observer or suspended continuation borrows another caller.
		pass.result = body(&operation{Machine: p.machine, Ops: pass.ops})
		if failure := pass.ops.failure(); failure != nil {
			pass.result = failure
		}
	}
	pass.next, pass.stop = iter.Pull(sequence)
	p.mu.Lock()
	p.nextPass++
	pass.id = p.nextPass
	p.passes[pass.id] = pass
	p.mu.Unlock()
	if e := p.submit(ctx, host, pass); e != nil {
		p.finish(pass, e)
	}
	select {
	case <-pass.done:
	case <-ctx.Done():
		p.finish(pass, ctx.Err())
		<-pass.done // Iterator finally and prior side effects finish before owner.
	}
	p.mu.Lock()
	err := pass.err
	p.mu.Unlock()
	return err
}
func (p *Plugin) submit(ctx context.Context, host plugins.HostCaller, pass *cleanupPass) error {
	p.mu.Lock()
	if pass.sealed || p.passes[pass.id] != pass {
		p.mu.Unlock()
		return errors.New("cleanup pass closed")
	}
	pass.token++
	token := pass.token
	p.mu.Unlock()
	payload := map[string]any{"pass": pass.id, "token": token}
	manager := plugins.Manager{Host: host}
	if plugins.IsCleanupInvocation(ctx) {
		return manager.SubmitCleanup(ctx, "cleanup_continue", payload)
	}
	return manager.Submit(ctx, "cleanup_continue", payload)
}
func (p *Plugin) finish(pass *cleanupPass, err error) {
	p.mu.Lock()
	if !pass.sealed {
		pass.sealed, pass.err = true, err
	}
	if pass.active || p.passes[pass.id] != pass {
		p.mu.Unlock()
		return
	}
	delete(p.passes, pass.id)
	p.mu.Unlock()
	// A pull is stopped only between callbacks. Stop resumes a suspended
	// source stack with yield=false and executes its actual defer/finally.
	pass.ops.unbind()
	pass.stop()
	close(pass.done)
}
func (p *Plugin) continueCleanup(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) error {
	var in struct{ Pass, Token uint64 }
	if e := json.Unmarshal(raw, &in); e != nil {
		return e
	}
	p.mu.Lock()
	pass := p.passes[in.Pass]
	if pass == nil || pass.sealed || pass.active || pass.token != in.Token {
		p.mu.Unlock()
		return nil
	}
	pass.active = true
	p.mu.Unlock()
	call, cancel := context.WithCancel(ctx)
	stopOwner := context.AfterFunc(pass.owner, cancel)
	pass.ops.bind(call, host)
	_, more := pass.next()
	pass.ops.unbind()
	stopOwner()
	cancel()
	p.mu.Lock()
	pass.active = false
	sealed := pass.sealed
	p.mu.Unlock()
	if !more || sealed {
		p.finish(pass, pass.result)
		return nil
	}
	if e := ctx.Err(); e != nil {
		p.finish(pass, e)
		return nil
	}
	if e := p.submit(ctx, host, pass); e != nil {
		p.finish(pass, e)
	}
	return nil
}
