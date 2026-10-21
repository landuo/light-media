package durable

import (
	"context"
	"sync"
)

// Explicit synthetic host proof store: independent of journal, no external IO.
type syntheticSourcePort struct {
	mu           sync.Mutex
	results      map[SourceIdentity]SourceResult
	calls, binds int
	invoke       func(SourceRequest) (SourceResult, error)
	read         func(SourceIdentity) (SourceResult, bool, error)
}

func (p *syntheticSourcePort) Bind(_ context.Context, b Binding, _ string) (SourceConfigIdentity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.binds++
	return SourceConfigIdentity{Revision: b.Revision, Digest: "synthetic-effective-config"}, nil
}
func (p *syntheticSourcePort) Invoke(_ context.Context, _ Binding, r SourceRequest) error {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	result := SourceResult{Identity: r.Identity, Outcome: SourceNormal, ProofID: "synthetic-independent-proof:" + sourceGuard(r.Identity)}
	var err error
	if p.invoke != nil {
		result, err = p.invoke(r)
	}
	if result.ProofID != "" {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.results == nil {
			p.results = map[SourceIdentity]SourceResult{}
		}
		p.results[r.Identity] = result
	}
	return err
}
func (p *syntheticSourcePort) ReadResult(_ context.Context, _ Binding, i SourceIdentity) (SourceResult, bool, error) {
	if p.read != nil {
		return p.read(i)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	result, found := p.results[i]
	return result, found, nil
}
