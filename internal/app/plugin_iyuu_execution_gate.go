package app

import "context"

// Only deferred.execute creates this capability after acquiring the instance
// gate. Worker JSON and queue metadata cannot grant or share the gate.
type iyuuDeferredOwnerKey struct{}
type iyuuDeferredOwner struct {
	call  *pluginDeferredCall
	owner *pluginIYUUOwner
}

func (d *pluginDeferredCall) lockIYUUDeferred(ctx context.Context) (context.Context, func(), error) {
	owner, err := d.a.iyuuOwner(ctx, d.installation)
	if err != nil {
		return ctx, nil, err
	}
	if err = owner.lock(ctx); err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, iyuuDeferredOwnerKey{}, iyuuDeferredOwner{d, owner}), owner.unlock, nil
}
