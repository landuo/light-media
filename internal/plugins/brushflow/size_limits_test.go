package brushflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sort"
	"testing"
)

type sizeOracle struct {
	Name        string   `json:"name"`
	TaskMissing bool     `json:"task_missing"`
	TaskLimit   *float64 `json:"task_limit"`
	TaskSize    float64  `json:"task_size"`
	GlobalLimit *float64 `json:"global_limit"`
	GlobalSize  *float64 `json:"global_size"`
	Tasks       []string `json:"tasks"`
	Downloaders map[string]struct {
		Sizes           []float64
		Inactive, Error bool
	} `json:"downloaders"`
	Expected struct {
		Passed bool
		Reason *string
		Calls  []string
		Error  string
	} `json:"expected"`
	calls []string
}

func (o *sizeOracle) DownloadSizes(_ context.Context, name string) (bool, bool, []float64, error) {
	o.calls = append(o.calls, name)
	d, exists := o.Downloaders[name]
	var err error
	if d.Error {
		err = errors.New("owned fixture transport failure")
	}
	return exists, !d.Inactive, d.Sizes, err
}
func TestOriginalSizeLimitBusinessCases(t *testing.T) {
	raw, err := os.ReadFile("reference/size-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct{ Cases []sizeOracle }
	if err = json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if len(reference.Cases) != 30 {
		t.Fatal("incomplete source cases", len(reference.Cases))
	}
	for _, c := range reference.Cases {
		t.Run(c.Name, func(t *testing.T) {
			tasks := []SizeTask{}
			for _, name := range c.Tasks {
				tasks = append(tasks, SizeTask{Downloader: name})
			}
			var current *SizeTask
			if !c.TaskMissing {
				current = &SizeTask{DiskSize: c.TaskLimit}
			}
			pass, reason, err := EvaluateSizeCondition(context.Background(), current, c.TaskSize, c.GlobalLimit, c.GlobalSize, tasks, &c)
			if c.Expected.Error != "" {
				if err == nil || err.Error() != c.Expected.Error {
					t.Fatal(err, c.Expected.Error)
				}
			} else {
				want := ""
				if c.Expected.Reason != nil {
					want = *c.Expected.Reason
				}
				if err != nil || pass != c.Expected.Passed || reason != want {
					t.Fatal(pass, reason, err, "expected", c.Expected)
				}
			}
			sort.Strings(c.calls)
			if len(c.calls) != len(c.Expected.Calls) || (len(c.calls) > 0 && !reflect.DeepEqual(c.calls, c.Expected.Calls)) {
				t.Fatal("wrong downloader reads", c.calls, c.Expected.Calls)
			}
		})
	}
}
