package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

// Pure wire/normalization plus real App admission component tests only. They do
// not replace a provider, install a worker, make HTTP calls or establish native
// cache behavior. Actual negotiated reentry/native reads have separate gates.
func TestOriginalMediaRecognitionWireNormalization(t *testing.T) {
	for _, sample := range []struct {
		input string
		cache any
	}{
		{`{"args":{"tmdbid":900011}}`, true},
		{`{"args":{"tmdbid":900011,"cache":false}}`, false},
		{`{"args":{"tmdbid":900011,"cache":null}}`, nil},
	} {
		got, err := originalMediaRecognitionArgs(json.RawMessage(sample.input))
		meta, metaErr := json.Marshal(got["meta"])
		if err != nil || metaErr != nil || len(got) != 10 || got["meta"] != nil || string(meta) != "null" || got["mtype"] != nil || got["source"] != "themoviedb" || got["tmdbid"] != json.Number("900011") || got["cache"] != sample.cache {
			t.Fatal("source normalization/default/null was changed", got, err)
		}
		if _, present := got["share_meta"]; present {
			t.Fatal("wrapper-only share_meta leaked into original module kwargs")
		}
	}
	got, err := originalMediaRecognitionArgs(json.RawMessage(`{"args":{"meta":null,"share_meta":null,"source":" TMDB ","mediaid":"900012","tmdbid":900011,"doubanid":"owned-other","mtype":"电视剧","episode_group":"owned-group","cache":0}}`))
	if err != nil || got["source"] != "themoviedb" || got["mediaid"] != "900012" || got["tmdbid"] != json.Number("900012") || got["doubanid"] != nil || got["mtype"] != "电视剧" || got["episode_group"] != "owned-group" || got["cache"] != json.Number("0") {
		t.Fatal("existing source explicit identity precedence or raw cache changed", got, err)
	}
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"args":null}`, `{"args":{},"extra":1}`,
		`{"args":{"tmdbid":900011,"tmdbid":900012}}`,
		`{"args":{"tmdbid":900011,"cache":{"a":1,"a":2}}}`,
		`{"args":{"tmdbid":900011,"endpoint":"https://owned.invalid"}}`,
		`{"args":{"tmdbid":900011}} {}`,
		`{"args":{"tmdbid":900011,"episode_group":"\ud800"}}`,
		`{"args":{"tmdbid":900011,"meta":{}}}`,
		`{"args":{"tmdbid":900011,"share_meta":{}}}`,
		`{"args":{"source":"douban","mediaid":"900011"}}`,
		`{"args":{"tmdbid":null}}`, `{"args":{"tmdbid":0}}`,
		`{"args":{"tmdbid":-1}}`, `{"args":{"tmdbid":2147483648}}`,
		`{"args":{"tmdbid":900011,"mtype":"未知"}}`,
		`{"args":{"tmdbid":900011,"mtype":{}}}`,
	} {
		if got, err := originalMediaRecognitionArgs(json.RawMessage(raw)); got != nil || err == nil {
			t.Fatal("invalid or explicitly pending finite domain admitted", raw, got, err)
		}
	}
	tooBig := `{"args":{"tmdbid":900011,"episode_group":"` + strings.Repeat("x", 128<<10) + `"}}`
	if got, err := originalMediaRecognitionArgs(json.RawMessage(tooBig)); got != nil || err == nil {
		t.Fatal("existing128KiB limit bypassed", err)
	}
	// Decode does not borrow the caller's byte storage or erase numeric lexemes.
	raw := json.RawMessage(`{"args":{"tmdbid":900011,"episode_group":{"owned":9007199254740993}}}`)
	got, err = originalMediaRecognitionArgs(raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw {
		raw[i] = ' '
	}
	group, ok := got["episode_group"].(map[string]any)
	if !ok || group["owned"] != json.Number("9007199254740993") {
		t.Fatal("ordinary JSON ownership/UseNumber was lost", got)
	}
	// Only small ordinary spellings. This is not a huge-exponent/DoS probe.
	for _, n := range []json.Number{"0", "-0", "0.0e-9"} {
		if originalMediaRecognitionTruth(n) {
			t.Fatal("zero numeric cache became true", n)
		}
	}
	if !originalMediaRecognitionTruth(json.Number("1.5e2")) {
		t.Fatal("ordinary nonzero exponent cache became false")
	}
	got, err = originalMediaRecognitionArgs(json.RawMessage(`{"args":{"tmdbid":9000.11e2,"mediaid":0.0,"episode_group":0,"cache":1.5e2}}`))
	if err != nil || got["tmdbid"] != json.Number("900011") || got["mediaid"] != json.Number("0.0") || got["episode_group"] != json.Number("0") || got["cache"] != json.Number("1.5e2") {
		t.Fatal("bounded normalization altered original module-visible raw fields", got, err)
	}
}

func TestOriginalMediaRecognitionAdmissionAndFresh(t *testing.T) {
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	in := PluginInstallation{PluginID: "OwnedRecognitionComponent", InstanceID: "OwnedRecognitionComponent", Version: "1.0"}
	object := &originalPluginInstance{app: a, installation: in}
	closed := errors.New("owned component scope closed")
	calls := 0
	scope := originalBaseCallScope{object: object, installation: in, validate: func(context.Context) error { calls++; return closed }}
	valid := json.RawMessage(`{"args":{"tmdbid":900011}}`)
	ctx := context.Background()
	if out, err := a.originalSharedMediaRecognitionResource(ctx, scope, plugins.MediaRecognizeCapability, valid); out != nil || !errors.Is(err, closed) || calls != 1 {
		t.Fatal("closed authority did not stop before module/provider work", string(out), err, calls)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if out, err := a.originalSharedMediaRecognitionResource(cancelled, scope, plugins.MediaRecognizeCapability, valid); out != nil || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("pre-cancelled request performed authority/provider work", string(out), err, calls)
	}
	if out, err := a.originalSharedMediaRecognitionResource(ctx, scope, "media.recognize.other", valid); out != nil || err == nil || calls != 1 {
		t.Fatal("unsupported capability reached authority/provider", string(out), err, calls)
	}
	if !originalStandardCapabilityAllowed(pluginResourcesSDKContract, plugins.MediaRecognizeCapability) || originalStandardCapabilityAllowed(pluginBaseSDKContract, plugins.MediaRecognizeCapability) {
		t.Fatal("resource-only capability wiring differs")
	}
	// A component object with no negotiated client cannot claim concurrent
	// reentry. No process is built/launched and this is not an actual handshake.
	object.manifest.Methods = []string{"get_module"}
	a.pluginRunningInstances.Store(in.InstanceID, object)
	defer a.pluginRunningInstances.Delete(in.InstanceID)
	scope.validate = func(context.Context) error { return nil }
	out, err := a.originalSharedMediaRecognitionResource(ctx, scope, plugins.MediaRecognizeCapability, valid)
	var api *APIError
	if out != nil || !errors.As(err, &api) || api.Code != "PLUGIN_CONCURRENT_REQUIRED" {
		t.Fatal("unnegotiated self discovery did not stop before methodMu", string(out), err)
	}
	a.pluginRunningInstances.Delete(in.InstanceID)
	object.manifest.Methods = nil

	// Parent guard is a recording authority port, not a provider substitute.
	// It must survive a nested resource context and reject before discovery.
	parentCalls := 0
	parent := context.WithValue(ctx, originalMediaRecognitionGuardKey{}, func(context.Context) error {
		parentCalls++
		return closed
	})
	out, err = a.originalSharedMediaRecognitionResource(parent, scope, plugins.MediaRecognizeCapability, valid)
	if out != nil || !errors.Is(err, closed) || parentCalls != 1 {
		t.Fatal("nested admission dropped its outer guard", string(out), err, parentCalls)
	}

	initial := a.Settings()
	calls = 0
	scope.validate = func(current context.Context) error {
		calls++
		if calls == 2 {
			if !originalMediaRecognitionFresh(current) {
				t.Fatal("cache=null did not activate source fresh before discovery")
			}
			a.cfgMu.Lock()
			a.config.TMDBImageDomain = "https://changed-owned.invalid"
			a.cfgMu.Unlock()
		}
		return nil
	}
	out, err = a.originalSharedMediaRecognitionResource(ctx, scope, plugins.MediaRecognizeCapability, json.RawMessage(`{"args":{"tmdbid":900011,"cache":null}}`))
	api = nil
	if out != nil || !errors.As(err, &api) || api.Code != "PLUGIN_SNAPSHOT_STALE" || calls != 2 {
		t.Fatal("current settings changed without rejection before discovery", string(out), err, calls)
	}
	a.cfgMu.Lock()
	a.config.TMDBImageDomain = initial.TMDBImageDomain
	a.cfgMu.Unlock()

	plain := context.Background()
	if originalMediaRecognitionFresh(plain) {
		t.Fatal("fresh became ambient global state")
	}
	forced := withOriginalMediaRecognitionFresh(plain, true)
	inherited := withOriginalMediaRecognitionFresh(forced, false)
	if !originalMediaRecognitionFresh(forced) || !originalMediaRecognitionFresh(inherited) || originalMediaRecognitionFresh(plain) || originalMediaRecognitionFresh(withOriginalMediaRecognitionFresh(plain, false)) {
		t.Fatal("fresh OR inheritance/restoration contract differs")
	}
	if originalMediaRecognitionGuard(plain) == nil || originalMediaRecognitionGuard(nil) == nil {
		t.Fatal("provider guard accepted an unbound context")
	}
}
