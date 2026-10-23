package app

import (
	"context"
	"sync/atomic"

	native "light-media/internal/plugins/iyuuautoseed/native"
	sr "light-media/internal/plugins/seedrescue"
)

// This capability exists only while get_service holds the retained owner's gate.
// It permits the original connection probe, never torrent queries or mutations.
type originalSeedServiceKey struct{}
type originalSeedServiceRead struct {
	object  *originalPluginInstance
	owner   *pluginSeedRescueOwner
	machine *sr.Machine
	active  atomic.Bool
}

func originalSeedServiceContext(ctx context.Context, p *originalPluginInstance, o *pluginSeedRescueOwner) (context.Context, func()) {
	read := &originalSeedServiceRead{object: p, owner: o, machine: o.machine}
	read.active.Store(true)
	return context.WithValue(ctx, originalSeedServiceKey{}, read), func() { read.active.Store(false) }
}
func (o *pluginSeedRescueOwner) originalServiceRead(ctx context.Context) (*originalSeedServiceRead, bool) {
	read, ok := ctx.Value(originalSeedServiceKey{}).(*originalSeedServiceRead)
	return read, ok && read != nil && read.owner == o
}
func (r *originalSeedServiceRead) guard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !r.active.Load() || r.object.app != r.owner.a || r.object.installation.InstanceID != r.owner.instance || r.object.installation.Generation != r.owner.generation || r.owner.machine != r.machine {
		return seedRescueEvidenceError()
	}
	loaded, ok := r.owner.a.pluginRunningInstances.Load(r.owner.instance)
	if !ok || loaded != r.object {
		return seedRescueEvidenceError()
	}
	r.object.configMu.RLock()
	same := r.object.nativeState == r.machine
	r.object.configMu.RUnlock()
	if !same {
		return seedRescueEvidenceError()
	}
	return nil
}
func (r *originalSeedServiceRead) service(ctx context.Context, name string) (*sr.Service, error) {
	if err := r.guard(ctx); err != nil {
		return nil, err
	}
	if name != sr.ParseConfig(r.machine.Config()).Downloader {
		return nil, seedRescueEvidenceError()
	}
	d, err := r.owner.boundDownloader(ctx, name)
	if err != nil {
		if seedRescueErrorCode(err) == "SEED_RESCUE_DOWNLOADER_UNBOUND" {
			return nil, nil
		}
		return nil, err
	}
	digest := seedRescueDownloaderSHA(d)
	guard := func(ctx context.Context) error {
		if err := r.guard(ctx); err != nil {
			return err
		}
		current, err := r.owner.boundDownloader(ctx, name)
		if err != nil || seedRescueDownloaderSHA(current) != digest {
			return seedRescueEvidenceError()
		}
		return nil
	}
	client, err := native.NewIYUUClient(seedRescueNativeConfig(d), native.IYUUOptions{APIKey: d.APIKey, AutoCategory: d.AutoCategory, Sequential: d.Sequential, FirstLast: d.FirstLast, Guard: guard})
	if err != nil {
		return nil, err
	}
	defer client.Close()
	connectedErr := client.SeedRescueConnected(ctx)
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return &sr.Service{Name: name, Kind: seedRescueNativeConfig(d).Kind, Connected: connectedErr == nil}, nil
}
func (r *originalSeedServiceRead) iyuu(ctx context.Context) (*originalPluginInstance, bool) {
	if r.guard(ctx) != nil {
		return nil, false
	}
	loaded, ok := r.owner.a.pluginRunningInstances.Load("IYUUAutoSeed")
	if !ok {
		return nil, false
	}
	object, ok := loaded.(*originalPluginInstance)
	return object, ok && object.installation.PluginID == "IYUUAutoSeed"
}
