package autosignin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func orderedHistoryKeys(raw json.RawMessage) []string {
	var input map[string]json.RawMessage
	json.Unmarshal(raw, &input)
	if len(input["history"]) == 0 {
		return []string{}
	}
	decoder := json.NewDecoder(bytes.NewReader(input["history"]))
	decoder.Token()
	keys := []string{}
	for decoder.More() {
		key, _ := decoder.Token()
		keys = append(keys, key.(string))
		decoder.Decode(new(any))
	}
	return keys
}
func TestActualInstalledDailyExecutionAndDeletedReferences(t *testing.T) {
	raw, err := readAutoSignInFixture("oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Name, Kind, Error string
			ErrorClass        string `json:"error_class"`
			Input             json.RawMessage
			Expected          any
			PartialTrace      any `json:"partial_trace"`
		}
	}
	if Decode(raw, &source) != nil {
		t.Fatal("source invalid")
	}
	count := 0
	for _, c := range source.Cases {
		if c.Kind != "do" && c.Kind != "deleted" {
			continue
		}
		count++
		t.Run(c.Name, func(t *testing.T) {
			var input Object
			Decode(c.Input, &input)
			clock := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
			engine := New(fixtureInventory(input), func() time.Time { return clock })
			config, exists := input["config"]
			if !exists {
				config = Object{"enabled": true, "sign_sites": []any{"all"}, "retry_keyword": "错误|失败", "notify": true}
			}
			if err := engine.Init(config); err != nil {
				t.Fatal(err)
			}
			if history, ok := input["history"].(Object); ok {
				engine.Data = clone(history).(Object)
			}
			engine.Trace = []Object{}
			var runErr error
			if c.Kind == "deleted" {
				runErr = engine.SiteDeleted(input["site_id"])
			} else {
				results := map[string]Result{"1": {Message: "签到成功", Success: true, LoggedIn: true}, "2": {Message: "签到失败，Cookie已失效"}, "x": {Message: "登录成功，签到未确认", LoggedIn: true}}
				if overrides, ok := input["results"].(Object); ok {
					for id, value := range overrides {
						o := value.(Object)
						results[id] = Result{Message: fmt.Sprint(o["message"]), Success: Truth(o["success"]), LoggedIn: Truth(o["logged_in"])}
					}
				}
				hooks := RunHooks{Visit: func(_ context.Context, site Object, _ string, _ bool) (Result, error) {
					result := results[pythonScalar(site["id"])]
					result.SiteName = fmt.Sprint(site["name"])
					return result, nil
				}, Emit: func(context.Context, string, Object) error { return nil }, Notify: func(context.Context, Object) error { return nil }}
				operation := "签到"
				if value, ok := input["type"].(string); ok {
					operation = value
				}
				selected, exists := input["selected"]
				if !exists {
					selected = []any{"all"}
				}
				var event *SourceEvent
				if Truth(input["event"]) {
					event = &SourceEvent{Channel: "synthetic-channel", User: "synthetic-user"}
				}
				runErr = engine.Do(context.Background(), clock, operation, selected, event, orderedHistoryKeys(c.Input), hooks)
			}
			if c.Error != "" {
				if runErr == nil {
					t.Fatal("source exception became success")
				}
				if c.ErrorClass == "error" {
					var compile *RetryCompileError
					if !errors.As(runErr, &compile) {
						t.Fatal("source regex compile failure lost", runErr)
					}
				} else if runErr.Error() != c.Error {
					t.Fatal("source error differs", runErr, c.Error)
				}
				if !equivalent(canonical(engine.Trace), c.PartialTrace) {
					t.Fatalf("pre-error source operations differ\ngot=%s\nwant=%s", mustJSON(engine.Trace), mustJSON(c.PartialTrace))
				}
				return
			}
			if runErr != nil || !equivalent(canonical(engine.Snapshot()), c.Expected) {
				t.Fatalf("daily source differs err=%v\ngot=%s\nwant=%s", runErr, mustJSON(engine.Snapshot()), mustJSON(c.Expected))
			}
		})
	}
	if count != 19 {
		t.Fatal("run/deletion source inventory changed", count)
	}
}

func TestBoundedParallelVisitsRetainInputOrderAndAwaitAllResults(t *testing.T) {
	indexers := []Object{}
	for i := 1; i <= 4; i++ {
		indexers = append(indexers, Object{"id": json.Number(fmt.Sprint(i)), "name": fmt.Sprint("Site", i)})
	}
	engine := New(Inventory{Configured: indexers, Indexers: indexers}, func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) })
	if err := engine.Init(Object{"enabled": true, "sign_sites": []any{"all"}, "queue_cnt": json.Number("2"), "retry_keyword": "错误|失败"}); err != nil {
		t.Fatal(err)
	}
	engine.Trace = []Object{}
	var active, maximum, completed atomic.Int32
	gate := make(chan struct{})
	var once sync.Once
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hooks := RunHooks{Visit: func(ctx context.Context, site Object, operation string, schemas bool) (Result, error) {
		if operation != "签到" || !schemas {
			t.Error("source operation/module state changed")
		}
		value := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if value <= old || maximum.CompareAndSwap(old, value) {
				break
			}
		}
		if value == 2 {
			once.Do(func() { close(gate) })
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
		id, _ := site["id"].(json.Number).Int64()
		time.Sleep(time.Duration(5-id) * time.Millisecond)
		completed.Add(1)
		return Result{SiteName: fmt.Sprint(site["name"]), Message: "签到成功", Success: true, LoggedIn: true}, nil
	}}
	if err := engine.Do(ctx, engine.Now(), "签到", []any{"all"}, nil, nil, hooks); err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 2 || completed.Load() != 4 {
		t.Fatal("source pool limit or completion lost", maximum.Load(), completed.Load())
	}
	rows := engine.Data["1月2日"].([]any)
	for i, row := range rows {
		if row.(Object)["site_id"] != json.Number(fmt.Sprint(i+1)) {
			t.Fatal("completion order replaced source map input order")
		}
	}
}

func TestUnknownAdapterAndUnverifiedRegexDoNotReturnSyntheticSuccess(t *testing.T) {
	if _, err := parallelVisits(context.Background(), []Object{{"id": "synthetic"}}, 1, "签到", false, nil, nil); !errors.Is(err, ErrAdapterNotReady) {
		t.Fatal("missing native adapter did not remain explicit")
	}
	for _, pattern := range []string{`\w+`, `(?i)failure`, `\d+`, `(?x)bad`} {
		if _, err := VerifiedRetryMatch(pattern, "anything"); !errors.Is(err, ErrRetryPatternNotReady) {
			t.Fatal("unverified Python dialect was silently changed", pattern, err)
		}
	}
}
