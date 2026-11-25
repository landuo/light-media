package iyuuautoseed

import (
	"context"
	"encoding/json"
	"errors"
	sdk "light-media/builtinplugins/internal/sdk"
	"reflect"
	"slices"
	"testing"
)

func TestIYUUAPIUsesTrustedHostEnvironmentNotWorkerOrSiteProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://worker.invalid:1")
	for _, environment := range [][]string{{}, {"https_proxy=http://host.invalid:2", "no_proxy=.owned.invalid"}} {
		caller := orderedIYUUHost(t, iyuuSDKStep{method: sdk.NetworkProxyReadCapability, reply: string(mustJSONProxy(t, sdk.NetworkProxySettingsV1{SchemaVersion: 1, Environment: environment, Proxies: map[string]string{"https": "http://explicit-site.invalid:3"}}))})
		tr, err := newHostAPITransport(t.Context(), caller, "synthetic-token", func(context.Context) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		defer tr.Close()
		if !reflect.DeepEqual(tr.environment, environment) {
			t.Fatalf("host environment not retained: %v", tr.environment)
		}
	}
	if !slices.Contains(Manifest().Capabilities, sdk.NetworkProxyReadCapability) {
		t.Fatal("manifest omits host proxy capability")
	}
}

func TestIYUUAPIHostProxyFailureDoesNotSilentlyFallBackToDirect(t *testing.T) {
	denied := errors.New("owned snapshot denied")
	caller := func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, denied }
	tr, err := newHostAPITransport(t.Context(), caller, "synthetic-token", func(context.Context) error { return nil })
	if tr != nil || err == nil {
		t.Fatal("failed host snapshot fell back to worker environment")
	}
}

func mustJSONProxy(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
