package durable

import (
	"context"
	source "light-media/internal/plugins/autosignin"
	"testing"
)

func TestNativeEngineRetainsIdentityAndDetachesClock(t *testing.T) {
	store, binding, _ := fixture(t)
	c := controller(store, binding, nil)
	e := source.New(source.Inventory{}, nil)
	c.Engine = e
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := c.Execute(ctx, "literal-init", Command{Action: "init", Config: source.Object{"enabled": true, "cron": "0 1 * * *"}}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if c.Engine != e || !source.Truth(e.Enabled) || e.Effects != nil {
		t.Fatal("actual object or callback lifecycle lost")
	}
	// The retained clock must work after the old run context has ended.
	if got := e.Now(); !got.Equal(c.Dependencies.Now()) {
		t.Fatalf("detached clock %v", got)
	}
	e.StartTime = "actual-member-not-in-config"
	if _, err := c.Execute(context.Background(), "literal-repeat", Command{Action: "init", Config: source.Object{}}); err != nil {
		t.Fatal(err)
	}
	if c.Engine != e || e.StartTime != "actual-member-not-in-config" || !source.Truth(e.Enabled) {
		t.Fatal("empty init reconstructed object")
	}
	state, err := store.State(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range state.Runs {
		for _, entries := range run.Lanes {
			for _, entry := range entries {
				if entry.Kind == "service.definitions" {
					t.Fatal("literal init called central get_service")
				}
			}
		}
	}
}

func TestNativeEngineFailedInitKeepsActualPartialMembers(t *testing.T) {
	store, binding, _ := fixture(t)
	c := controller(store, binding, nil)
	e := source.New(source.Inventory{}, nil)
	c.Engine = e
	_, err := c.Execute(context.Background(), "literal-failure", Command{Action: "init", Config: source.Object{"enabled": true, "cron": "0 7 * * *", "sign_sites": 42}})
	if err == nil {
		t.Fatal("expected original filter exception")
	}
	if c.Engine != e || !source.Truth(e.Enabled) || e.Cron != "0 7 * * *" || e.Effects != nil {
		t.Fatal("failed initialization discarded partial members or retained authority")
	}
}
