package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"testing"
)

func TestSourcePrivateSchedulerOwnershipAdmissionBoundaries(t *testing.T) {
	a := newTestApp(t)
	for _, row := range []struct {
		name             string
		managed, cleanup bool
		ownership, phase string
	}{{"undeclared", false, false, "private_scheduler", "init"}, {"unknown", true, false, "scheduler_other", "init"}, {"cleanup-combination", true, true, "private_scheduler", "init"}, {"stop-root", true, false, "private_scheduler", "stop"}} {
		t.Run(row.name, func(t *testing.T) {
			caps := []string{plugins.BackgroundSubmitCapability}
			if row.cleanup {
				caps = append(caps, plugins.CleanupSubmitCapability)
			}
			p := &originalPluginInstance{app: a, manifest: plugins.Manifest{ManagedPrivateScheduler: row.managed, Methods: []string{"child", "stop_service"}, Capabilities: caps}}
			raw, _ := json.Marshal(map[string]any{"method": "child", "payload": nil, "ownership": row.ownership})
			_, e := a.originalSubmitResource(context.Background(), originalBaseCallScope{object: p, phase: row.phase, validate: func(context.Context) error { return nil }}, raw)
			if pluginErrorCode(e) != "PLUGIN_CAPABILITY_DENIED" {
				t.Fatal("synthetic explicit negative grant boundary", e)
			}
		})
	}
	p := &originalPluginInstance{app: a}
	foreign := &originalPluginInstance{app: a}
	lease, _, e := foreign.reserveOriginalOwnedSubmission(nil, "private_scheduler")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { foreign.releaseOriginalSubmission(lease); a.wg.Done() }()
	p.stopping.Store(true)
	if _, _, e = p.reserveOriginalOwnedSubmission(lease, ""); pluginErrorCode(e) != "PLUGIN_CALL_CLOSED" {
		t.Fatal("foreign private lease inherited another owner", e)
	}
	p = &originalPluginInstance{app: a}
	lease2, _, e := p.reserveOriginalOwnedSubmission(nil, "private_scheduler")
	if e != nil {
		t.Fatal(e)
	}
	p.releaseOriginalSubmission(lease2)
	a.wg.Done()
	p.stopping.Store(true)
	if _, _, e = p.reserveOriginalOwnedSubmission(lease2, ""); pluginErrorCode(e) != "PLUGIN_CALL_CLOSED" {
		t.Fatal("returned/retired private lease regained authority", e)
	}
}
