package brushflow

import (
	"context"
	"sync"
	"testing"
	"time"
)

type blockingGlobalHost struct {
	*memoryHost
	entered, release chan struct{}
	once             sync.Once
}

func (h *blockingGlobalHost) Torrents(ctx context.Context, name string) ([]DownloadState, error) {
	h.once.Do(func() { close(h.entered) })
	select {
	case <-h.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return h.memoryHost.Torrents(ctx, name)
}
func TestCommittedOtherTaskConfigPreservesSourceGlobalDeletionLocks(t *testing.T) {
	h := &blockingGlobalHost{memoryHost: testHost(), entered: make(chan struct{}), release: make(chan struct{})}
	cfg := Config{Settings: Row{"enabled": true, "global_proxy_delete": true, "global_delete_size_range": "0-1"}, Tasks: []Task{
		NormalizeTask(Row{"id": "first", "name": "A", "site_id": 1, "downloader": "qb"}, h.NewID),
		NormalizeTask(Row{"id": "second", "name": "B", "site_id": 1, "downloader": "qb"}, h.NewID),
	}}
	p := NewReadProjection(h, cfg)
	done := make(chan error, 1)
	go func() { _, err := p.GlobalDelete(context.Background()); done <- err }()
	select {
	case <-h.entered:
	case <-time.After(time.Second):
		t.Fatal("actual global deletion never entered downloader read")
	}
	defer func() {
		close(h.release)
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("global deletion did not release source locks")
		}
	}()
	next := p.Config()
	next.Tasks = append(next.Tasks, NormalizeTask(Row{"id": "third", "name": "C", "site_id": 1, "downloader": "qb"}, h.NewID))
	p.ApplyConfig(next)
	if !p.TaskBusy("first") || !p.TaskBusy("second") || p.TaskBusy("third") {
		t.Fatal("committed config replaced source lock ownership")
	}
	if n, err := p.GlobalDelete(context.Background()); err != nil || n != 0 {
		t.Fatal("second global deletion entered", n, err)
	}
	// The first read still owns both source locks until its native IO returns.
	select {
	case <-done:
		t.Fatal("held native IO completed unexpectedly")
	default:
	}
}
