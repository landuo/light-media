package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	cf "light-media/internal/plugins/cloudflarespeedtest"
	sr "light-media/internal/plugins/seedrescue"
)

type originalMachineCFHost struct {
	cf.Host
	cf.Runtime
	stops int
}

func (h *originalMachineCFHost) Guard(context.Context) error    { return nil }
func (h *originalMachineCFHost) StopOnce(context.Context) error { h.stops++; return nil }

type originalMachineSeedHost struct {
	sr.Host
	writes     int
	serviceErr error
	calls      int
}

func (h *originalMachineSeedHost) Guard(context.Context) error { return nil }
func (h *originalMachineSeedHost) LoadData(context.Context, string) (json.RawMessage, error) {
	return nil, nil
}
func (h *originalMachineSeedHost) UpdateConfig(context.Context, sr.Object) error {
	h.writes++
	return nil
}
func (h *originalMachineSeedHost) IYUUAvailable(context.Context) bool { return true }
func (h *originalMachineSeedHost) IYUUEnabled(context.Context) bool   { return true }
func (h *originalMachineSeedHost) Service(context.Context, string) (*sr.Service, error) {
	h.calls++
	return &sr.Service{Kind: "qbittorrent", Connected: true}, h.serviceErr
}
func (h *originalMachineSeedHost) ValidateCron(string) error { return nil }
func (h *originalMachineSeedHost) Now() time.Time {
	return time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
}
func (h *originalMachineSeedHost) Log(context.Context, string, string) {}

func TestOriginalMachineMethodsCloudflareRetained(t *testing.T) {
	ctx := context.Background()
	h := &originalMachineCFHost{}
	m, err := cf.New(h, h)
	if err != nil {
		t.Fatal(err)
	}
	cfg := cf.Object{"cf_ip": "192.0.2.1", "cron": "0 1 * * *", "ipv4": true}
	if err = m.Init(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	o := &pluginCloudflareOwner{machine: m, gate: make(chan struct{}, 1), generation: 7, timezone: "UTC"}
	a.pluginCloudflare.owners = map[string]*pluginCloudflareOwner{"source:7": o}
	p := &originalPluginInstance{app: a, installation: PluginInstallation{PluginID: "CloudflareSpeedTest", InstanceID: "source", Generation: 7}, appliedConfig: json.RawMessage(`{"ipv4":false}`)}
	state, handled, err := originalMachineState(ctx, p)
	if err != nil || !handled || !state {
		t.Fatalf("state=%v handled=%v err=%v", state, handled, err)
	}
	jobs, handled, err := originalMachineServices(ctx, p)
	if err != nil || !handled || len(jobs) != 1 || jobs[0].ID != "CloudflareSpeedTest" || jobs[0].Handler != "cloudflare" || jobs[0].Trigger.Cron != "0 1 * * *" {
		t.Fatalf("jobs=%+v handled=%v err=%v", jobs, handled, err)
	}
	if h.stops != 1 {
		t.Fatalf("getter reinitialized machine: stops=%d", h.stops)
	}
	cfg["cron"] = "invalid"
	if err = m.Init(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, _, err = originalMachineServices(ctx, p); err == nil {
		t.Fatal("invalid live cron was hidden")
	}
	p.installation.InstanceID = "clone"
	if _, handled, err = originalMachineState(ctx, p); !handled || err == nil {
		t.Fatal("missing clone silently reused source")
	}
	p.installation.InstanceID = "source"
	p.installation.Generation = 8
	if _, _, err = originalMachineState(ctx, p); err == nil {
		t.Fatal("stale generation accepted")
	}
	if len(o.gate) != 0 {
		t.Fatal("gate leaked")
	}
}

func TestOriginalMachineMethodsSeedRetained(t *testing.T) {
	t.Setenv("TZ", "UTC")
	ctx := context.Background()
	h := &originalMachineSeedHost{}
	m, err := sr.New(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	cfg := sr.Object{"enabled": true, "onlyonce": true, "token": "fixture", "target_site_id": 1, "rss_urls": "https://example.invalid/rss", "source_site_ids": []any{2}, "downloader": "fixture", "cron": "*/30 * * * *"}
	if err = m.Init(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	o := &pluginSeedRescueOwner{machine: m, gate: make(chan struct{}, 1), generation: 3}
	a.pluginSeedRescue.owners = map[string]*pluginSeedRescueOwner{"source:3": o}
	p := &originalPluginInstance{app: a, installation: PluginInstallation{PluginID: "SeedRescue", InstanceID: "source", Generation: 3}, appliedConfig: json.RawMessage(`{"enabled":false}`)}
	if state, handled, err := originalMachineState(ctx, p); err != nil || !handled || !state {
		t.Fatalf("state=%v handled=%v err=%v", state, handled, err)
	}
	jobs, handled, err := originalMachineServices(ctx, p)
	if err != nil || !handled || len(jobs) != 2 {
		t.Fatalf("jobs=%+v handled=%v err=%v", jobs, handled, err)
	}
	if jobs[0].ID != "SeedRescue.Scan" || jobs[1].ID != "SeedRescue.RunOnce" || jobs[1].Trigger.RunAt == "" || jobs[0].Handler != "seedrescue" {
		t.Fatalf("jobs=%+v", jobs)
	}
	if h.writes != 1 || h.calls != 1 {
		t.Fatalf("getter changed init or dependency semantics: writes=%d calls=%d", h.writes, h.calls)
	}
	failure := errors.New("host admission missing")
	h.serviceErr = failure
	if _, _, err = originalMachineServices(ctx, p); !errors.Is(err, failure) {
		t.Fatalf("host error hidden: %v", err)
	}
	if len(o.gate) != 0 {
		t.Fatal("gate leaked")
	}
}

func TestOriginalMachineMethodsMissingCancelledUnsupported(t *testing.T) {
	for _, id := range []string{"CloudflareSpeedTest", "SeedRescue"} {
		a := &App{}
		p := &originalPluginInstance{app: a, installation: PluginInstallation{PluginID: id, InstanceID: "fixture"}}
		if _, handled, err := originalMachineState(context.Background(), p); !handled || err == nil {
			t.Fatalf("%s missing owner: %v %v", id, handled, err)
		}
		gate := make(chan struct{}, 1)
		gate <- struct{}{}
		a.pluginCloudflare.owners = map[string]*pluginCloudflareOwner{"fixture:0": {gate: gate}}
		a.pluginSeedRescue.owners = map[string]*pluginSeedRescueOwner{"fixture:0": {gate: gate}}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := originalMachineState(ctx, p); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s cancellation=%v", id, err)
		}
		<-gate
		if _, _, err := originalMachineServices(context.Background(), p); err == nil {
			t.Fatalf("%s missing machine accepted", id)
		}
		if len(gate) != 0 {
			t.Fatal("gate leaked")
		}
	}
	p := &originalPluginInstance{installation: PluginInstallation{PluginID: "Other"}}
	if _, handled, err := originalMachineState(context.Background(), p); handled || err != nil {
		t.Fatal("unsupported state handled")
	}
	if _, handled, err := originalMachineServices(context.Background(), p); handled || err != nil {
		t.Fatal("unsupported services handled")
	}
}
