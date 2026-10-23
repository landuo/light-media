package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
	"light-media/internal/plugins/scrapefileclean"
	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

// initOriginalIdleMachine replaces the idle branch's temporary Config.Apply.
// It runs only inside the real verified configure invocation, never a getter.
func (o *pluginRemoveLinkOwner) initOriginalIdleMachine(ctx context.Context, raw json.RawMessage) error {
	machine, _ := originalNativeStateBeforeInit(ctx).(*removelink.Machine)
	if machine == nil {
		machine = removelink.New(&originalRemoveLinkNoFiles{})
	}
	// A different existing host owns its machine's Ops. This branch is only idle.
	if o.h != nil {
		return cleanuphost.ErrUnsafe
	}
	ops := &originalRemoveLinkNoFiles{}
	machine.Ops = ops
	o.originalMachine = machine
	// Capture before Init so a thrown source error does not discard mutations.
	captureOriginalNativeState(ctx, machine)
	captureOriginalAppliedConfiguration(ctx, raw)
	err := machine.Init(raw)
	if ops.touched || len(machine.Watches) != 0 || len(machine.Queue) != 0 || machine.TimerDue != nil {
		return cleanuphost.ErrUnsafe
	}
	return err
}

type originalRemoveLinkNoFiles struct{ touched bool }

func (o *originalRemoveLinkNoFiles) Exists(p string) bool { o.touched = true; return false }
func (o *originalRemoveLinkNoFiles) Stat(p string) (removelink.Identity, error) {
	o.touched = true
	return removelink.Identity{}, cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) Children(p string) ([]removelink.Entry, error) {
	o.touched = true
	return nil, cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) Scan(p string) ([]removelink.Tracked, error) {
	o.touched = true
	return nil, cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) Unlink(p string) error {
	o.touched = true
	return cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) RemoveTree(p string) error {
	o.touched = true
	return cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) RemoveDir(p string) error {
	o.touched = true
	return cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) History(a, b string) (bool, error) {
	o.touched = true
	return false, cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) Event(p string) error {
	o.touched = true
	return cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) Notice(a, b string) error {
	o.touched = true
	return cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) StorageExists(p removelink.Item) (bool, error) {
	o.touched = true
	return false, cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) StorageList(p removelink.Item) ([]removelink.Item, error) {
	o.touched = true
	return nil, cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) StorageDelete(p removelink.Item) (bool, error) {
	o.touched = true
	return false, cleanuphost.ErrUnsafe
}
func (o *originalRemoveLinkNoFiles) MediaExtensions() []string { o.touched = true; return nil }

// initOriginalIdleMachine replaces the idle branch's temporary Config.Apply.
// It runs only inside the real verified configure invocation, never a getter.
func (o *pluginScrapeCleanOwner) initOriginalIdleMachine(ctx context.Context, raw json.RawMessage) error {
	machine, _ := originalNativeStateBeforeInit(ctx).(*scrapefileclean.Machine)
	if machine == nil {
		machine = scrapefileclean.New(&originalScrapeCleanNoFiles{})
	}
	// A different existing host owns its machine's Ops. This branch is only idle.
	if o.h != nil {
		return scrapehost.ErrUnsafe
	}
	ops := &originalScrapeCleanNoFiles{}
	machine.Ops = ops
	o.originalMachine = machine
	// Capture before Init so a thrown source error does not discard mutations.
	captureOriginalNativeState(ctx, machine)
	captureOriginalAppliedConfiguration(ctx, raw)
	err := machine.Init(raw)
	if ops.touched || len(machine.Watches) != 0 || len(machine.Queue) != 0 || machine.TimerDue != nil {
		return scrapehost.ErrUnsafe
	}
	return err
}

type originalScrapeCleanNoFiles struct{ touched bool }

func (o *originalScrapeCleanNoFiles) Exists(p string) bool { o.touched = true; return false }
func (o *originalScrapeCleanNoFiles) Stat(p string) (scrapefileclean.Identity, error) {
	o.touched = true
	return scrapefileclean.Identity{}, scrapehost.ErrUnsafe
}
func (o *originalScrapeCleanNoFiles) Children(p string) ([]scrapefileclean.Entry, error) {
	o.touched = true
	return nil, scrapehost.ErrUnsafe
}
func (o *originalScrapeCleanNoFiles) Scan(p string) ([]scrapefileclean.Tracked, error) {
	o.touched = true
	return nil, scrapehost.ErrUnsafe
}
func (o *originalScrapeCleanNoFiles) Unlink(p string) error {
	o.touched = true
	return scrapehost.ErrUnsafe
}
func (o *originalScrapeCleanNoFiles) RemoveTree(p string) error {
	o.touched = true
	return scrapehost.ErrUnsafe
}
func (o *originalScrapeCleanNoFiles) RemoveDir(p string) error {
	o.touched = true
	return scrapehost.ErrUnsafe
}
func (o *originalScrapeCleanNoFiles) History(a, b string) (bool, error) {
	o.touched = true
	return false, scrapehost.ErrUnsafe
}
func (o *originalScrapeCleanNoFiles) Event(p string) error {
	o.touched = true
	return scrapehost.ErrUnsafe
}
func (o *originalScrapeCleanNoFiles) Notice(a, b string) error {
	o.touched = true
	return scrapehost.ErrUnsafe
}
func (o *originalScrapeCleanNoFiles) IsDir(p string) bool     { o.touched = true; return false }
func (o *originalScrapeCleanNoFiles) IsSymlink(p string) bool { o.touched = true; return false }

// originalWatcherStop handles only the no-host logical object. Nonempty-root
// owners remain on the existing per-owner Host.Close path. Call before the
// common stop switch; this never changes a sibling or a global closing flag.
func originalWatcherStop(ctx context.Context, a *App, instance, pluginID string) (bool, error) {
	if pluginID == "RemoveLink" {
		r := &a.pluginRemoveLink
		r.mu.Lock()
		o := r.owners[instance]
		r.mu.Unlock()
		if o == nil {
			return false, nil
		}
		select {
		case <-o.ready:
		case <-ctx.Done():
			return true, ctx.Err()
		}
		if err := o.lock(ctx); err != nil {
			return true, err
		}
		defer o.unlock()
		if o.h != nil || o.originalMachine == nil {
			return false, nil
		}
		if err := ctx.Err(); err != nil {
			return true, err
		}
		o.originalMachine.Stop()
		r.mu.Lock()
		if r.owners[instance] == o {
			delete(r.owners, instance)
		}
		r.mu.Unlock()
		return true, nil
	}
	if pluginID == "ScrapeFileClean" {
		r := &a.pluginScrapeClean
		r.mu.Lock()
		o := r.owners[instance]
		r.mu.Unlock()
		if o == nil {
			return false, nil
		}
		select {
		case <-o.ready:
		case <-ctx.Done():
			return true, ctx.Err()
		}
		if err := o.lock(ctx); err != nil {
			return true, err
		}
		defer o.unlock()
		if o.h != nil || o.originalMachine == nil {
			return false, nil
		}
		if err := ctx.Err(); err != nil {
			return true, err
		}
		o.originalMachine.Stop()
		r.mu.Lock()
		if r.owners[instance] == o {
			delete(r.owners, instance)
		}
		r.mu.Unlock()
		return true, nil
	}
	return false, nil
}
