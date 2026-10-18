package app

import (
	"bytes"
	"context"
	"database/sql"
)

type cloudflareDeferredOwnerKey struct{}
type cloudflareDeferredOwner struct {
	call  *pluginDeferredCall
	owner *pluginCloudflareOwner
}

// No process/global/SQL lock is held while waiting. The lease includes started
// publication, the actual native operation, and its outer acknowledgement.
func (d *pluginDeferredCall) lockCloudflareDeferred(ctx context.Context) (context.Context, func(), error) {
	o, e := d.a.cloudflareOwner(ctx, d.installation)
	if e != nil {
		return ctx, nil, e
	}
	if e = o.lock(ctx); e != nil {
		return ctx, nil, e
	}
	return context.WithValue(ctx, cloudflareDeferredOwnerKey{}, cloudflareDeferredOwner{d, o}), o.unlock, nil
}

// The owner fact and deferred.started share the caller's existing transaction.
// Losing power before Host entry is an authenticated unknown, never a missing
// owner record from which a fresh generation or a replay could be inferred.
func (d *pluginDeferredCall) cloudflareStartedTx(ctx context.Context, tx *sql.Tx) error {
	held, ok := ctx.Value(cloudflareDeferredOwnerKey{}).(cloudflareDeferredOwner)
	if !ok || held.call != d || held.owner == nil || held.owner.a != d.a || held.owner.instance != d.run.InstanceID || held.owner.generation != d.run.Generation {
		return bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare启动缺少私有宿主预留")
	}
	s, prior, e := d.a.pluginStore.cloudflareRead(ctx, tx, d.run.InstanceID, d.run.Generation)
	if e != nil {
		return e
	}
	if _, exists := s.Runs[d.run.ID]; exists {
		return bad("CLOUDFLARE_REPLAY_DENIED", "Cloudflare启动身份已经登记")
	}
	r := cloudflareAction{RunID: d.run.ID, Action: d.run.Action, Revision: d.run.ConfigRevision, InputSHA: deferredInputSHA(d.run.Input), Method: d.record.Method, Phase: "started"}
	if cloudflareNoUnknown(s) != nil {
		r.Phase = "failed"
		r.ErrorCode = cloudflareBlockedByUnknown
		r.Result = cloudflareBlockedResult()
	}
	s.Runs[d.run.ID] = r
	return d.a.pluginStore.cloudflareSave(ctx, tx, s, prior)
}

func cloudflareHeldOwner(ctx context.Context, a *App, d *pluginDeferredCall, o *pluginCloudflareOwner) bool {
	held, ok := ctx.Value(cloudflareDeferredOwnerKey{}).(cloudflareDeferredOwner)
	return ok && held.call == d && held.owner == o && held.call != nil && d.a == a && d.run.InstanceID == o.instance && d.run.Generation == o.generation
}

func cloudflareStartedMatches(r cloudflareAction, c *cloudflareCall) bool {
	return r.RunID == c.run.ID && r.Action == c.run.Action && r.Revision == c.run.ConfigRevision && r.Method == c.method && r.InputSHA == deferredInputSHA(c.run.Input) && !r.Entered && len(r.Effects) == 0 && r.Phase == "started" && r.ErrorCode == "" && (len(r.Result) == 0 || bytes.Equal(r.Result, []byte("null")))
}

func cloudflareBlockedMatches(r cloudflareAction, c *cloudflareCall) bool {
	return r.RunID == c.run.ID && r.Action == c.run.Action && r.Revision == c.run.ConfigRevision && r.Method == c.method && r.InputSHA == deferredInputSHA(c.run.Input) && !r.Entered && len(r.Effects) == 0 && r.Phase == "failed" && r.ErrorCode == cloudflareBlockedByUnknown && bytes.Equal(r.Result, cloudflareBlockedResult())
}
