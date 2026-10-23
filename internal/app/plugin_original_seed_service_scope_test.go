package app

import (
	"context"
	"errors"
	"testing"
)

// Uses the actually initialized App/primary IYUU/SeedRescue owner fixture.
// This is a guard and denied-call test, not proof of a successful network probe.
func TestPluginOriginalSeedServiceScopeIdentityAndRevocation(t *testing.T) {
	a, p, probes := originalMachineInitSeed(t)
	o := originalMachineInitSeedOwner(t, p)
	ctx := context.Background()
	if err := o.lock(ctx); err != nil {
		t.Fatal(err)
	}
	defer o.unlock()
	if o.active != nil {
		t.Fatal("fixture unexpectedly holds an execution call")
	}
	readCtx, closeRead := originalSeedServiceContext(ctx, p, o)
	defer closeRead()
	read, ok := o.originalServiceRead(readCtx)
	if !ok || read == nil {
		t.Fatal("actual owner cannot resolve its own scope")
	}
	if err := read.guard(readCtx); err != nil {
		t.Fatalf("valid original identity rejected: %v", err)
	}
	if _, ok := (&pluginSeedRescueOwner{}).originalServiceRead(readCtx); ok {
		t.Fatal("different owner recognized scope")
	}

	t.Run("wrong-running-object", func(t *testing.T) {
		a.pluginRunningInstances.Store(o.instance, &originalPluginInstance{})
		defer a.pluginRunningInstances.Store(o.instance, p)
		if err := read.guard(readCtx); err == nil {
			t.Fatal("replacement running object inherited scope")
		}
	})
	t.Run("missing-running-object", func(t *testing.T) {
		a.pluginRunningInstances.Delete(o.instance)
		defer a.pluginRunningInstances.Store(o.instance, p)
		if err := read.guard(readCtx); err == nil {
			t.Fatal("unregistered object retained scope")
		}
	})
	t.Run("owner-generation", func(t *testing.T) {
		old := o.generation
		o.generation++
		defer func() { o.generation = old }()
		if err := read.guard(readCtx); err == nil {
			t.Fatal("changed generation inherited scope")
		}
	})
	t.Run("owner-machine", func(t *testing.T) {
		old := o.machine
		o.machine = nil
		defer func() { o.machine = old }()
		if err := read.guard(readCtx); err == nil {
			t.Fatal("changed machine inherited scope")
		}
	})
	t.Run("native-state", func(t *testing.T) {
		p.configMu.Lock()
		old := p.nativeState
		p.nativeState = nil
		p.configMu.Unlock()
		defer func() { p.configMu.Lock(); p.nativeState = old; p.configMu.Unlock() }()
		if err := read.guard(readCtx); err == nil {
			t.Fatal("wrong captured native state inherited scope")
		}
	})
	t.Run("cancelled-context", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(readCtx)
		cancel()
		if err := read.guard(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled scope error=%v", err)
		}
	})
	t.Run("service-scope-does-not-authorize-other-reads", func(t *testing.T) {
		count := probes.Load()
		client, _, err := o.downloader(readCtx, "Machine fixture qB")
		if client != nil {
			client.Close()
			t.Error("execution downloader returned client under service-only scope")
		}
		if err == nil {
			t.Error("execution downloader accepted service-only scope")
		}
		if _, err := o.QueryTorrents(readCtx, "Machine fixture qB", []string{"0000000000000000000000000000000000000000"}); err == nil {
			t.Error("QueryTorrents accepted service-only scope")
		}
		if probes.Load() != count {
			t.Error("denied calls reached network")
		}
		if o.active != nil {
			t.Error("scope fabricated an execution call")
		}
	})
	if err := read.guard(readCtx); err != nil {
		t.Fatalf("restored exact identity rejected: %v", err)
	}
	closeRead()
	if err := read.guard(readCtx); err == nil {
		t.Fatal("closed scope remained valid")
	}
	count := probes.Load()
	if _, err := o.Service(readCtx, "Machine fixture qB"); err == nil {
		t.Fatal("closed service scope reached authorized service")
	}
	if probes.Load() != count {
		t.Fatal("revoked scope reached network")
	}
}
