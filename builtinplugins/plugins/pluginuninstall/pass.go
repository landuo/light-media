package pluginuninstall

import (
	"context"
	"encoding/json"
	"errors"
	"iter"

	plugins "light-media/builtinplugins/internal/sdk"
)

type uninstallPass struct {
	id, token      uint64
	owner          context.Context
	done           chan struct{}
	ops            *sdkOperations
	next           func() (struct{}, bool)
	stop           func()
	active, sealed bool
	result, err    error
}

func (p *Plugin) run(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) error {
	pass := &uninstallPass{owner: ctx, done: make(chan struct{}), ops: &sdkOperations{}}
	pass.next, pass.stop = iter.Pull(func(yield func(struct{}) bool) {
		pass.ops.yield = func() bool { return yield(struct{}{}) }
		_, pass.result = Run(ctx, raw, pass.ops)
	})
	p.mu.Lock()
	p.nextPass++
	pass.id = p.nextPass
	p.passes[pass.id] = pass
	p.mu.Unlock()
	if err := p.submit(ctx, host, pass); err != nil {
		p.finish(pass, err)
	}
	select {
	case <-pass.done:
	case <-ctx.Done():
		p.finish(pass, ctx.Err())
		<-pass.done
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return pass.err
}

func (p *Plugin) submit(ctx context.Context, host plugins.HostCaller, pass *uninstallPass) error {
	p.mu.Lock()
	if pass.sealed || p.passes[pass.id] != pass {
		p.mu.Unlock()
		return errors.New("uninstall initializer closed")
	}
	pass.token++
	token := pass.token
	p.mu.Unlock()
	return (plugins.Manager{Host: host}).Submit(ctx, "uninstall_continue", map[string]uint64{"pass": pass.id, "token": token})
}

func (p *Plugin) finish(pass *uninstallPass, err error) {
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
	pass.ops.ctx, pass.ops.host = nil, nil
	pass.stop()
	close(pass.done)
}

func (p *Plugin) continueUninstall(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) error {
	var input struct{ Pass, Token uint64 }
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
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
	pass.ops.ctx, pass.ops.host, pass.ops.calls = call, host, 0
	_, more := pass.next()
	pass.ops.ctx, pass.ops.host = nil, nil
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
	if err := ctx.Err(); err != nil {
		p.finish(pass, err)
		return nil
	}
	if err := p.submit(ctx, host, pass); err != nil {
		p.finish(pass, err)
	}
	return nil
}
