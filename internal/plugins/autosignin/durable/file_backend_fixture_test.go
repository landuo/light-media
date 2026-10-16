package durable

import "context"

type fileStoreBackend struct{ f *FileStore }

func (b fileStoreBackend) AcquireExecution(ctx context.Context, binding Binding) (func(), error) {
	return b.f.AcquireExecution(ctx, binding)
}
func (b fileStoreBackend) Hold(ctx context.Context, binding Binding) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return b.f.retainOwner(binding)
}
func (b fileStoreBackend) View(ctx context.Context, binding Binding, fn func(*State) error) error {
	return b.f.updateRead(ctx, func(s *State) error {
		if err := check(s, binding); err != nil {
			return err
		}
		return fn(s)
	})
}
func (b fileStoreBackend) Update(ctx context.Context, binding Binding, fn func(*State) error) error {
	release, err := b.f.retainOwner(binding)
	if err != nil {
		return err
	}
	defer release()
	return b.f.update(ctx, func(s *State) error {
		if err := check(s, binding); err != nil {
			return err
		}
		return fn(s)
	})
}
