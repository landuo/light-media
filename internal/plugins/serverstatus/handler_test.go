package serverstatus

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestServerStatusSDKWorker(t *testing.T) {
	if !slices.Contains(os.Args, "serverstatus-sdk-helper") {
		return
	}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, Manifest(), Handle); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestRealSDKFixedHostSnapshotsShareVolatileSampleAndConfigureResets(t *testing.T) {
	manifest := Manifest()
	if err := manifest.Validate(); err != nil || !manifest.HasPage || len(manifest.APIs) != 0 || len(manifest.Events) != 0 || !reflect.DeepEqual(manifest.Capabilities, []string{ConfigureCapability, SnapshotCapability}) {
		t.Fatal("invalid or invented source surface", err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	now := 1000.0
	network := &fakeNetwork{now: &now, step: Object{}, calls: []any{}}
	machine := New(network, func() float64 { return now })
	if err := machine.Init(json.RawMessage(`{"enable":true}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := plugins.Command{Path: executable, Args: []string{"-test.run=^TestServerStatusSDKWorker$", "--", "serverstatus-sdk-helper"}}
	snapshots := []Snapshot{}
	var expected any
	hostCalls := 0
	run := func(method, action, config string) plugins.RunResult {
		t.Helper()
		raw, _ := json.Marshal(plugins.Invocation{Action: action, Config: json.RawMessage(config), Input: json.RawMessage(`{"url":"http://unrelated.invalid/private","include_network_details":false,"cache":{"forged":true}}`)})
		if method == "config_form" {
			raw = nil
			expected = Form(false)
		}
		out, err := (plugins.Runner{Timeout: 5 * time.Second, MaxHostCalls: 1}).Run(ctx, command, manifest, "MPServerStatus", method, raw, func(_ context.Context, id plugins.Identity, capability string, payload json.RawMessage) (json.RawMessage, error) {
			hostCalls++
			if id.PluginID != manifest.PluginID || id.InstanceID != "MPServerStatus" || id.Version != Version {
				t.Error("unbound SDK identity")
			}
			switch capability {
			case ConfigureCapability:
				if string(payload) != "{}" || method != "invoke" || (action != "init" && action != "reload") {
					t.Error("configure scope widened")
				}
				// A real host reads its pinned stored configuration, never the
				// worker's forged URL/cache or a different worker config.
				err := machine.Init(json.RawMessage(`{"enable":true,"unknown":"preserved by host"}`))
				expected = nil
				return json.RawMessage(`null`), err
			case SnapshotCapability:
				want, _ := json.Marshal(Object{"include_network_details": method == "page"})
				if string(payload) != string(want) || (method != "page" && method != "dashboard") {
					t.Error("snapshot scope widened", capability, string(payload))
				}
				snapshot := machine.Load(context.Background(), method == "page")
				snapshots = append(snapshots, snapshot)
				if method == "page" {
					expected = Object{"render_mode": "vuetify", "page": Elements(snapshot, false)}
				} else {
					expected = Object{"cols": Object{"cols": 12, "md": 8}, "attrs": Object{"refresh": 10}, "elements": Elements(snapshot, true)}
				}
				return json.Marshal(snapshot)
			default:
				t.Error("uncompiled capability", capability)
				return nil, errors.New("uncompiled capability")
			}
		})
		if err != nil {
			t.Fatal("real SDK roundtrip", method, err)
		}
		var got any
		if decode(out.Result, &got) != nil || !equivalent(got, canonical(expected)) {
			t.Fatalf("SDK changed original render contract: %s", out.Result)
		}
		return out
	}
	run("config_form", "", "{}")
	network.step = Object{"response": Object{"status": 200, "text": "Active connections: 12\n1000 995 12345\nReading: 2 Writing: 3 Waiting: 7\n"}}
	// Direct get_dashboard remains callable for enable=false, like the source.
	run("dashboard", "", "{\"enable\":false}")
	now = 1010
	network.step = Object{"response": Object{"status": 200, "text": "Active connections: 12\n1010 1005 12445\nReading: 2 Writing: 3 Waiting: 7\n"}}
	run("dashboard", "", "{\"enable\":false}")
	if len(snapshots) != 2 || snapshots[0].Metrics["requests_rate"] != nil || number(snapshots[1].Metrics["requests_rate"]) <= 0 || len(network.calls) != 2 {
		t.Fatal("short workers lost shared sample or dashboard added detail probes")
	}
	now = 1020
	run("page", "", "{\"enable\":true}")
	if machine.DNSCache == nil || machine.TLSCache == nil || machine.Last == nil {
		t.Fatal("detail snapshot did not retain all three volatile caches")
	}
	before := hostCalls
	expected = Object{"render_mode": "vuetify", "page": []any{Object{"component": "div", "text": "插件未启用", "props": Object{"class": "text-center"}}}}
	run("page", "", "{\"enable\":false}")
	if hostCalls != before {
		t.Fatal("disabled page probed network")
	}
	run("invoke", "reload", "{\"enable\":false}")
	if !machine.State() || machine.Last != nil || machine.DNSCache != nil || machine.TLSCache != nil {
		t.Fatal("host ignored bound config or reload did not reset caches")
	}
}

func TestHandlerRejectsUnsupportedActionsAndMissingHost(t *testing.T) {
	for _, method := range []string{"page", "dashboard", "invoke", "other"} {
		raw, _ := json.Marshal(plugins.Invocation{Action: "init", Config: json.RawMessage(`{"enable":true}`)})
		if _, err := Handle(context.Background(), method, raw, nil); err == nil {
			t.Fatal("missing host or unsupported method accepted", method)
		}
	}
	calls := 0
	for _, action := range []string{"event", "service", "manual", "api", ""} {
		raw, _ := json.Marshal(plugins.Invocation{Action: action, Config: json.RawMessage(`{}`)})
		if _, err := Handle(context.Background(), "invoke", raw, func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, nil }); err == nil {
			t.Fatal("unsupported action accepted", action)
		}
	}
	if calls != 0 {
		t.Fatal("unsupported source surface called host")
	}
	for _, method := range []string{"config_form", "stop"} {
		if _, err := Handle(context.Background(), method, nil, nil); err != nil {
			t.Fatal("local source method required host", method, err)
		}
	}
	for _, method := range []string{"invoke", "page", "dashboard"} {
		raw, _ := json.Marshal(plugins.Invocation{Action: "init", Config: json.RawMessage(`{"enable":true}`)})
		sentinel := errors.New("bound synthetic host error")
		_, err := Handle(context.Background(), method, raw, func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, sentinel })
		if !errors.Is(err, sentinel) {
			t.Fatal("host failure hidden", method, err)
		}
	}
	raw, _ := json.Marshal(plugins.Invocation{Config: json.RawMessage(`{"enable":true}`)})
	if _, err := Handle(context.Background(), "page", raw, func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`not json`), nil
	}); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Fatal("invalid host snapshot accepted")
	}
}
