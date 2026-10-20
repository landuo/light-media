package noticetemplate

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"light-media/internal/notice"
)

func TestSourceNotificationTemplateAssignment(t *testing.T) {
	raw, err := os.ReadFile("testdata/assignment.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string
		Pairs  [][]json.RawMessage
		Result json.RawMessage
		Error  *string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			e, err := notice.NewEnvelope(notice.Object{})
			if err != nil {
				t.Fatal(err)
			}
			fields := make([]notice.TemplateField, 0, len(c.Pairs))
			for _, p := range c.Pairs {
				var key string
				if err := json.Unmarshal(p[0], &key); err != nil {
					t.Fatal(err)
				}
				value, err := noticeJSONValue(p[1])
				if err != nil {
					t.Fatal(err)
				}
				fields = append(fields, notice.TemplateField{Name: key, Value: value})
			}
			err = e.ApplyTemplateFields(fields)
			var source notice.SourceError
			if c.Error == nil {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.As(err, &source) || source.Class != *c.Error {
				t.Fatalf("source error: %v, want %s", err, *c.Error)
			}
			want, err := noticeJSONValue(c.Result)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(e.Dump(), want) {
				t.Fatalf("partial fields: %#v, want %#v", e.Dump(), want)
			}
		})
	}
}

func TestSourceCachetoolsMemoryTTLAndLRU(t *testing.T) {
	raw, err := os.ReadFile("testdata/cache-source.json")
	if err != nil {
		t.Fatal(err)
	}
	var ops []struct {
		Op, Key       string
		At            float64
		Value, Result Object
	}
	if err := json.Unmarshal(raw, &ops); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(0, 0)
	c := NewContextCache(func() time.Time { return now })
	for i, op := range ops {
		now = time.Unix(0, int64(op.At*float64(time.Second)))
		switch op.Op {
		case "set":
			c.Set(op.Key, op.Value)
		case "get":
			if got := c.Get(op.Key); !reflect.DeepEqual(got, op.Result) {
				t.Fatalf("source op %d key %s: %#v, want %#v", i, op.Key, got, op.Result)
			}
		case "mutate":
			c.Get(op.Key)["reference"] = true
		default:
			t.Fatalf("unknown source op %s", op.Op)
		}
	}
}
