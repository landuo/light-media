package sitestatistic

import (
	"context"
	"encoding/json"
	"errors"

	plugins "light-media/builtinplugins/internal/sdk"
)

// Each admitted date read gets a fresh HostCaller. The original page/dashboard/
// event call remains active until its whole __get_data invocation finishes.
// These locals are neither persisted nor replayed after process retirement.
type dataPass struct {
	id, token      uint64
	cursor         *selectionCursor
	owner          context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	err            error
	active, sealed bool
}

func (p *Plugin) getSelected(ctx context.Context, caller plugins.HostCaller) (Selected, error) {
	cursor, e := beginSelection(ctx, sdkHost{caller: caller})
	if e != nil {
		return Selected{}, e
	}
	if cursor.finished {
		return cursor.selected, nil
	}
	owner, cancel := context.WithCancel(ctx)
	defer cancel()
	pass := &dataPass{cursor: cursor, owner: owner, cancel: cancel, done: make(chan struct{})}
	p.mu.Lock()
	p.nextPass++
	pass.id = p.nextPass
	p.passes[pass.id] = pass
	p.mu.Unlock()
	if e = p.submitData(ctx, caller, pass); e != nil {
		p.endData(pass, e)
	}
	select {
	case <-pass.done:
	case <-ctx.Done():
		cancel()
		p.endData(pass, ctx.Err())
		<-pass.done
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return cursor.selected, pass.err
}
func (p *Plugin) endData(pass *dataPass, e error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !pass.sealed {
		pass.sealed = true
		pass.err = e
	}
	if !pass.active && p.passes[pass.id] == pass {
		delete(p.passes, pass.id)
		close(pass.done)
	}
}
func (p *Plugin) submitData(ctx context.Context, caller plugins.HostCaller, pass *dataPass) error {
	p.mu.Lock()
	if pass.sealed || p.passes[pass.id] != pass {
		p.mu.Unlock()
		return errors.New("source data invocation closed")
	}
	pass.token++
	token := pass.token
	p.mu.Unlock()
	return (plugins.Manager{Host: caller}).Submit(ctx, "data_continue", map[string]any{"pass": pass.id, "token": token})
}
func (p *Plugin) continueData(ctx context.Context, raw json.RawMessage, caller plugins.HostCaller) error {
	var input struct{ Pass, Token uint64 }
	if e := json.Unmarshal(raw, &input); e != nil {
		return e
	}
	p.mu.Lock()
	pass := p.passes[input.Pass]
	if pass == nil || pass.sealed || pass.active || pass.token != input.Token {
		p.mu.Unlock()
		return nil
	}
	pass.active = true
	p.mu.Unlock()
	call, cancel := context.WithCancel(ctx)
	stopOwner := context.AfterFunc(pass.owner, cancel)
	defer func() { stopOwner(); cancel() }()
	e := call.Err()
	if e == nil {
		e = pass.cursor.advance(call, sdkHost{caller: caller})
	}
	p.mu.Lock()
	pass.active = false
	sealed := pass.sealed
	p.mu.Unlock()
	if e != nil || pass.cursor.finished || sealed {
		p.endData(pass, e)
		return nil
	}
	if e = p.submitData(call, caller, pass); e != nil {
		p.endData(pass, e)
	}
	return nil
}
