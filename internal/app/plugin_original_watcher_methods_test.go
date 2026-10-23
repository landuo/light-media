package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

// These use the actual SDK fixture and production owner constructors, not an
// invented owner registry. Fixture watchers have no OS observer; no deletion is
// requested. These are prepared tests, not evidence until executed.
func TestOriginalWatcherMethodsRealRemoveLinkOwner(t *testing.T) {
	a, _, run := removeLinkOwnerFixture(t)
	ctx := context.Background()
	i, err := a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	p := &originalPluginInstance{app: a, installation: i}
	state, handled, err := originalWatcherState(ctx, p)
	if err != nil || !handled || !state {
		t.Fatalf("state=%v handled=%v err=%v", state, handled, err)
	}
	owner := a.pluginRemoveLink.owners[i.InstanceID]
	t.Cleanup(func() { _ = owner.h.Suspend(ctx) })
	p.installation.Generation++
	if _, _, err = originalWatcherState(ctx, p); !errors.Is(err, cleanuphost.ErrUncertain) {
		t.Fatal("stale generation accepted", err)
	}
	p.installation = i
	p.installation.InstanceID += "-absent-sibling"
	if _, _, err = originalWatcherState(ctx, p); err == nil {
		t.Fatal("missing sibling borrowed live owner")
	}
	p.installation = i
	if err = owner.lock(ctx); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = originalWatcherState(canceled, p)
	owner.unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = owner.h.Suspend(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err = originalWatcherState(ctx, p); !errors.Is(err, cleanuphost.ErrClosed) {
		t.Fatal("closed host read", err)
	}
}

func TestOriginalWatcherMethodsRealScrapeOwner(t *testing.T) {
	a, _, run, owner := scrapeActualFixture(t, true, false)
	ctx := context.Background()
	i, err := a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	p := &originalPluginInstance{app: a, installation: i}
	state, handled, err := originalWatcherState(ctx, p)
	if err != nil || !handled || !state {
		t.Fatalf("state=%v handled=%v err=%v", state, handled, err)
	}
	p.installation.Generation++
	if _, _, err = originalWatcherState(ctx, p); !errors.Is(err, scrapehost.ErrUncertain) {
		t.Fatal("stale generation accepted", err)
	}
	p.installation = i
	p.installation.InstanceID += "-absent-sibling"
	if _, _, err = originalWatcherState(ctx, p); err == nil {
		t.Fatal("missing sibling borrowed live owner")
	}
	p.installation = i
	if err = owner.lock(ctx); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = originalWatcherState(canceled, p)
	owner.unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	a.pluginScrapeClean.mu.Lock()
	a.pluginScrapeClean.closing = true
	a.pluginScrapeClean.mu.Unlock()
	if _, _, err = originalWatcherState(ctx, p); !errors.Is(err, scrapehost.ErrClosed) {
		t.Fatal("closing runtime accepted", err)
	}
}

func TestOriginalWatcherMethodsInertCaptureGap(t *testing.T) {
	t.Run("RemoveLink", func(t *testing.T) {
		a := newTestApp(t)
		a.config = Settings{}
		run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), map[string]any{"enabled": false}, true)
		removeLinkOwnerWorker(t, a, run)
		i, err := a.pluginStore.Installation(context.Background(), run.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		_, handled, err := originalWatcherState(context.Background(), &originalPluginInstance{app: a, installation: i})
		if !handled || err == nil || !strings.Contains(err.Error(), "live machine missing") {
			t.Fatal("inert owner must expose capture gap", handled, err)
		}
	})
	t.Run("ScrapeFileClean", func(t *testing.T) {
		a := newTestApp(t)
		a.config = Settings{}
		run := scrapeActualInstall(t, a, map[string]any{"enabled": false})
		scrapeActualWorker(t, a, run)
		i, err := a.pluginStore.Installation(context.Background(), run.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		_, handled, err := originalWatcherState(context.Background(), &originalPluginInstance{app: a, installation: i})
		if !handled || err == nil || !strings.Contains(err.Error(), "live machine missing") {
			t.Fatal("inert owner must expose capture gap", handled, err)
		}
	})
}

func TestOriginalWatcherMethodsSourceDeclarations(t *testing.T) {
	for _, id := range []string{"RemoveLink", "ScrapeFileClean"} {
		p := &originalPluginInstance{installation: PluginInstallation{PluginID: id}}
		services, handled, err := originalWatcherServices(context.Background(), p)
		if !handled || err != nil || services != nil {
			t.Fatal(id, services, handled, err)
		}
		commands, handled, err := originalWatcherCommands(context.Background(), p)
		if !handled || err != nil || len(commands) != 0 || (id == "RemoveLink") != (commands == nil) {
			t.Fatal(id, commands, handled, err)
		}
	}
	p := &originalPluginInstance{installation: PluginInstallation{PluginID: "Other"}}
	if _, handled, err := originalWatcherState(context.Background(), p); handled || err != nil {
		t.Fatal(handled, err)
	}
	if _, handled, _ := originalWatcherServices(context.Background(), p); handled {
		t.Fatal("unrelated services handled")
	}
	if _, handled, _ := originalWatcherCommands(context.Background(), p); handled {
		t.Fatal("unrelated commands handled")
	}
}
