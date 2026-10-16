package cleanuphost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExpandRootsRetainsQueueIdentityAndRequiresPublishedInvocation(t *testing.T) {
	f, h, _, target := delayedFixture(t)
	ctx := context.Background()
	before := snapshot(t, h)
	third := filepath.Join(f.root, "new-source")
	if err := os.Mkdir(third, 0700); err != nil {
		t.Fatal(err)
	}
	plan := append(append([]Root(nil), f.opts.Roots...), Root{"new-source", third, "source"})
	ticket := begin(t, h, f.opts.Binding, "expand-roots", "init")
	if err := h.ExpandRoots(ctx, ticket, "init-expand", plan); !errors.Is(err, ErrUnsafe) {
		t.Fatal("unpublished expansion", err)
	}
	to := f.opts.Binding
	to.Revision++
	publish(t, f, ticket, to, "init-expand")
	if err := h.ExpandRoots(ctx, ticket, "wrong", plan); !errors.Is(err, ErrUnsafe) {
		t.Fatal("unbound invocation", err)
	}
	if err := h.ExpandRoots(ctx, ticket, "init-expand", plan); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, h)
	if len(after.Roots) != 3 || !reflect.DeepEqual(after.Roots[:2], before.Roots) || !reflect.DeepEqual(after.Pins, before.Pins) || !reflect.DeepEqual(after.Machine, before.Machine) || !exists(target) {
		t.Fatal("expansion changed old capabilities or queued work")
	}
	if err := h.ApplyTransition(ctx, ticket, "init-expand", f.config(false, map[string]any{"monitor_dirs": third, "delete_history": false, "notify": false, "delete_torrents": false})); err != nil {
		t.Fatal(err)
	}
	if exists(target) {
		t.Fatal("ordinary init did not retain and drain old selection")
	}
}

func TestExpandRootsRejectsReplacementAndUnknownCommitDoesNotResume(t *testing.T) {
	for _, mode := range []string{"reorder", "overlap", "symlink", "commit_unknown", "guard"} {
		t.Run(mode, func(t *testing.T) {
			f, h, _, target := delayedFixture(t)
			ctx := context.Background()
			third := filepath.Join(f.root, "extra")
			if err := os.Mkdir(third, 0700); err != nil {
				t.Fatal(err)
			}
			ticket := begin(t, h, f.opts.Binding, "expand-"+mode, "init")
			to := f.opts.Binding
			to.Revision++
			publish(t, f, ticket, to, "init-expand")
			plan := append(append([]Root(nil), f.opts.Roots...), Root{"extra", third, "source"})
			switch mode {
			case "reorder":
				plan[0], plan[1] = plan[1], plan[0]
			case "overlap":
				plan[2].Path = f.src
			case "symlink":
				link := filepath.Join(f.root, "link")
				if err := os.Symlink(third, link); err != nil {
					t.Fatal(err)
				}
				plan[2].Path = link
			case "commit_unknown":
				h.opts.Journal = &rootUnknownCommitJournal{Journal: f.j}
			case "guard":
				f.allow.Store(false)
			}
			if err := h.ExpandRoots(ctx, ticket, "init-expand", plan); err == nil {
				t.Fatal("unsafe expansion accepted")
			}
			h.opts.Journal = f.j
			f.allow.Store(true)
			if !exists(target) || !h.paused || len(h.ops.roots) != 2 {
				t.Fatal("failed expansion resumed or replaced active capabilities")
			}
			state, err := f.j.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if mode == "commit_unknown" {
				want = 3
			}
			if len(state.Roots) != want {
				t.Fatal("unexpected publication", len(state.Roots), want)
			}
		})
	}
}

// Only this invocation loses its update reply; Read still exposes the actual
// committed marker to the authoritative reconciliation caller.
type rootUnknownCommitJournal struct{ Journal }

func (j *rootUnknownCommitJournal) Update(ctx context.Context, fn func(*Durable) error) error {
	if err := j.Journal.Update(ctx, fn); err != nil {
		return err
	}
	return errors.New("lost commit reply")
}
