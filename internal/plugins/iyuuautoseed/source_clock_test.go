package iyuuautoseed

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestDurableSourceHelperReadsTimeAtEachActualReseedAndReplays(t *testing.T) {
	raw, e := os.ReadFile("reference/source_clock-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Cases []struct {
			Retry    bool `json:"retry"`
			Requests []struct {
				Params map[string]json.RawMessage `json:"params"`
			} `json:"requests"`
		} `json:"cases"`
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "two-batches", true: "site-hash-retry"}[retry], func(t *testing.T) {
			ctx := context.Background()
			var mu sync.Mutex
			timestamps := []json.Number{}
			wire, clockReads := 0, 0
			replaying := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				wire++
				if replaying {
					t.Error("replay dispatched helper HTTP")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/reseed/sites/index":
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"sites": []any{map[string]any{"id": 1, "base_url": "https://owned.invalid/", "download_page": "download.php?id={id}"}}}})
				case "/reseed/sites/reportExisting":
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"sid_sha1": "owned-sid"}})
				case "/reseed/index/index":
					var p map[string]any
					d := json.NewDecoder(r.Body)
					d.UseNumber()
					if e := d.Decode(&p); e != nil {
						t.Error(e)
					}
					timestamp, ok := p["timestamp"].(json.Number)
					if !ok {
						t.Error("source timestamp type", p)
					}
					timestamps = append(timestamps, timestamp)
					if retry && len(timestamps) == 1 {
						json.NewEncoder(w).Encode(map[string]any{"code": 7, "msg": "站点哈希值 require again"})
					} else {
						json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
					}
				default:
					t.Error("unexpected helper request", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "journal.sqlite")
			key := bytes.Repeat([]byte{77}, 32)
			id := RunIdentity{Instance: "OwnedClock", Generation: 1, Revision: 1, Fingerprint: "owned-source-input", Run: "owned-clock-run"}
			runHelper := func(replay bool) {
				j, e := OpenJournal(path, key)
				if e != nil {
					t.Fatal(e)
				}
				defer j.Close()
				run, e := j.Begin(ctx, id, map[string]any{}, liveGuard)
				if e != nil {
					t.Fatal(e)
				}
				api, e := NewAPITransport(TrustedAPIBinding{Token: "owned-synthetic-token"}, APITransportOptions{Guard: liveGuard, LoopbackOrigin: server.URL})
				if e != nil {
					t.Fatal(e)
				}
				defer api.Close()
				durable, e := NewDurableAPI(ctx, run, api)
				if e != nil {
					t.Fatal(e)
				}
				durable.clock = func() time.Time {
					if replay {
						t.Fatal("replay read fresh source clock")
					}
					clockReads++
					return time.Unix(1720000000+int64(clockReads-1)*7200, 875000000)
				}
				helper, e := NewHelper(durable, func() time.Time { t.Fatal("actual durable helper used startup/fallback clock"); return time.Time{} })
				if e != nil {
					t.Fatal(e)
				}
				count := 2
				if retry {
					count = 1
				}
				for range count {
					result, e := helper.SeedInfo(ctx, []string{"original"})
					if e != nil || result.Failure != nil {
						t.Fatal("actual source helper", result, e)
					}
				}
			}
			runHelper(false)
			mu.Lock()
			wantWire := wire
			got := append([]json.Number{}, timestamps...)
			replaying = true
			mu.Unlock()
			want := []json.Number{}
			for _, source := range oracle.Cases {
				if source.Retry == retry {
					for _, request := range source.Requests {
						if stamp, ok := request.Params["timestamp"]; ok {
							var n json.Number
							if e = json.Unmarshal(stamp, &n); e != nil {
								t.Fatal(e)
							}
							want = append(want, n)
						}
					}
				}
			}
			if !reflect.DeepEqual(got, want) || clockReads != 2 || len(want) != 2 {
				t.Fatal("source used initial time across batches/retry", got, clockReads)
			}
			runHelper(true)
			mu.Lock()
			defer mu.Unlock()
			if wire != wantWire || clockReads != 2 {
				t.Fatal("replayed source read fresh effects", wire, wantWire, clockReads)
			}
		})
	}
}

func TestSourceTimeUsesPythonIntOfSecondsDouble(t *testing.T) {
	raw, e := os.ReadFile("reference/source_clock-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Conversions []struct {
			Seconds int64 `json:"seconds"`
			Ns      int64 `json:"nanoseconds"`
			Want    int64 `json:"expected"`
		} `json:"conversions"`
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	if len(oracle.Conversions) != 5 {
		t.Fatal("source clock oracle missing cases")
	}
	for _, tc := range oracle.Conversions {
		got, e := sourceUnixTime(time.Unix(tc.Seconds, tc.Ns))
		if e != nil || got != tc.Want {
			t.Fatal(tc, got, e)
		}
	}
}
