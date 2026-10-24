package seedhost

import (
	"context"
	"database/sql"
	"errors"
	seed "light-media/internal/plugins/cleaninvalidseed"
	"testing"
)

func TestNativeMachineRetainsMembersWithoutRuntimeDecode(t *testing.T) {
	f := newFixture(t, nil, nil)
	m := seed.New(nil)
	ctx := WithMachine(context.Background(), m)
	config := raw(map[string]any{"enabled": true, "cron": "bad cron", "min_seeding_days": 0})
	inv := f.run(t, "literal-init", "init", "")
	f.c.config = config
	if _, err := f.db.Exec(`UPDATE core SET config_sha=?`, digest(config)); err != nil {
		t.Fatal(err)
	}
	if err := f.o.Configure(ctx, inv, config); err != nil {
		t.Fatal(err)
	}
	if !seed.Truth(m.GetState()) || m.Host != nil {
		t.Fatal("actual state or fresh host lifecycle lost")
	}
	if _, err := m.ServiceTrigger(); err == nil {
		t.Fatal("getter swallowed invalid live cron")
	}
	m.Config["owned_marker"] = "actual-member-not-in-config"
	// Poison legacy serialization to prove no fallback reconstructs this object.
	f.o.runtimeConfig = []byte(`not-json`)
	repeat := f.run(t, "literal-repeat", "init", "")
	f.c.config = raw(map[string]any{})
	if _, err := f.db.Exec(`UPDATE core SET config_sha=?`, digest(f.c.config)); err != nil {
		t.Fatal(err)
	}
	if err := f.o.Configure(ctx, repeat, raw(map[string]any{})); err != nil {
		t.Fatal(err)
	}
	if m.Config["owned_marker"] != "actual-member-not-in-config" || !seed.Truth(m.GetState()) || m.Host != nil {
		t.Fatal("repeat replaced actual machine")
	}
	state, err := f.j.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range state.Triggers {
		if tr.Kind == "service" {
			t.Fatal("literal init registered central service twice")
		}
	}
}

func TestNativeMachineFailedInitKeepsActualPartialMembers(t *testing.T) {
	f := newFixture(t, nil, nil)
	m := seed.New(nil)
	config := raw(map[string]any{"enabled": true, "onlyonce": true, "min_seeding_days": 0})
	inv := f.run(t, "literal-failure", "init", "")
	f.c.config = config
	f.o.opts.Core = &nativeFailureCore{fixtureCore: f.c}
	if _, err := f.db.Exec(`UPDATE core SET config_sha=?`, digest(config)); err != nil {
		t.Fatal(err)
	}
	if err := f.o.Configure(WithMachine(context.Background(), m), inv, config); err == nil {
		t.Fatal("expected config-save failure")
	}
	if !seed.Truth(m.GetState()) || seed.Truth(m.Config["onlyonce"]) || len(m.Schedulers) != 1 || m.Host != nil {
		t.Fatal("failed init lost original mutations or retained host authority")
	}
}

// Owned fault seam: no downloader, filesystem mutation, or external request.
type nativeFailureCore struct{ *fixtureCore }

func (c *nativeFailureCore) Config(context.Context, *sql.Tx, Invocation, Effect) (ConfigPublication, error) {
	return ConfigPublication{}, errors.New("owned config publication failure")
}
