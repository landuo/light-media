package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	signin "light-media/internal/plugins/autosignin"
	journal "light-media/internal/plugins/autosignin/durable"
	seed "light-media/internal/plugins/cleaninvalidseed"
)

func originalEngineFixture(id string) (*originalPluginInstance, *signin.Engine, *seed.Machine) {
	a := &App{}
	in := PluginInstallation{PluginID: id, InstanceID: "owned-source", Version: "fixture", Generation: 7}
	p := &originalPluginInstance{app: a, installation: in, appliedConfig: json.RawMessage(`{"enabled":false}`)}
	if id == "AutoSignIn" {
		e := signin.New(signin.Inventory{}, nil)
		e.Enabled = true
		e.Cron = "0 1 * * *"
		o := &pluginAutoSignInOwner{binding: journal.Binding{Scope: journal.Scope{Generation: 7}}, originalEngine: e}
		slot := &pluginAutoSignInSlot{owner: o, ready: make(chan struct{}), gate: make(chan struct{}, 1)}
		close(slot.ready)
		a.pluginAutoSignIn.owners = map[string]*pluginAutoSignInSlot{in.InstanceID: slot}
		p.nativeState = e
		return p, e, nil
	}
	m := seed.New(nil)
	m.Config["enabled"] = true
	m.Config["cron"] = "0 1 * * *"
	a.pluginCleanInvalidSeed.owners = map[string]*pluginCleanInvalidSeedOwner{in.InstanceID: {generation: 7, gate: make(chan struct{}, 1), originalMachine: m}}
	p.nativeState = m
	return p, nil, m
}
func TestOriginalEngineMethodsReadMembersWithoutStore(t *testing.T) {
	for _, id := range []string{"AutoSignIn", "CleanInvalidSeed"} {
		t.Run(id, func(t *testing.T) {
			p, e, m := originalEngineFixture(id)
			ctx := context.Background()
			state, handled, err := originalEngineState(ctx, p)
			if err != nil || !handled || !state {
				t.Fatalf("state %v %v %v", state, handled, err)
			}
			jobs, handled, err := originalEngineServices(ctx, p)
			if err != nil || !handled || len(jobs) != 1 || jobs[0].ID != id {
				t.Fatalf("jobs %+v %v %v", jobs, handled, err)
			}
			if e != nil {
				e.Enabled = false
			} else {
				m.Config["enabled"] = false
			}
			state, _, err = originalEngineState(ctx, p)
			if err != nil || state {
				t.Fatalf("live disabled %v %v", state, err)
			}
			// Direct declaration is not filtered here; the common manager owns filtering.
			commands, handled, err := originalEngineCommands(ctx, p)
			if err != nil || !handled || len(commands) == 0 {
				t.Fatalf("commands %v %v %v", commands, handled, err)
			}
			p.installation.InstanceID = "owned-clone"
			if _, _, err = originalEngineState(ctx, p); err == nil {
				t.Fatal("clone adopted source")
			}
			p.installation.InstanceID = "owned-source"
			p.installation.Generation++
			if _, _, err = originalEngineState(ctx, p); err == nil {
				t.Fatal("stale generation accepted")
			}
		})
	}
}
func TestOriginalEngineMethodsCanceledAndMissing(t *testing.T) {
	for _, id := range []string{"AutoSignIn", "CleanInvalidSeed"} {
		p, _, _ := originalEngineFixture(id)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := originalEngineState(ctx, p); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
		p.nativeState = nil
		if _, _, err := originalEngineState(context.Background(), p); err == nil {
			t.Fatal("missing native pointer accepted")
		}
	}
}
func TestOriginalEngineCaptureSeparatesFreshAndRepeat(t *testing.T) {
	for _, id := range []string{"AutoSignIn", "CleanInvalidSeed"} {
		t.Run(id, func(t *testing.T) {
			p, oldEngine, oldMachine := originalEngineFixture(id)
			call := &originalInitializationCall{object: p}
			ctx := context.WithValue(context.Background(), originalInitializingObjectKey{}, call)
			if oldEngine != nil {
				o := p.app.pluginAutoSignIn.owners[p.installation.InstanceID].owner
				fresh, err := originalAutoSignInEngineForCall(ctx, o, p.installation, "init", "UTC")
				if err != nil || fresh == oldEngine || call.nativeState != fresh {
					t.Fatalf("fresh %p old %p err %v", fresh, oldEngine, err)
				}
				fresh.Enabled = true
				call.nativeBefore = fresh
				repeat, err := originalAutoSignInEngineForCall(ctx, o, p.installation, "init", "UTC")
				if err != nil || repeat != fresh || !signin.Truth(repeat.Enabled) {
					t.Fatal("repeat lost actual members")
				}
			} else {
				o := p.app.pluginCleanInvalidSeed.owners[p.installation.InstanceID]
				fresh := originalCleanSeedMachineForCall(ctx, o, p.installation, "init")
				if fresh == oldMachine || call.nativeState != fresh {
					t.Fatal("fresh adopted old machine")
				}
				fresh.Config["enabled"] = true
				call.nativeBefore = fresh
				repeat := originalCleanSeedMachineForCall(ctx, o, p.installation, "init")
				if repeat != fresh || !seed.Truth(repeat.GetState()) {
					t.Fatal("repeat lost actual members")
				}
			}
		})
	}
}
