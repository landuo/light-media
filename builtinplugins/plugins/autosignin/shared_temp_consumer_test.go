package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	sdk "light-media/builtinplugins/internal/sdk"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

func tempEnsureResult(c *sdkTjuptCache, ctx context.Context) (err error) {
	defer recoverSource(&err)
	return c.Ensure(ctx)
}
func TestSharedTempConsumerManifest(t *testing.T) {
	m := Manifest()
	if !slices.Contains(m.Capabilities, sdk.SettingsReadCapability) || slices.Contains(m.Capabilities, sdk.BaseGetDataPath) || slices.Contains(m.Capabilities, sdk.SettingsWriteCapability) {
		t.Fatal("temporary cache capability projection differs", m.Capabilities)
	}
}
func TestSharedTempConsumerPathSnapshotConcurrent(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int64
	host := func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		var q sdk.SettingsHostRequestV1
		if e := json.Unmarshal(raw, &q); e != nil {
			return nil, e
		}
		if method != sdk.SettingsReadCapability || q.Key != "TEMP_PATH" {
			t.Errorf("unexpected resource %s/%s", method, q.Key)
		}
		return json.Marshal(sdk.SettingsHostResultV1{SchemaVersion: 1, Key: q.Key, Value: mustTempJSON(dir)})
	}
	c := &sdkTjuptCache{backend: sdkBackend{ctx: context.Background(), host: host}}
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- tempEnsureResult(c, context.Background()) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("snapshot retried or raced", calls.Load())
	}
	raw := []byte("{\n    \"prior\": \"a\",\n    \"hash\": \"1\"\n}")
	if e := c.Write(context.Background(), raw); e != nil {
		t.Fatal(e)
	}
	got, e := c.Read(context.Background())
	if e != nil || string(got) != string(raw) {
		t.Fatal(string(got), e)
	}
	if _, e = os.Stat(filepath.Join(dir, "signin", "tjupt.json")); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("file operations reloaded immutable root", calls.Load())
	}
}
func mustTempJSON(v any) json.RawMessage {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func TestSharedTempConsumerInvalidSettingNoFallback(t *testing.T) {
	for _, value := range []any{nil, false, 0, []any{}, map[string]any{}, "", "owned-relative"} {
		t.Run(string(mustTempJSON(value)), func(t *testing.T) {
			var calls int
			host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				calls++
				return json.Marshal(sdk.SettingsHostResultV1{SchemaVersion: 1, Key: "TEMP_PATH", Value: mustTempJSON(value)})
			}
			c := &sdkTjuptCache{backend: sdkBackend{ctx: context.Background(), host: host}}
			first := tempEnsureResult(c, context.Background())
			second := tempEnsureResult(c, context.Background())
			if first == nil || second == nil || isCacheIO(first) || calls != 1 {
				t.Fatal("setting became ordinary IO/retry/fallback", first, second, calls)
			}
		})
	}
}
func TestSharedTempConsumerRejectedHostNoRetry(t *testing.T) {
	want := errors.New("owned host scope rejected")
	var calls int
	c := &sdkTjuptCache{backend: sdkBackend{ctx: context.Background(), host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, want }}}
	for i := 0; i < 2; i++ {
		e := tempEnsureResult(c, context.Background())
		if !errors.Is(e, want) || isCacheIO(e) {
			t.Fatal("typed failure lost", e)
		}
	}
	if calls != 1 {
		t.Fatal("rejected call retried", calls)
	}
}
func TestSharedTempConsumerCancelledBeforeSetting(t *testing.T) {
	calls := 0
	c := &sdkTjuptCache{backend: sdkBackend{ctx: context.Background(), host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New("must not call")
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := tempEnsureResult(c, ctx); !errors.Is(e, context.Canceled) || calls != 0 {
		t.Fatal(e, calls)
	}
}

type tempForbiddenPage struct{ calls *int }

func (p tempForbiddenPage) Do(context.Context, PageRequest) (*PageResponse, error) {
	*p.calls++
	return nil, errors.New("page must remain uncalled")
}
func TestSharedTempConsumerMissingProviderBeforeTjuptPage(t *testing.T) {
	want := errors.New("owned previous host has no TEMP_PATH key")
	calls := 0
	httpCalls := 0
	host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, want }
	b := sdkBackend{ctx: context.Background(), host: host}
	c := &sdkTjuptCache{backend: b}
	h := SiteVisitHooks{HTTP: tempForbiddenPage{&httpCalls}, Tjupt: &TjuptDependencies{Cache: c, Public: unavailableTjuptPublic{backend: b}}}
	var err error
	func() {
		defer recoverSource(&err)
		_, err = TjuptSignin(context.Background(), Object{"name": "owned", "url": "https://www.tjupt.org/"}, h)
	}()
	if !errors.Is(err, want) || httpCalls != 0 || calls != 1 {
		t.Fatal("provider failure crossed source page boundary", err, calls, httpCalls)
	}
}
func TestSharedTempConsumerGuardRetainsBoundaryPanic(t *testing.T) {
	want := errors.New("owned typed scope failure")
	c := &sdkTjuptCache{backend: sdkBackend{ctx: context.Background(), host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, want }}}
	panicked := false
	func() {
		defer func() {
			if v := recover(); v != nil {
				b, ok := v.(*boundaryFailure)
				if !ok || !errors.Is(b.cause, want) {
					t.Errorf("wrong boundary panic: %T %v", v, v)
				}
				panicked = true
			}
		}()
		_ = c.Ensure(context.Background())
	}()
	if !panicked {
		t.Fatal("typed failure was converted to ordinary adapter error")
	}
}
