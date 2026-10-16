package durable

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestActualInstalledSiteStatisticAlgorithm(t *testing.T) {
	raw, err := readAutoSignInFixture("statistic-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Cases []struct {
			Input struct {
				Name    string
				Initial map[string]any
				Calls   []struct {
					Success bool
					Seconds *int
					Date    string
				}
			}
			States []map[string]any
		}
	}
	if json.Unmarshal(raw, &ref) != nil || len(ref.Cases) != 9 {
		t.Fatal("stat reference inventory")
	}
	for _, c := range ref.Cases {
		t.Run(c.Input.Name, func(t *testing.T) {
			stat := Statistic{}
			exists := c.Input.Initial != nil
			if exists {
				initial := c.Input.Initial
				value := func(key string) int {
					if n, ok := initial[key].(float64); ok {
						return int(n)
					}
					return 0
				}
				stat.Success = value("success")
				stat.Fail = value("fail")
				if initial["seconds"] != nil {
					n := value("seconds")
					stat.Seconds = &n
				}
				if note, ok := initial["note"].(map[string]any); ok {
					stat.Note = map[string]int{}
					for k, v := range note {
						stat.Note[k] = int(v.(float64))
					}
				}
			}
			for i, call := range c.Input.Calls {
				date := call.Date
				if date == "" {
					date = "2026-01-02 12:00:00"
				}
				stat = UpdateStatistic(stat, exists, call.Success, call.Seconds, date)
				exists = true
				got := map[string]any{"domain": "synthetic.invalid", "success": stat.Success, "fail": stat.Fail, "seconds": stat.Seconds, "lst_state": stat.LastState, "lst_mod_date": stat.LastDate, "note": stat.Note}
				var canonical map[string]any
				json.Unmarshal(marshal(got), &canonical)
				if !reflect.DeepEqual(canonical, c.States[i]) {
					t.Fatalf("source stat differs at%d\ngot=%v\nwant=%v", i, canonical, c.States[i])
				}
			}
		})
	}
}
func TestConcurrentAtomicStatisticsDoNotLoseCountsAndReplayDoesNotAdd(t *testing.T) {
	store, binding, _ := fixture(t)
	store.Start(context.Background(), binding, "stats", "test", json.RawMessage(`{}`), Baseline{})
	run := func() {
		var wait sync.WaitGroup
		for i := 0; i < 16; i++ {
			wait.Add(1)
			go func(i int) {
				defer wait.Done()
				lane := (&Execution{Store: store, Binding: binding, RunID: "stats", DigestKey: fixtureKey}).Lane(itoa(i), "")
				if err := lane.Statistic(context.Background(), "same-domain", true, 0, time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)); err != nil {
					t.Error(err)
				}
			}(i)
		}
		wait.Wait()
	}
	run()
	run()
	state, _ := store.State(context.Background(), binding)
	if state.Stats["same-domain"].Success != 16 || len(state.Stats["same-domain"].Note) != 1 {
		t.Fatal("counter duplicated/lost", state.Stats)
	}
}
func TestCacheBytesMissingEmptyInvalidAndReplayAreDurable(t *testing.T) {
	store, binding, _ := fixture(t)
	store.Start(context.Background(), binding, "cache", "test", json.RawMessage(`{}`), Baseline{})
	lane := (&Execution{Store: store, Binding: binding, RunID: "cache", DigestKey: fixtureKey}).Lane("cache", "")
	cache := Cache{Lane: lane, Key: "tjupt"}
	if err := cache.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	payload := []byte("{\n    broken: \xff\n}")
	if err := cache.Write(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	got, err := cache.Read(context.Background())
	if err != nil || string(got) != string(payload) {
		t.Fatal("cache content changed", got, err)
	}
	// A fresh scope replays the prior exact write/read, rather than reading a
	// later head value. The body may be malformed JSON; parser owns that failure.
	lane = (&Execution{Store: store, Binding: binding, RunID: "cache", DigestKey: fixtureKey}).Lane("cache", "")
	cache.Lane = lane
	cache.Ensure(context.Background())
	cache.Write(context.Background(), payload)
	got, err = cache.Read(context.Background())
	if err != nil || string(got) != string(payload) {
		t.Fatal("cache replay changed bytes")
	}
}
