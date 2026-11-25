package mergemessagenotify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestSourceNoticeRouting(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-routing.json")
	if err != nil {
		t.Fatal(err)
	}
	type attempt struct {
		Channel string         `json:"channel"`
		Title   any            `json:"title"`
		Text    any            `json:"text"`
		Type    any            `json:"type"`
		Fields  map[string]any `json:"fields"`
	}
	var fixture struct {
		Cases []struct {
			Name   string
			Config map[string]any
			Fields map[string]any
			Calls  []attempt
			Logs   [][2]string
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("missing original observations")
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			calls := []attempt{}
			logs := [][2]string{}
			p := New(Host{Log: func(_ context.Context, level, message string) {
				logs = append(logs, [2]string{level, message})
			}})
			p.config = row.Config
			p.registered = []Channel{
				{Key: "first", Name: "first", Elements: []any{}},
				{Key: "second", Name: "second", Elements: []any{}},
				{Key: "broken", Name: "broken", Elements: []any{}},
			}
			p.dispatch(t.Context(), row.Fields, func(_ context.Context, key string, in Message) (any, error) {
				calls = append(calls, attempt{key, in.Title, in.Text, in.RawType, in.Ext})
				if key == "broken" {
					return nil, errors.New("synthetic failure")
				}
				return key == "first", nil
			})
			if !reflect.DeepEqual(calls, row.Calls) || !reflect.DeepEqual(logs, row.Logs) {
				t.Fatalf("calls = %#v, want %#v\nlogs = %v, want %v", calls, row.Calls, logs, row.Logs)
			}
		})
	}
}
