package larkmessager

import (
	"context"
	"sync"
)

type testKV struct {
	mu   sync.Mutex
	data map[string]Object
}

func newTestKV() *testKV { return &testKV{data: map[string]Object{}} }
func (k *testKV) Get(ctx context.Context, key string) (Object, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return clone(k.data[key]), ctx.Err()
}
func (k *testKV) Put(ctx context.Context, key string, v Object) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.data[key] = clone(v)
	return ctx.Err()
}
func (k *testKV) Delete(ctx context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.data, key)
	return ctx.Err()
}
