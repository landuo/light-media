package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

func TestPluginConfigurationReadRegistryUsesCompiledDeclarations(t *testing.T) {
	// Different IDs, routes, and payload schemas are deliberately independent of
	// BrushFlow. These are host-compiled test registrations, not HTTP declarations.
	a := builtin.CategoryEditorManifest()
	b := builtin.CustomHostsManifest()
	a.Capabilities = append(a.Capabilities, "fixture.config.read")
	b.Capabilities = append(b.Capabilities, "fixture.other.read")
	a.APIs = []plugins.HTTPAPI{{Key: "config", Path: "/configuration", Methods: []string{"GET"}, Auth: "bear"}}
	b.APIs = []plugins.HTTPAPI{{Key: "inspect", Path: "/inspect/{name}", Methods: []string{"GET"}, Auth: "bear"}}
	registrations := []pluginConfigurationHTTPRegistration{
		{manifest: a, capability: "fixture.config.read", keys: []string{"config"}, read: func(context.Context, pluginFormView, plugins.HTTPRequest) (json.RawMessage, error) {
			return json.RawMessage(`{"text":"first"}`), nil
		}},
		{manifest: b, capability: "fixture.other.read", keys: []string{"inspect"}, read: func(context.Context, pluginFormView, plugins.HTTPRequest) (json.RawMessage, error) {
			return json.RawMessage(`{"items":[1,2]}`), nil
		}},
	}
	for i, m := range []plugins.Manifest{a, b} {
		read := resolvePluginConfigurationHTTPReader(registrations, m, m.APIs[0], "GET")
		if read == nil {
			t.Fatal("independent compiled reader missing", m.PluginID)
		}
		got, e := read(context.Background(), pluginFormView{}, plugins.HTTPRequest{})
		want := []string{`{"text":"first"}`, `{"items":[1,2]}`}[i]
		if e != nil || string(got) != want {
			t.Fatal(string(got), e)
		}
		if resolvePluginConfigurationHTTPReader(registrations, m, m.APIs[0], "POST") != nil {
			t.Fatal("write granted")
		}
		changed := m.APIs[0]
		changed.Auth = "anonymous"
		if resolvePluginConfigurationHTTPReader(registrations, m, changed, "GET") != nil {
			t.Fatal("changed declaration granted")
		}
		stale := m
		stale.Version = "unknown"
		if resolvePluginConfigurationHTTPReader(registrations, stale, m.APIs[0], "GET") != nil {
			t.Fatal("unknown version granted")
		}
	}
	wrong := a
	wrong.PluginID = "Unregistered"
	if resolvePluginConfigurationHTTPReader(registrations, wrong, a.APIs[0], "GET") != nil {
		t.Fatal("request identity granted")
	}
	missing := append([]pluginConfigurationHTTPRegistration(nil), registrations...)
	missing[0].capability = "not.granted"
	if resolvePluginConfigurationHTTPReader(missing, a, a.APIs[0], "GET") != nil {
		t.Fatal("missing capability granted")
	}
}

func TestPluginInstanceReadCommonScopeAcrossCompiledPlugins(t *testing.T) {
	for _, m := range []plugins.Manifest{builtin.CategoryEditorManifest(), builtin.CustomHostsManifest()} {
		t.Run(m.PluginID, func(t *testing.T) {
			a := newTestApp(t)
			p := a.pluginStore
			ctx := context.Background()
			installCloneSource(t, p, m)
			if _, e := p.SaveConfig(ctx, m.PluginID, 0, json.RawMessage(`{"enable":false,"text":"source"}`)); e != nil {
				t.Fatal(e)
			}
			in, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "ReadPair"})
			if e != nil {
				t.Fatal(e)
			}
			before := instanceReadTables(t, a)
			got, e := p.readPluginInstanceView(ctx, in, func(c context.Context, v pluginFormView) (json.RawMessage, error) {
				if v.installation.InstanceID != in.InstanceID || v.installation.DesiredEnabled || v.mode != pluginFormInstalledCold {
					t.Fatal("wrong instance view")
				}
				return v.durable.Value, nil
			})
			if e != nil || len(got) == 0 {
				t.Fatal(string(got), e)
			}
			_, e = p.readPluginInstanceView(ctx, in, func(c context.Context, v pluginFormView) (json.RawMessage, error) {
				// Ignoring a denied write must not turn the request into success.
				_, _ = p.SaveConfig(c, in.InstanceID, v.durable.Revision, json.RawMessage(`{"text":"bad"}`))
				return json.RawMessage(`{}`), nil
			})
			if e == nil {
				t.Fatal("swallowed write admitted")
			}
			if after := instanceReadTables(t, a); !reflect.DeepEqual(before, after) {
				t.Fatal("read changed persistent state")
			}
		})
	}
}
