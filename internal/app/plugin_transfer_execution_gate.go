package app

import "context"

// Only deferred.execute creates this capability after acquiring the instance
// gate. Worker JSON and queue metadata cannot grant or share the gate.
type transferDeferredOwnerKey struct{}
type transferDeferredOwner struct {
	call  *pluginDeferredCall
	owner *pluginTransferOwner
}

func (d *pluginDeferredCall) lockTransferDeferred(ctx context.Context) (context.Context, func(), error) {
	owner, err := d.a.transferOwner(ctx, d.installation)
	if err != nil {
		return ctx, nil, err
	}
	if err = owner.lock(ctx); err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, transferDeferredOwnerKey{}, transferDeferredOwner{d, owner}), owner.unlock, nil
}
