package categoryeditor

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"light-media/internal/plugins"
)

// These tests are source-derived unit checks, not a substitute for running the
// original Python plugin or the real application's file/category/message path.
type callbackCall struct {
	method string
	raw    json.RawMessage
}

type callbackRecorder struct {
	calls        []callbackCall
	failMethod   string
	defaultYAML  string
	updateResult json.RawMessage
}

var injectedFailure = errors.New("injected callback failure")

func (r *callbackRecorder) call(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	r.calls = append(r.calls, callbackCall{method, append(json.RawMessage(nil), raw...)})
	if method == r.failMethod {
		return nil, injectedFailure
	}
	switch method {
	case plugins.CategoryDefaultCapability:
		return json.Marshal(r.defaultYAML)
	case plugins.CategoryApplyCapability, plugins.SystemMessagePutCapability:
		return json.RawMessage(`null`), nil
	case plugins.BaseUpdateConfig:
		if len(r.updateResult) > 0 {
			return r.updateResult, nil
		}
		return json.RawMessage(`true`), nil
	default:
		return nil, errors.New("unexpected callback")
	}
}

func sameJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	actual, err := decodeValue(got)
	if err != nil {
		t.Fatalf("invalid result %q: %v", got, err)
	}
	expected, err := decodeValue(json.RawMessage(want))
	if err != nil {
		t.Fatalf("invalid test expectation %q: %v", want, err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

func state(t *testing.T, p *Plugin, want string) {
	t.Helper()
	raw, err := p.Handle(context.Background(), "get_state", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, raw, want)
}

func TestManifestIsStandardSourceArtifact(t *testing.T) {
	m := Manifest()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if m.PluginID != "CategoryEditor" || m.Version != "1.3" || m.Entry != "category-editor" || m.Name != "二级分类策略" || m.Description != "编辑下载和整理时自动二级分类的目录规则。" || m.Icon != "Bookstack_A.png" || m.Author != "jxxghp" || m.AuthorURL != "https://github.com/jxxghp" || m.PluginOrder != 5 {
		t.Fatalf("source identity mismatch: %+v", m)
	}
	if m.AuthLevel != nil || m.HasPage || len(m.APIs) != 0 || len(m.Events) != 0 || len(m.Dashboards) != 0 || m.ConfigDefaults != "" || m.ConfigFormProjection != "" {
		t.Fatalf("invented source features: %+v", m)
	}
	for _, legacy := range []string{"invoke", "config_form", "get_sidebar_nav"} {
		if slices.Contains(m.Methods, legacy) {
			t.Fatalf("unexpected method %s", legacy)
		}
	}
	wantCapabilities := []string{plugins.CategoryDefaultCapability, plugins.CategoryApplyCapability, plugins.SystemMessagePutCapability, plugins.BaseUpdateConfig}
	if !reflect.DeepEqual(m.Capabilities, wantCapabilities) {
		t.Fatalf("capabilities: %v", m.Capabilities)
	}
}

func TestDefaultsAndNoOpGetters(t *testing.T) {
	p := New()
	state(t, p, `false`)
	sameJSON(t, p.content, `""`)
	for _, method := range []string{"get_command", "get_api", "get_page", "get_service", "get_dashboard", "get_dashboard_meta", "get_auth_providers", "get_module", "close", "stop_service", "stop"} {
		t.Run(method, func(t *testing.T) {
			raw, err := p.Handle(context.Background(), method, json.RawMessage(`{}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, raw, `null`)
		})
	}
	raw, err := p.Handle(context.Background(), "get_render_mode", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, raw, `["vuetify",null]`)
	for _, method := range []string{"invoke", "config_form", "run", "get_sidebar_nav", "unknown"} {
		if _, err := p.Handle(context.Background(), method, nil, nil); err == nil {
			t.Fatalf("accepted unsupported method %s", method)
		}
	}
}

func TestFalseyInitDoesNotMutateExistingObject(t *testing.T) {
	for _, input := range []string{`null`, `{}`, `false`, `0`, `-0.0`, `1e-400`, `""`, `[]`} {
		t.Run(input, func(t *testing.T) {
			p := New()
			r := &callbackRecorder{failMethod: plugins.CategoryApplyCapability}
			if _, err := p.Handle(context.Background(), "init", json.RawMessage(`{"enabled":"still enabled","content":"retained"}`), r.call); !errors.Is(err, injectedFailure) {
				t.Fatalf("prepare failure: %v", err)
			}
			r.calls = nil
			raw, err := p.Handle(context.Background(), "init", json.RawMessage(input), r.call)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, raw, `null`)
			state(t, p, `"still enabled"`)
			sameJSON(t, p.content, `"retained"`)
			if len(r.calls) != 0 {
				t.Fatalf("falsey config mutated state/effects: content=%#v, callbacks=%v", p.content, r.calls)
			}
		})
	}
}

func TestNonemptyConfigPreservesRawDisabledState(t *testing.T) {
	for _, input := range []struct{ config, enabled, content string }{
		{`{"other":1}`, `null`, `""`},
		{`{"content":"retained"}`, `null`, `"retained"`},
		{`{"enabled":null,"content":"x"}`, `null`, `"x"`},
		{`{"enabled":false,"content":42}`, `false`, `42`},
		{`{"enabled":0,"content":false}`, `0`, `""`},
		{`{"enabled":-0.0,"content":null}`, `-0.0`, `""`},
		{`{"enabled":"","content":[]}`, `""`, `""`},
		{`{"enabled":[],"content":{}}`, `[]`, `""`},
		{`{"enabled":{},"content":[1]}`, `{}`, `[1]`},
		// init consumes this object itself, not the config in an old envelope.
		{`{"action":"init","config":{"enabled":true,"content":"wrong"}}`, `null`, `""`},
	} {
		t.Run(input.config, func(t *testing.T) {
			p, r := New(), &callbackRecorder{}
			raw, err := p.Handle(context.Background(), "init", json.RawMessage(input.config), r.call)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, raw, `null`)
			state(t, p, input.enabled)
			sameJSON(t, p.content, input.content)
			if len(r.calls) != 0 {
				t.Fatalf("disabled init invoked host: %v", r.calls)
			}
		})
	}
}

func TestEnabledTruthinessAndContentForwarding(t *testing.T) {
	for _, enabled := range []string{`true`, `1`, `-2`, `0.01`, `"false"`, `[false]`, `{"x":false}`, `123456789012345678901234567890`} {
		t.Run(enabled, func(t *testing.T) {
			p, r := New(), &callbackRecorder{failMethod: plugins.CategoryApplyCapability}
			_, err := p.Handle(context.Background(), "init", json.RawMessage(`{"enabled":`+enabled+`,"content":"x"}`), r.call)
			if !errors.Is(err, injectedFailure) {
				t.Fatalf("expected apply failure, got %v", err)
			}
			state(t, p, enabled)
			if len(r.calls) != 1 || r.calls[0].method != plugins.CategoryApplyCapability {
				t.Fatalf("unexpected calls: %v", r.calls)
			}
		})
	}
	for _, input := range []struct{ content, sent string }{
		{`null`, `""`}, {`false`, `""`}, {`0`, `""`}, {`-0.0`, `""`}, {`1e-400`, `""`},
		{`""`, `""`}, {`[]`, `""`}, {`{}`, `""`},
		{`"中文\r\n# keep\n"`, `"中文\r\n# keep\n"`},
		{`true`, `true`}, {`123456789012345678901234567890`, `123456789012345678901234567890`},
		{`[0]`, `[0]`}, {`{"a":1}`, `{"a":1}`},
	} {
		t.Run("content="+input.content, func(t *testing.T) {
			p, r := New(), &callbackRecorder{failMethod: plugins.CategoryApplyCapability}
			_, err := p.Handle(context.Background(), "init", json.RawMessage(`{"enabled":true,"content":`+input.content+`}`), r.call)
			if !errors.Is(err, injectedFailure) {
				t.Fatalf("expected apply failure, got %v", err)
			}
			if len(r.calls) != 1 {
				t.Fatalf("callbacks: %v", r.calls)
			}
			sameJSON(t, r.calls[0].raw, `{"content":`+input.sent+`}`)
		})
	}
}

func TestInitializationEffectOrderAndFailureBoundaries(t *testing.T) {
	wantOrder := []string{plugins.CategoryApplyCapability, plugins.SystemMessagePutCapability, plugins.BaseUpdateConfig}
	for _, scenario := range []struct {
		name, failAt, updateResult, enabled string
		calls                               int
	}{
		{"success", "", `true`, `false`, 3},
		{"update false is not exception", "", `false`, `false`, 3},
		{"update None is not exception", "", `null`, `false`, 3},
		{"apply failure keeps enabled", plugins.CategoryApplyCapability, `true`, `"yes"`, 1},
		{"message failure keeps enabled", plugins.SystemMessagePutCapability, `true`, `"yes"`, 2},
		{"update exception already disabled", plugins.BaseUpdateConfig, `true`, `false`, 3},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			p := New()
			r := &callbackRecorder{failMethod: scenario.failAt, updateResult: json.RawMessage(scenario.updateResult)}
			raw, err := p.Handle(context.Background(), "init", json.RawMessage(`{"enabled":"yes","content":"电影:\n  华语电影: {}\n","drop":"not retained"}`), r.call)
			if scenario.failAt == "" {
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, raw, `null`)
			} else if !errors.Is(err, injectedFailure) {
				t.Fatalf("expected callback failure, got %v", err)
			}
			state(t, p, scenario.enabled)
			if len(r.calls) != scenario.calls {
				t.Fatalf("callback count: %v", r.calls)
			}
			for i, call := range r.calls {
				if call.method != wantOrder[i] {
					t.Fatalf("callback order: %v", r.calls)
				}
			}
			sameJSON(t, r.calls[0].raw, `{"content":"电影:\n  华语电影: {}\n"}`)
			if len(r.calls) >= 2 {
				sameJSON(t, r.calls[1].raw, `{"message":"二级分类策略已更新，请注意同步调整目录设置，插件将恢复关闭状态！","title":"二级分类策略"}`)
			}
			if len(r.calls) >= 3 {
				// No plugin_id override: Base resolves the actual calling clone.
				sameJSON(t, r.calls[2].raw, `{"config":{"enabled":false,"content":"电影:\n  华语电影: {}\n"}}`)
			}
		})
	}
}

func TestReloadIsExplicitAndObjectsAreIndependent(t *testing.T) {
	p, other, r := New(), New(), &callbackRecorder{}
	input := json.RawMessage(`{"enabled":true,"content":"same"}`)
	for i := 0; i < 2; i++ {
		if _, err := p.Handle(context.Background(), "init", input, r.call); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.calls) != 6 {
		t.Fatalf("explicit equal init should repeat effects: %v", r.calls)
	}
	for _, method := range []string{"get_state", "stop_service", "close", "stop"} {
		if _, err := p.Handle(context.Background(), method, nil, r.call); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.calls) != 6 {
		t.Fatalf("implicit replay or object leak: %v / %#v", r.calls, other.content)
	}
	sameJSON(t, other.content, `""`)
	state(t, other, `false`)
	state(t, p, `false`)
}

func TestGetFormAlwaysReadsOriginalDefaults(t *testing.T) {
	p, r := New(), &callbackRecorder{defaultYAML: "default one\n"}
	if _, err := p.Handle(context.Background(), "init", json.RawMessage(`{"enabled":false,"content":"user content"}`), nil); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"default one\n", "default two\n"} {
		r.defaultYAML = content
		raw, err := p.Handle(context.Background(), "get_form", nil, r.call)
		if err != nil {
			t.Fatal(err)
		}
		var result []json.RawMessage
		if err := json.Unmarshal(raw, &result); err != nil || len(result) != 2 {
			t.Fatalf("expected form tuple, got %s, %v", raw, err)
		}
		// Independently transcribed source structure, not the embedded variable.
		sameJSON(t, result[0], `[{"component":"VForm","content":[{"component":"VRow","content":[{"component":"VCol","props":{"cols":12,"md":6},"content":[{"component":"VSwitch","props":{"model":"enabled","label":"启用插件（写入配置文件）"}}]}]},{"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VAceEditor","props":{"modelvalue":"content","lang":"yaml","theme":"monokai","style":"height: 30rem"}}]}]}]}]`)
		want, _ := json.Marshal(map[string]any{"enabled": false, "content": content})
		sameJSON(t, result[1], string(want))
	}
	if len(r.calls) != 2 {
		t.Fatalf("default read count: %v", r.calls)
	}
	for _, call := range r.calls {
		if call.method != plugins.CategoryDefaultCapability {
			t.Fatalf("unexpected form callback: %+v", call)
		}
		sameJSON(t, call.raw, `{}`)
	}
	sameJSON(t, p.content, `"user content"`)
}

func TestGetFormPropagatesDefaultReadFailure(t *testing.T) {
	p, r := New(), &callbackRecorder{failMethod: plugins.CategoryDefaultCapability}
	if _, err := p.Handle(context.Background(), "get_form", nil, r.call); !errors.Is(err, injectedFailure) {
		t.Fatalf("default failure: %v", err)
	}
	for _, value := range []string{`null`, `{}`, `1`, `false`, `not JSON`} {
		host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(value), nil
		}
		if _, err := p.Handle(context.Background(), "get_form", nil, host); err == nil {
			t.Fatalf("accepted non-text default %s", value)
		}
	}
}

func TestInvalidConfigDoesNotMutateMembers(t *testing.T) {
	for _, raw := range []string{`true`, `1`, `"text"`, `[1]`, `{`, `{} {}`} {
		t.Run(raw, func(t *testing.T) {
			p := New()
			if _, err := p.Handle(context.Background(), "init", json.RawMessage(raw), nil); err == nil {
				t.Fatalf("accepted invalid config %s", raw)
			}
			state(t, p, `false`)
			sameJSON(t, p.content, `""`)
		})
	}
	// A missing callback fails only once original execution needs it, after
	// member assignment; disabled and empty config do not require a host.
	p := New()
	if _, err := p.Handle(context.Background(), "init", json.RawMessage(`{"enabled":true,"content":"x"}`), nil); err == nil {
		t.Fatal("enabled init unexpectedly succeeded without host")
	}
	state(t, p, `true`)
	sameJSON(t, p.content, `"x"`)
}

func TestRawUnicodeIsPreservedForSourceEncodingFailure(t *testing.T) {
	p, r := New(), &callbackRecorder{failMethod: plugins.CategoryApplyCapability}
	_, err := p.Handle(context.Background(), "init", json.RawMessage(`{"enabled":"\ud800","content":"before\ud800after"}`), r.call)
	if !errors.Is(err, injectedFailure) {
		t.Fatalf("expected host encoding failure, got %v", err)
	}
	if len(r.calls) != 1 || string(r.calls[0].raw) != `{"content":"before\ud800after"}` {
		t.Fatalf("content's unpaired surrogate was replaced: %v", r.calls)
	}
	raw, err := p.Handle(context.Background(), "get_state", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"\ud800"` {
		t.Fatalf("state's unpaired surrogate was replaced: %s", raw)
	}
}
