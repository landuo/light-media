package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"testing"
)

func TestOriginalHostMediaCachePinnedSourceClocksPersistenceAndLRU(t *testing.T) {
	var oracle struct {
		SourceCommit      string `json:"source_commit"`
		CachetoolsVersion string `json:"cachetools_version"`
		Cases             []struct {
			Case       string         `json:"case"`
			Seed       map[string]any `json:"seed"`
			SeedOrder  []string       `json:"seed_order"`
			TTL        *int64         `json:"ttl"`
			Operations []struct {
				Action       string         `json:"action"`
				Meta         map[string]any `json:"meta"`
				Info         map[string]any `json:"info"`
				Wall         float64        `json:"wall"`
				Mono         float64        `json:"mono"`
				Start, Count int
			} `json:"operations"`
			Observations []struct {
				Value json.RawMessage `json:"value"`
			} `json:"observations"`
			Persisted      json.RawMessage `json:"persisted"`
			PersistedOrder []string        `json:"persisted_order"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/recognition-cache-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 10 || oracle.CachetoolsVersion != "7.1.4" || oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" {
		t.Fatal("pinned source cache oracle", err)
	}
	ctx := context.Background()
	for _, sample := range oracle.Cases {
		t.Run(sample.Case, func(t *testing.T) {
			wall, mono := 1000.0, 10.0
			ttl := int64(3600)
			if sample.TTL != nil {
				ttl = *sample.TTL
			}
			config := &HostMediaRecognitionConfiguration{Locale: "zh", MetaCacheExpire: big.NewInt(ttl / 3600), WallClock: func() float64 { return wall }, MonotonicClock: func() float64 { return mono }}
			dir := t.TempDir()
			if sample.Seed != nil {
				seed := map[string]any{}
				for key, value := range sample.Seed {
					seed[key] = value
				}
				if seed["version"] != nil {
					seed["order"] = sample.SeedOrder
				}
				store, err := OpenStore(dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Put(ctx, "plugin_host_tmdb_cache", "main", seed); err != nil {
					t.Fatal(err)
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			a, err := New(Options{DataDir: dir, HostMediaRecognition: config})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = a.Close() }()
			if a.originalHostMediaCache.items == nil {
				t.Fatal("cache was not restored at App construction")
			}
			// Source TmdbCache restores at module construction. This independent
			// component test opens that same cache before exercising any operations.
			if _, err := a.originalHostMediaCacheGet(ctx, "owned-missing-probe"); err != nil {
				t.Fatal(err)
			}
			observation := 0
			for _, operation := range sample.Operations {
				switch operation.Action {
				case "clock":
					wall, mono = operation.Wall, operation.Mono
				case "update":
					if err := a.originalHostMediaCachePut(ctx, originalHostMediaCacheKey(originalHostMediaMeta(operation.Meta), "zh"), operation.Info); err != nil {
						t.Fatal(err)
					}
				case "get":
					value, err := a.originalHostMediaCacheGet(ctx, originalHostMediaCacheKey(originalHostMediaMeta(operation.Meta), "zh"))
					if err != nil {
						t.Fatal(err)
					}
					if value == nil {
						value = map[string]any{}
					}
					actual, _ := json.Marshal(value)
					if !originalBaseJSONEqual(actual, sample.Observations[observation].Value) {
						t.Fatal("fixed source cache get", observation, string(sample.Observations[observation].Value), string(actual))
					}
					observation++
				case "bulk":
					for i := operation.Start; i < operation.Start+operation.Count; i++ {
						name := "Owned" + fmt.Sprint(i)
						meta := map[string]any{"name": name, "type": "未知"}
						info := map[string]any{"id": json.Number(fmt.Sprint(i + 1)), "title": "Owned", "name": "Owned", "media_type": "电影", "release_date": "2024-01-01", "first_air_date": "2024-01-01", "poster_path": nil, "backdrop_path": nil}
						if err := a.originalHostMediaCachePut(ctx, originalHostMediaCacheKey(originalHostMediaMeta(meta), "zh"), info); err != nil {
							t.Fatal(err)
						}
					}
				case "save":
					if err := a.saveOriginalHostMediaCache(ctx); err != nil {
						t.Fatal(err)
					}
				case "restart":
					if err := a.Close(); err != nil {
						t.Fatal(err)
					}
					a, err = New(Options{DataDir: dir, HostMediaRecognition: config})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := a.originalHostMediaCacheGet(ctx, "owned-missing-probe"); err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatal("unknown source operation", operation.Action)
				}
			}
			if err := a.saveOriginalHostMediaCache(ctx); err != nil {
				t.Fatal(err)
			}
			var persisted map[string]json.RawMessage
			err = a.Store.Get(ctx, "plugin_host_tmdb_cache", "main", &persisted)
			if string(sample.Persisted) == "null" {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("source removes empty positive persistence", err, persisted)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var order []string
			if json.Unmarshal(persisted["order"], &order) != nil || !reflect.DeepEqual(order, sample.PersistedOrder) {
				t.Fatal("source expiration heap save order", sample.PersistedOrder, order)
			}
			delete(persisted, "order")
			actual, _ := json.Marshal(persisted)
			if !originalBaseJSONEqual(actual, sample.Persisted) {
				t.Fatal("fixed source persisted identities and absolute wall expiration", string(sample.Persisted), string(actual))
			}
		})
	}
}

func TestHostMediaRecognitionEnvironmentSourceDefaultsAndOverrides(t *testing.T) {
	for _, sample := range []struct {
		env    map[string]string
		locale string
		hours  string
		bad    bool
	}{
		{map[string]string{}, "zh", "0", false},
		{map[string]string{"TMDB_LOCALE": "en-US", "META_CACHE_EXPIRE": "48"}, "en-US", "48", false},
		{map[string]string{"TMDB_LOCALE": "", "META_CACHE_EXPIRE": "-1"}, "", "-1", false},
		{map[string]string{"META_CACHE_EXPIRE": "invalid"}, "", "", true},
	} {
		value, err := HostMediaRecognitionEnvironment(func(key string) (string, bool) { value, ok := sample.env[key]; return value, ok })
		if sample.bad {
			if err == nil {
				t.Fatal("invalid source integer accepted")
			}
			continue
		}
		if err != nil || value.Locale != sample.locale || value.MetaCacheExpire.String() != sample.hours {
			t.Fatal(sample, value, err)
		}
	}
	for _, large := range []bool{false, true} {
		a, err := New(Options{DataDir: t.TempDir(), BigMemoryMode: large, HostMediaRecognition: &HostMediaRecognitionConfiguration{Locale: "zh"}})
		if err != nil {
			t.Fatal(err)
		}
		ttl, err := a.originalHostMediaCacheTTL()
		want := float64(24 * 3600)
		limit := 256
		if large {
			want = 72 * 3600
			limit = 1024
		}
		if err != nil || ttl != want || a.originalHostMediaCacheLimit() != limit {
			t.Fatal("source CONF memory mode", ttl, limit, err)
		}
		_ = a.Close()
	}
}
