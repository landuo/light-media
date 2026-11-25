package autosignin

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestRefinementSavedRandomSchedulePublicGetterOverflow(t *testing.T) {
	raw, e := os.ReadFile(edgeReviewRoot + "public-getter-source333.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Cases []struct {
			Raw       string `json:"raw_times"`
			Effects   []string
			JobCount  *int `json:"job_count"`
			Error     *string
			ErrorType *string `json:"error_type"`
		}
	}
	if e = Decode(raw, &f); e != nil {
		t.Fatal(e)
	}
	for i, c := range f.Cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			if !json.Valid([]byte(c.Raw)) {
				t.Fatal("not an IPC-legal test value")
			}
			var times any
			if e = Decode([]byte(c.Raw), &times); e != nil {
				t.Fatal(e)
			}
			now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			engine := New(Inventory{}, func() time.Time { return now })
			engine.Enabled = true
			engine.Cron = ""
			effects := []string{}
			engine.HistoryLive = func(key string) (any, error) {
				effects = append(effects, "get_data")
				return Object{"date": "2026-11-06", "times": times}, nil
			}
			engine.Effects = func(effect Effect) error {
				if effect.Kind == "history.put" {
					effects = append(effects, "save_data")
				}
				return nil
			}
			jobs, e := engine.Services(func(a, b int) (int, error) { effects = append(effects, "random"); return a, nil }, "UTC")
			if c.Error != nil {
				if e == nil || e.Error() != *c.Error {
					t.Errorf("raw=%s targeterr=%v jobs=%d effects=%v source=%s/%s effects=%v", c.Raw, e, len(jobs), effects, *c.ErrorType, *c.Error, c.Effects)
				}
			} else if e != nil || len(jobs) != *c.JobCount {
				t.Errorf("target %v/%v sourcecount=%v", jobs, e, c.JobCount)
			}
			if !reflect.DeepEqual(effects, c.Effects) {
				t.Errorf("getter side effects target=%v source=%v", effects, c.Effects)
			}
		})
	}
}
