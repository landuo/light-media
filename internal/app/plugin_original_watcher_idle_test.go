package app

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/removelink"
	"light-media/internal/plugins/scrapefileclean"
)

func TestOriginalWatcherIdleActualApp(t *testing.T) {
	for _, manifest := range []plugins.Manifest{removelink.Manifest(), scrapefileclean.Manifest()} {
		t.Run(manifest.PluginID, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			a.config = Settings{}
			installCloneSource(t, a.pluginStore, manifest)
			if _, err := a.pluginStore.SaveConfig(ctx, manifest.PluginID, 0, map[string]any{"enabled": true, "monitor_dirs": ""}); err != nil {
				t.Fatal(err)
			}
			p := a.startOriginalPlugin(ctx, manifest.PluginID)
			if p == nil {
				t.Fatal("real original start did not retain zero-root object")
			}
			state, handled, err := originalWatcherState(ctx, p)
			if err != nil || !handled || !state {
				t.Fatal(state, handled, err)
			}
			first := p.nativeState
			if first == nil {
				t.Fatal("actual Init did not capture machine")
			}
			if err = p.init(ctx); err != nil {
				t.Fatal("direct repeated init", err)
			}
			if p.nativeState != first {
				t.Fatal("direct init replaced logical object")
			}
			// Clone is a real production installation and must not alias source state.
			sibling, err := a.pluginStore.Clone(ctx, manifest.PluginID, PluginCloneRequest{Suffix: "IdleSibling"})
			if err != nil {
				t.Fatal(err)
			}
			q := a.startOriginalPlugin(ctx, sibling.InstanceID)
			if q == nil {
				t.Fatal("sibling start")
			}
			if q.nativeState == first || q.nativeState == nil {
				t.Fatal("sibling machine alias")
			}
			// Original Clone explicitly writes enabled=false. Compare the actual
			// sibling before/after state rather than assuming it matches source.
			siblingState, siblingHandled, err := originalWatcherState(ctx, q)
			if err != nil || !siblingHandled {
				t.Fatal("sibling state before target stop", siblingState, siblingHandled, err)
			}
			if handled, err := originalWatcherStop(ctx, a, manifest.PluginID, manifest.PluginID); !handled || err != nil {
				t.Fatal(handled, err)
			}
			if state, handled, err := originalWatcherState(ctx, q); err != nil || !handled || state != siblingState {
				t.Fatal("target stop changed sibling", state, siblingState, handled, err)
			}
			// New logical object, even if some external registry retained old metadata.
			fresh := a.startOriginalPlugin(ctx, manifest.PluginID)
			if fresh == nil {
				t.Fatal("fresh start")
			}
			if fresh.nativeState == nil || fresh.nativeState == first {
				t.Fatal("fresh start reused old logical object")
			}
		})
	}
}

// Focused mutation/error test supplements, not substitutes for, real App start.
func TestOriginalWatcherIdlePartialAndNoAuthority(t *testing.T) {
	ctx := context.Background()
	for _, id := range []string{"RemoveLink", "ScrapeFileClean"} {
		t.Run(id, func(t *testing.T) {
			p := &originalPluginInstance{installation: PluginInstallation{PluginID: id}}
			call := &originalInitializationCall{object: p}
			callCtx := context.WithValue(ctx, originalInitializingObjectKey{}, call)
			if id == "RemoveLink" {
				o := &pluginRemoveLinkOwner{}
				if err := o.initOriginalIdleMachine(callCtx, json.RawMessage(`{"enabled":false}`)); err != nil {
					t.Fatal(err)
				}
				first := o.originalMachine
				call.nativeBefore = first
				if err := o.initOriginalIdleMachine(callCtx, json.RawMessage(`{"enabled":true,"custom_scrap_extensions":1}`)); err == nil {
					t.Fatal("bad source member accepted")
				}
				if o.originalMachine != first || call.nativeState != first || !removelink.Truth(first.StateValue()) {
					t.Fatal("partial mutation lost")
				}
				if err := o.initOriginalIdleMachine(callCtx, json.RawMessage(`{"enabled":true,"monitor_dirs":"/never-authorized"}`)); err == nil {
					t.Fatal("no-authority scan accepted")
				}
				ops, ok := first.Ops.(*originalRemoveLinkNoFiles)
				if !ok || !ops.touched {
					t.Fatal("missing fail-closed Ops")
				}
			} else {
				o := &pluginScrapeCleanOwner{}
				if err := o.initOriginalIdleMachine(callCtx, json.RawMessage(`{"enabled":false}`)); err != nil {
					t.Fatal(err)
				}
				first := o.originalMachine
				call.nativeBefore = first
				if err := o.initOriginalIdleMachine(callCtx, json.RawMessage(`{"enabled":true,"custom_scrap_extensions":1}`)); err == nil {
					t.Fatal("bad source member accepted")
				}
				if o.originalMachine != first || call.nativeState != first || !first.GetState() {
					t.Fatal("partial mutation lost")
				}
				if err := o.initOriginalIdleMachine(callCtx, json.RawMessage(`{"enabled":true,"monitor_dirs":"/never-authorized"}`)); err == nil {
					t.Fatal("no-authority scan accepted")
				}
				ops, ok := first.Ops.(*originalScrapeCleanNoFiles)
				if !ok || !ops.touched {
					t.Fatal("missing fail-closed Ops")
				}
			}
		})
	}
}

