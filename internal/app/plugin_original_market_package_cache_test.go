package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/plugins/artifacts"
)

type ownedMarketPackageBand struct {
	Status  int     `json:"status"`
	Version *string `json:"version"`
	Body    *string `json:"body"`
}
type ownedMarketPackageRow struct {
	ID, Version, Repo string
}
type ownedMarketPackageObservation struct {
	Rows     []ownedMarketPackageRow `json:"rows"`
	Requests []json.RawMessage       `json:"requests"`
}
type ownedMarketPackageRoundTrip func(*http.Request) (*http.Response, error)

func (f ownedMarketPackageRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// This is an owned protocol fixture, not the production HTTP/worker witness.
// The latter is replayed separately with the same complete-source inputs.
func ownedMarketPackagePeer(t *testing.T) (*App, *http.Client, func(map[string]ownedMarketPackageBand), func() []string) {
	t.Helper()
	a := newTestApp(t)
	var mu sync.Mutex
	var requests []string
	bands := map[string]ownedMarketPackageBand{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		band := "base"
		if strings.HasSuffix(r.URL.Path, "/package.v2.json") {
			band = "higher"
		} else if !strings.HasSuffix(r.URL.Path, "/package.json") {
			t.Errorf("unexpected owned package route %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		selected := bands[band]
		requests = append(requests, band)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(selected.Status)
		if selected.Version != nil {
			id := "OwnedBase"
			if band == "higher" {
				id = "OwnedHigher"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{id: map[string]any{"name": id, "version": *selected.Version, "v2": true}})
		} else if selected.Body != nil {
			_, _ = w.Write([]byte(*selected.Body))
		} else {
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(peer.Close)
	local, _ := url.Parse(peer.URL)
	base := peer.Client().Transport
	client := &http.Client{Transport: ownedMarketPackageRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "raw.githubusercontent.com" || r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/Owned/") {
			return nil, errors.New("nonowned package destination denied")
		}
		copy := r.Clone(r.Context())
		target := *r.URL
		target.Scheme, target.Host = local.Scheme, local.Host
		copy.URL, copy.Host = &target, target.Host
		return base.RoundTrip(copy)
	})}
	set := func(next map[string]ownedMarketPackageBand) { mu.Lock(); bands = next; mu.Unlock() }
	read := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, requests...) }
	return a, client, set, read
}

func ownedMarketPackageProject(values []artifacts.Discovery) []ownedMarketPackageRow {
	rows := []ownedMarketPackageRow{}
	for _, value := range values {
		rows = append(rows, ownedMarketPackageRow{value.PluginID, value.CurrentVersion, value.RepositoryURL})
	}
	return rows
}

func ownedMarketPackageOracle(t *testing.T) (map[string][]ownedMarketPackageObservation, map[string]ownedMarketPackageObservation) {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/market-selection-collision/half-package/cache-edge-oracle.json")
	var value struct {
		Sequences []struct {
			Name   string
			Phases []ownedMarketPackageObservation
		}
		TTL map[string]json.RawMessage
	}
	if err != nil || json.Unmarshal(raw, &value) != nil {
		t.Fatal("independently executed source package-cache oracle", err)
	}
	sequences := map[string][]ownedMarketPackageObservation{}
	for _, sequence := range value.Sequences {
		sequences[sequence.Name] = sequence.Phases
	}
	ttl := map[string]ownedMarketPackageObservation{}
	for name, raw := range value.TTL {
		var row ownedMarketPackageObservation
		if json.Unmarshal(raw, &row) == nil && row.Rows != nil {
			ttl[name] = row
		}
	}
	return sequences, ttl
}

