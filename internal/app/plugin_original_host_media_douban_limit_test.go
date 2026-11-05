package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestOriginalHostMediaDoubanFixedSourceLimiterAndRetry(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-integration-20261031/source-models/douban-limit-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Events []struct {
			Now               float64
			Method, Operation string
			Result            json.RawMessage
			Calls             int
			Next, Wait        float64
		}
		RetryCases []struct {
			Failures int
			Limited  bool
			Calls    int
			Sleeps   []int
			Result   json.RawMessage
			Error    *string
		} `json:"retry_cases"`
	}
	if json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("source limiter oracle")
	}
	a := newTestApp(t)
	clock := 0.
	a.originalHostMediaRecognition.WallClock = func() float64 { return clock }
	calls := map[string]int{}
	for _, row := range oracle.Events {
		clock = row.Now
		got, err := a.originalHostDoubanLimited(context.Background(), row.Method == "match", func() (map[string]any, error) {
			calls[row.Method]++
			switch row.Operation {
			case "limit":
				return nil, bad("MEDIA_RATE_LIMITED", "owned")
			case "empty":
				return nil, nil
			default:
				return map[string]any{"ok": true}, nil
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if !originalBaseJSONEqual(mustCleanSeedJSON(got), row.Result) {
			t.Fatalf("source limiter result %s", row.Result)
		}
		limit := a.originalHostDouban.detail
		if row.Method == "match" {
			limit = a.originalHostDouban.match
		}
		if limit.next != row.Next || limit.wait != row.Wait || calls[row.Method] != row.Calls {
			t.Fatalf("limiter at %v got %v calls%d want next%v wait%v calls%d", row.Now, limit, calls[row.Method], row.Next, row.Wait, row.Calls)
		}
	}
	for _, row := range oracle.RetryCases {
		a.originalHostDouban.match = originalHostDoubanLimit{}
		clock = 0
		count := 0
		sleeps := []int{}
		a.originalHostDouban.wait = func(ctx context.Context, d time.Duration) error {
			sleeps = append(sleeps, int(d/time.Second))
			clock += d.Seconds()
			return nil
		}
		got, err := a.originalHostDoubanRetry(context.Background(), func() (map[string]any, error) {
			count++
			if count <= row.Failures {
				if row.Limited {
					return nil, bad("MEDIA_RATE_LIMITED", "owned")
				}
				return nil, errors.New("owned value error")
			}
			return map[string]any{"ok": true}, nil
		})
		if (err != nil) != (row.Error != nil) || count != row.Calls || !reflect.DeepEqual(sleeps, row.Sleeps) || !originalBaseJSONEqual(mustCleanSeedJSON(got), row.Result) {
			t.Fatalf("retry %d limited%v got=%v err%v sleeps%v calls%d", row.Failures, row.Limited, got, err, sleeps, count)
		}
	}
}