func TestOriginalWatcherIdleToActiveActualApp(t *testing.T) {
	for _, manifest := range []plugins.Manifest{removelink.Manifest(), scrapefileclean.Manifest()} {
		t.Run(manifest.PluginID, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			a.config = Settings{}
			removeLinkOwnerFakeWatch(a)
			scrapeActualIdle(a)
			installCloneSource(t, a.pluginStore, manifest)
			if _, err := a.pluginStore.SaveConfig(ctx, manifest.PluginID, 0, map[string]any{"enabled": true}); err != nil {
				t.Fatal(err)
			}
			p := a.startOriginalPlugin(ctx, manifest.PluginID)
			if p == nil {
				t.Fatal("zero-root start")
			}
			before := p.nativeState
			root := t.TempDir()
			a.cfgMu.Lock()
			a.config.AllowedRoots = []string{root}
			a.cfgMu.Unlock()
			cfg, err := a.pluginStore.Config(ctx, manifest.PluginID)
			if err != nil {
				t.Fatal(err)
			}
			// Existing roots may only change through the actual reserved owner
			// lifecycle route. Execute its real queued run before direct re-init.
			body := map[string]any{"revision": cfg.Revision, "config": map[string]any{"enabled": true, "monitor_dirs": root}}
			if manifest.PluginID == "RemoveLink" {
				if _, err = removeLinkOwnerHTTP(t, a, manifest.PluginID, "config", body); err != nil {
					t.Fatal(err)
				}
				run := removeLinkOwnerClaim(t, a, manifest.PluginID)
				removeLinkOwnerWorker(t, a, run)
			} else {
				if _, err = scrapeActualHTTP(t, a, manifest.PluginID, "config", body); err != nil {
					t.Fatal(err)
				}
				run, claimErr := a.pluginStore.claimRunForInstance(ctx, manifest.PluginID)
				if claimErr != nil {
					t.Fatal(claimErr)
				}
				scrapeActualWorker(t, a, run)
			}
			if p.nativeState != before {
				t.Fatal("authorized publication replaced original logical object")
			}
			if err = p.init(ctx); err != nil {
				t.Fatal("real authority transition", err)
			}
			if before == nil || p.nativeState != before {
				t.Fatal("idle-to-active replaced logical machine")
			}
			if manifest.PluginID == "RemoveLink" {
				o := a.pluginRemoveLink.owners[manifest.PluginID]
				if o == nil || o.h == nil {
					t.Fatal("host missing")
				}
				m, err := o.h.OriginalMachine(ctx)
				if err != nil || m != before {
					t.Fatal("host did not adopt actual machine", err)
				}
				t.Cleanup(func() { _ = o.h.Suspend(ctx) })
			} else {
				o := a.pluginScrapeClean.owners[manifest.PluginID]
				if o == nil || o.h == nil {
					t.Fatal("host missing")
				}
				m, err := o.h.OriginalMachine(ctx)
				if err != nil || m != before {
					t.Fatal("host did not adopt actual machine", err)
				}
				t.Cleanup(func() { _ = o.h.Suspend(ctx) })
			}
		})
	}
}
