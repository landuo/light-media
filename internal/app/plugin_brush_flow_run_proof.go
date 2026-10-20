package app

import (
	"bytes"
	"context"
	bf "light-media/internal/plugins/brushflow"
)

func (c *brushFlowCall) captureRunCipher(ctx context.Context) error {
	var sealed []byte
	p := c.o.a.pluginStore
	if e := p.store.DB.QueryRowContext(ctx, `SELECT input FROM plugin_runs WHERE id=? AND instance_id=? AND version=? AND generation=?`, c.run.ID, c.run.InstanceID, c.run.Version, c.run.Generation).Scan(&sealed); e != nil {
		return e
	}
	raw, e := p.unseal("run", c.run.InstanceID, c.run.ID, sealed)
	if e != nil || !bytes.Equal(raw, c.run.Input) {
		return brushFlowEvidenceError()
	}
	c.runCipherSHA = tokenHash(string(sealed))
	return nil
}

func (p *PluginStore) brushFlowRunProofCall(ctx context.Context, id string) (*brushFlowCall, bool) {
	c, ok := ctx.Value(brushFlowCallKey{}).(*brushFlowCall)
	d, bound := ctx.Value(pluginDeferredExecutionKey{}).(*pluginDeferredCall)
	lease, leased := ctx.Value(brushFlowLeaseKey{}).(brushFlowLease)
	if !ok || !bound || !leased || c == nil || d == nil || c.o == nil || c.o.a.pluginStore != p || lease.d != d || lease.o != c.o || d.a != c.o.a || d.run.ID != c.run.ID || id != c.run.ID || c.run.InstanceID != c.o.instance || c.run.Generation != c.o.generation || c.run.Version != bf.Version || d.installation.PluginID != bf.PluginID || d.installation.InstanceID != c.run.InstanceID || d.installation.Version != bf.Version || d.record.Method != c.method || !bytes.Equal(d.run.Input, c.run.Input) || c.runCipherSHA == "" {
		return nil, false
	}
	return c, true
}

// Private native code alone can hold this live-call proof. Every access reads
// the actual SQL metadata and complete ciphertext and checks its exact hash.
// Only the already-authenticated immutable plaintext is reused; no DB row,
// mutable phase, config revision, owner head or receipt is taken from a cache.
func (p *PluginStore) brushFlowLoadRun(ctx context.Context, q pluginRemoveLinkJournalReader, id string) (PluginRun, error) {
	c, ok := p.brushFlowRunProofCall(ctx, id)
	if !ok {
		return p.scanPluginRun(q.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, id))
	}
	var run PluginRun
	var input, result []byte
	e := q.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, id).Scan(&run.ID, &run.InstanceID, &run.Version, &run.Generation, &run.ConfigRevision, &run.Action, &run.State, &input, &result, &run.ErrorCode, &run.CreatedAt, &run.UpdatedAt)
	if e != nil {
		return run, e
	}
	if run.ID != c.run.ID || run.InstanceID != c.run.InstanceID || run.Version != c.run.Version || run.Generation != c.run.Generation || tokenHash(string(input)) != c.runCipherSHA {
		return run, brushFlowEvidenceError()
	}
	run.Input = c.run.Input
	run.RequestID = c.run.RequestID
	if len(result) > 0 {
		run.Result, e = p.unseal("run_result", run.InstanceID, run.ID, result)
	}
	return run, e
}
