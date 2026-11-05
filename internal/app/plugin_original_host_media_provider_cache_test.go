package app

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"sync"
	"testing"
)

func TestOriginalHostMediaProviderCachedPinnedDecorator(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Case       string `json:"case"`
			SkipEmpty  bool   `json:"skip_empty"`
			Operations []struct {
				Key    string          `json:"key"`
				Result json.RawMessage `json:"result"`
				Fresh  bool            `json:"fresh"`
				Mono   *float64        `json:"mono"`
			} `json:"operations"`
			Observations []struct {
				Value json.RawMessage `json:"value"`
				Loads int             `json:"loads"`
			} `json:"observations"`
		} `json:"generic_cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/recognition-provider-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 6 {
		t.Fatal("source cached decorator oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Case, func(t *testing.T) {
			mono := 10.0
			a, err := New(Options{DataDir: t.TempDir(), HostMediaRecognition: &HostMediaRecognitionConfiguration{Locale: "zh", Source: "themoviedb", MetaCacheExpire: big.NewInt(1), MonotonicClock: func() float64 { return mono }}})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			loads := 0
			for index, operation := range sample.Operations {
				if operation.Mono != nil {
					mono = *operation.Mono
				}
				ctx := context.Background()
				if operation.Fresh {
					ctx = context.WithValue(ctx, originalHostMediaFreshContextKey{}, true)
				}
				key := operation.Key
				if key == "" {
					key = "owned"
				}
				value, err := a.originalHostMediaProviderCached(ctx, "douban", "get", key, sample.SkipEmpty, func() (any, error) { loads++; return decodeSourceJSON(operation.Result) })
				if err != nil {
					t.Fatal(err)
				}
				actual, _ := json.Marshal(value)
				if !originalBaseJSONEqual(actual, sample.Observations[index].Value) || loads != sample.Observations[index].Loads {
					t.Fatal("pinned decorator cache/None/empty/fresh/key/TTL", index, string(sample.Observations[index].Value), string(actual), sample.Observations[index].Loads, loads)
				}
			}
		})
	}
}

func TestOriginalHostMediaProviderCacheConcurrentConsumersAreIsolated(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	input := map[string]any{"id": json.Number("123"), "actors": []any{map[string]any{"name": "Owned"}}}
	_, err := a.originalHostMediaProviderCached(ctx, "bangumi", "get", "owned", false, func() (any, error) { return input, nil })
	if err != nil {
		t.Fatal(err)
	}
	input["id"] = json.Number("999")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := a.originalHostMediaProviderCached(ctx, "bangumi", "get", "owned", false, func() (any, error) { t.Error("unexpected provider cache miss"); return nil, nil })
			if err != nil {
				t.Error(err)
				return
			}
			fields := sourceMap(value)
			if fields["id"] != json.Number("123") {
				t.Error("load retained caller's mutable map", fields["id"])
			}
			fields["id"] = json.Number("456")
			sourceMap(sourceRows(fields["actors"])[0])["name"] = "changed"
		}()
	}
	wg.Wait()
	value, err := a.originalHostMediaProviderCached(ctx, "bangumi", "get", "owned", false, func() (any, error) { t.Error("unexpected provider cache miss"); return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	fields := sourceMap(value)
	if fields["id"] != json.Number("123") || sourceMap(sourceRows(fields["actors"])[0])["name"] != "Owned" {
		t.Fatal("cached model leaked one Go consumer's mutations", value)
	}
}

func TestOriginalHostMediaProviderTypedNilPreservesSourceNoneVersusEmpty(t *testing.T) {
	for _, sample := range []struct {
		name  string
		value any
		loads int
	}{
		{"nil_map", map[string]any(nil), 2}, {"nil_slice", []any(nil), 2}, {"raw_null", json.RawMessage(`null`), 2}, {"empty_map", map[string]any{}, 1}, {"empty_slice", []any{}, 1}, {"raw_empty_map", json.RawMessage(`{}`), 1}, {"raw_empty_slice", json.RawMessage(`[]`), 1},
	} {
		t.Run(sample.name, func(t *testing.T) {
			a := newTestApp(t)
			loads := 0
			for i := 0; i < 2; i++ {
				_, err := a.originalHostMediaProviderCached(context.Background(), "bangumi", "get", "owned", false, func() (any, error) { loads++; return sample.value, nil })
				if err != nil {
					t.Fatal(err)
				}
			}
			if loads != sample.loads {
				t.Fatal("source skip_none=true skip_empty=false", sample.loads, loads)
			}
		})
	}
}
