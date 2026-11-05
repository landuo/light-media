package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalHostProxyFixedSourceGetter(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-platform-final-20261104/settings/proxy-settings-independent-source-oracle.json")
	var oracle struct {
		Commit string `json:"source_commit"`
		Cases  []struct {
			Name        string            `json:"name"`
			Setting     *string           `json:"proxy_host"`
			Environment map[string]string `json:"environment"`
			Proxy       map[string]string `json:"proxy"`
		} `json:"cases"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 17 {
		t.Fatal("fixed independently executed config.py PROXY oracle missing", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			setting := ""
			if sample.Setting != nil {
				setting = *sample.Setting
			}
			environment := []string{}
			for name, value := range sample.Environment {
				environment = append(environment, name+"="+value)
			}
			sort.Strings(environment)
			if got := originalProxySettingsValue(setting, originalProxyEnvironment(environment)); !reflect.DeepEqual(got, sample.Proxy) {
				t.Fatal("fixed source nullable proxy mapping differs")
			}
		})
	}
}

func TestOriginalHostProxyPrivateEnvironmentBoundary(t *testing.T) {
	// All proxy spellings and host-specific keys are inputs to urllib's two
	// passes; REQUEST_METHOD presence suppresses upper HTTP_PROXY in CGI.
	want := []string{"HTTP_PROXY=", "http_proxy=http://owned.invalid", "http://owned.invalid_proxy=http://specific.invalid", "Odd_PrOxY=value", "REQUEST_METHOD=", "NO_PROXY=*"}
	input := append(append([]string{}, want...), "UNRELATED_TOKEN=must-not-cross", "REQUEST_METHOD_OTHER=secret", "not_an_assignment")
	if got := originalProxyEnvironment(input); !reflect.DeepEqual(got, want) {
		t.Fatal("proxy environment boundary changed")
	}
	value := plugins.NetworkProxySettingsV1{SchemaVersion: 1, Proxies: map[string]string{"http": "http://owned:private-test@proxy.invalid"}, Environment: want}
	for _, text := range []string{fmt.Sprint(value), fmt.Sprintf("%#v", value)} {
		if strings.Contains(text, "private-test") || strings.Contains(text, "proxy.invalid") {
			t.Fatal("ordinary formatting leaked private proxy")
		}
	}
}

func TestOriginalHostProxyCurrentScopeAndChanges(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	scope := originalBaseCallScope{object: &originalPluginInstance{app: a}, validate: func(context.Context) error { return nil }}
	if !originalStandardCapabilityAllowed(pluginResourcesSDKContract, plugins.NetworkProxyReadCapability) || originalStandardCapabilityAllowed(pluginBaseSDKContract, plugins.NetworkProxyReadCapability) {
		t.Fatal("proxy contract grant changed")
	}
	if _, err := a.originalHostProxyResource(ctx, scope, json.RawMessage(`{"unexpected":true}`)); err == nil {
		t.Fatal("unknown input accepted")
	}
	if _, err := a.originalHostProxyResource(ctx, originalBaseCallScope{validate: scope.validate}, json.RawMessage(`{}`)); err == nil {
		t.Fatal("private snapshot without real object")
	}
	denied := errors.New("owned revoked")
	scope.validate = func(context.Context) error { return denied }
	if _, err := a.originalHostProxyResource(ctx, scope, json.RawMessage(`{}`)); !errors.Is(err, denied) {
		t.Fatal("revoked scope returned private proxy")
	}
	calls := 0
	scope.validate = func(context.Context) error {
		calls++
		if calls == 2 {
			c := a.Settings()
			c.ProxyHost = "http://owned.invalid:3128"
			a.cfgMu.Lock()
			a.config = c
			a.cfgMu.Unlock()
		}
		return nil
	}
	if _, err := a.originalHostProxyResource(ctx, scope, json.RawMessage(`{}`)); err == nil {
		t.Fatal("changed configuration returned old private proxy")
	}
}