func TestOriginalMarketPackageCacheFullSourceErrorEmptyAndRecovery(t *testing.T) {
	oracle, _ := ownedMarketPackageOracle(t)
	raw, err := os.ReadFile("../../testdata/market-selection-collision/half-package/cache-edge-inputs.json")
	var input struct {
		Sequences []struct {
			Name, Repository string
			Phases           []struct {
				Name  string
				Force bool
				Bands map[string]ownedMarketPackageBand
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &input) != nil {
		t.Fatal(err)
	}
	for _, sequence := range input.Sequences {
		t.Run(sequence.Name, func(t *testing.T) {
			a, client, set, requests := ownedMarketPackagePeer(t)
			want := oracle[sequence.Name]
			if len(want) != len(sequence.Phases) {
				t.Fatal("missing source phases")
			}
			for i, phase := range sequence.Phases {
				set(phase.Bands)
				before := len(requests())
				values, cached, _, err := a.originalMarketSourcePackages(context.Background(), sequence.Repository, sequence.Repository, phase.Force, client)
				if got := ownedMarketPackageProject(values); !reflect.DeepEqual(got, want[i].Rows) {
					t.Fatalf("%s: got %#v, source %#v, err=%v", phase.Name, got, want[i].Rows, err)
				}
				if len(want[i].Requests) == 0 && (len(requests()) != before || !cached || err != nil) {
					t.Fatalf("%s: source made no request; target cache=%v err=%v requests=%v", phase.Name, cached, err, requests()[before:])
				}
				if phase.Force && (strings.Contains(sequence.Name, "500") || strings.Contains(sequence.Name, "malformed") || strings.Contains(sequence.Name, "failure")) && phase.Name != "initial" && err == nil {
					t.Fatal("partial source failure was falsely reported as complete success")
				}
			}
		})
	}
}

func TestOriginalMarketPackageCacheIndependentTTLFullSourceClock(t *testing.T) {
	_, oracle := ownedMarketPackageOracle(t)
	a, client, set, requests := ownedMarketPackagePeer(t)
	repository := "https://github.com/Owned/TTLClock"
	version := func(value string) ownedMarketPackageBand { return ownedMarketPackageBand{Status: 200, Version: &value} }
	read := func(name string, force bool) {
		t.Helper()
		before := len(requests())
		values, _, _, err := a.originalMarketSourcePackages(context.Background(), repository, repository, force, client)
		want, exists := oracle[name]
		if !exists || !reflect.DeepEqual(ownedMarketPackageProject(values), want.Rows) {
			t.Fatalf("%s source TTL mismatch: %v %v", name, values, err)
		}
		if len(want.Requests) == 0 && len(requests()) != before {
			t.Fatal("cache hit refetched source package", name)
		}
	}
	set(map[string]ownedMarketPackageBand{"higher": version("1.0"), "base": version("1.0")})
	read("initial", true)
	set(map[string]ownedMarketPackageBand{"higher": {Status: 500}, "base": version("2.0")})
	read("base_refreshed_at_1000", true)
	set(map[string]ownedMarketPackageBand{"higher": version("3.0"), "base": version("3.0")})
	for _, phase := range []struct {
		name               string
		higherAge, baseAge time.Duration
	}{{"ordinary_at_1799", 1799 * time.Second, 799 * time.Second}, {"ordinary_at_1801", 1801 * time.Second, 801 * time.Second}, {"ordinary_at_2801", 1000 * time.Second, 1801 * time.Second}} {
		a.pluginOriginalMarkets.mu.Lock()
		for band, age := range map[string]time.Duration{"v2": phase.higherAge, "": phase.baseAge} {
			key := originalMarketPackageKey{repository, band}
			entry := a.pluginOriginalMarkets.packages[key]
			entry.status.CheckedAt = time.Now().Add(-age)
			a.pluginOriginalMarkets.packages[key] = entry
		}
		a.pluginOriginalMarkets.mu.Unlock()
		read(phase.name, false)
	}
}

func TestOriginalMarketPackageCacheLRU128AndCanceledFailure(t *testing.T) {
	a, client, set, requests := ownedMarketPackagePeer(t)
	version := "1.0"
	set(map[string]ownedMarketPackageBand{"base": {Status: 200, Version: &version}})
	read := func(number int, wantCached bool) {
		t.Helper()
		repository := fmt.Sprintf("https://github.com/Owned/LRU%d", number)
		_, cached, err := a.originalMarketSourcePackage(context.Background(), repository, repository, "", false, client)
		if err != nil || cached != wantCached {
			t.Fatalf("LRU%d cached=%v err=%v", number, cached, err)
		}
	}
	for i := 0; i < 128; i++ {
		read(i, false)
	}
	read(0, true)
	read(128, false)
	read(0, true)
	read(1, false)
	if len(a.pluginOriginalMarkets.packages) != 128 || len(requests()) != 130 {
		t.Fatal("source 128-package LRU boundary", len(a.pluginOriginalMarkets.packages), len(requests()))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := a.originalMarketSourcePackage(ctx, "https://github.com/Owned/LRU0", "https://github.com/Owned/LRU0", "", true, client); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled force did not preserve failure", err)
	}
	read(0, true)
}

func TestOriginalMarketSelectionSeededSource400(t *testing.T) {
	path := os.Getenv("OWNED_MARKET_SEEDED_SOURCE")
	if path == "" {
		t.Skip("set OWNED_MARKET_SEEDED_SOURCE to a freshly executed source_seeded.py result")
	}
	var oracle struct {
		Seed  int
		Cases []struct {
			Name                   string
			Markets                []string
			Higher, Base, Selected []ownedMarketPackageRow
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Seed != 2683 || len(oracle.Cases) != 400 {
		t.Fatal("fixed full-source seeded oracle is required", err)
	}
	convert := func(rows []ownedMarketPackageRow) []artifacts.Discovery {
		out := []artifacts.Discovery{}
		for _, row := range rows {
			out = append(out, artifacts.Discovery{PluginID: row.ID, CurrentVersion: row.Version, RepositoryURL: row.Repo})
		}
		return out
	}
	for _, example := range oracle.Cases {
		t.Run(example.Name, func(t *testing.T) {
			got := selectOriginalMarketDeclarations(convert(example.Higher), convert(example.Base), example.Markets)
			if !reflect.DeepEqual(got, convert(example.Selected)) {
				t.Fatalf("source seeded selection mismatch: %#v != %#v", got, example.Selected)
			}
		})
	}
}

// The complete source helper's TLRUCache expires dead entries before evicting
// a live LRU entry. The fixture clock observations are bound in this oracle.
func TestOriginalMarketPackageCacheExpiryPrecedesLRUEviction(t *testing.T) {
	var source struct {
		ExpiryLRU map[string]struct {
			Requests []json.RawMessage `json:"requests"`
		} `json:"expiry_lru"`
	}
	raw, err := os.ReadFile("../../testdata/market-selection-collision/half-package/expiry-lru-source.json")
	if err != nil || json.Unmarshal(raw, &source) != nil || len(source.ExpiryLRU) != 4 {
		t.Fatal("complete source expiry/LRU oracle required", err)
	}
	a, client, set, requests := ownedMarketPackagePeer(t)
	version := "1.0"
	set(map[string]ownedMarketPackageBand{"base": {Status: 200, Version: &version}})
	read := func(number int, phase string) {
		t.Helper()
		before := len(requests())
		repository := fmt.Sprintf("https://github.com/Owned/ExpiryLRU%d", number)
		_, _, err := a.originalMarketSourcePackage(context.Background(), repository, repository, "", false, client)
		if err != nil {
			t.Fatal(err)
		}
		if phase != "" && len(requests())-before != len(source.ExpiryLRU[phase].Requests) {
			t.Fatalf("%s source request count mismatch", phase)
		}
	}
	for i := 0; i < 128; i++ {
		read(i, "")
	}
	read(0, "touch_expiring")
	a.pluginOriginalMarkets.mu.Lock()
	key := originalMarketPackageKey{"https://github.com/Owned/ExpiryLRU0", ""}
	entry := a.pluginOriginalMarkets.packages[key]
	entry.status.CheckedAt = time.Now().Add(-1801 * time.Second)
	a.pluginOriginalMarkets.packages[key] = entry
	a.pluginOriginalMarkets.mu.Unlock()
	read(128, "insert_after_expiry")
	read(1, "valid_oldest_survives")
	read(0, "expired_refetched")
}
