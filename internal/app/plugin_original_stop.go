package app

import "context"

// stopOriginalInstanceResources calls the existing owned resource implementation
// for one loaded instance. It never uses the close-all application shutdown APIs.
func (a *App) stopOriginalInstanceResources(ctx context.Context, instance, pluginID string) error {
	switch pluginID {
	case "MPServerStatus":
		a.pluginServerStatus.invalidate(instance)
	case "RemoveLink":
		r := &a.pluginRemoveLink
		r.mu.Lock()
		o := r.owners[instance]
		r.mu.Unlock()
		if o == nil {
			return nil
		}
		select {
		case <-o.ready:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := o.lock(ctx); err != nil {
			return err
		}
		defer o.unlock()
		if o.h != nil {
			if err := o.h.Close(removeLinkOwnerContext(ctx, o, nil)); err != nil {
				return err
			}
			r.mu.Lock()
			if r.owners[instance] == o {
				delete(r.owners, instance)
			}
			r.mu.Unlock()
		}
	case "ScrapeFileClean":
		r := &a.pluginScrapeClean
		r.mu.Lock()
		o := r.owners[instance]
		r.mu.Unlock()
		if o == nil {
			return nil
		}
		select {
		case <-o.ready:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := o.lock(ctx); err != nil {
			return err
		}
		defer o.unlock()
		if o.h != nil {
			if err := o.h.Close(scrapeCleanOwnerContext(ctx, o, nil)); err != nil {
				return err
			}
			r.mu.Lock()
			if r.owners[instance] == o {
				delete(r.owners, instance)
			}
			r.mu.Unlock()
		}
	case "CleanInvalidSeed":
		r := &a.pluginCleanInvalidSeed
		r.mu.Lock()
		o := r.owners[instance]
		r.mu.Unlock()
		if o == nil {
			return nil
		}
		if err := o.lock(ctx); err != nil {
			return err
		}
		defer o.unlock()
		if o.h != nil {
			if err := o.h.Stop(context.WithValue(ctx, pluginCleanInvalidSeedStopKey{}, o)); err != nil {
				return err
			}
			r.mu.Lock()
			if r.owners[instance] == o {
				delete(r.owners, instance)
			}
			r.mu.Unlock()
		}
	}
	return nil
}
