package torrenttransfer

import (
	"context"
	"encoding/json"
	"errors"

	"light-media/builtinplugins/internal/sdk"
)

// transferPass contains only this invocation's Python for-loop locals. It is
// neither persisted nor replayed after process retirement.
type transferPass struct {
	id, token      uint64
	cursor         *transferCursor
	owner          context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	err            error
	active, sealed bool
}

func (p *Plugin) runTransfer(ctx context.Context, host plugins.HostCaller) error {
	cursor, e := p.core(host).beginTransfer(ctx)
	if e != nil || cursor == nil {
		return e
	}
	owner, cancel := context.WithCancel(ctx)
	pass := &transferPass{cursor: cursor, owner: owner, cancel: cancel, done: make(chan struct{})}
	p.mu.Lock()
	p.nextPass++
	pass.id = p.nextPass
	p.passes[pass.id] = pass
	p.mu.Unlock()
	if e = p.submitTransfer(ctx, host, pass); e != nil {
		p.endPass(pass, e)
	}
	select {
	case <-pass.done:
	case <-ctx.Done():
		cancel()
		p.endPass(pass, ctx.Err())
		<-pass.done
	}
	cancel()
	p.mu.Lock()
	e = pass.err
	p.mu.Unlock()
	return e
}
func (p *Plugin) endPass(pass *transferPass, e error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !pass.sealed {
		pass.sealed = true
		pass.err = e
	}
	if !pass.active {
		if p.passes[pass.id] == pass {
			delete(p.passes, pass.id)
			close(pass.done)
		}
	}
}
func (p *Plugin) submitTransfer(ctx context.Context, host plugins.HostCaller, pass *transferPass) error {
	p.mu.Lock()
	if pass.sealed || p.passes[pass.id] != pass {
		p.mu.Unlock()
		return errors.New("transfer pass closed")
	}
	pass.token++
	token := pass.token
	p.mu.Unlock()
	return (plugins.Manager{Host: host}).Submit(ctx, "transfer_continue", map[string]any{"pass": pass.id, "token": token})
}
func (p *Plugin) continueTransfer(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) error {
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
	defer func() { stopOwner(); cancel() }()
	core := p.core(host)
	e := call.Err()
	if e == nil {
		e = core.advanceTransfer(call, pass.cursor)
	}
	last := pass.cursor.finished
	p.mu.Lock()
	pass.active = false
	sealed := pass.sealed
	p.mu.Unlock()
	if e != nil || last || sealed {
		p.endPass(pass, e)
		return nil
	}
	if e = p.submitTransfer(call, host, pass); e != nil {
		p.endPass(pass, e)
	}
	return nil
}
