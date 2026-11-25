package systemprocess

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

func TestSystemProcessWorkerReplaysInstalledSource(t *testing.T) {
	raw, err := os.ReadFile("reference/system-process-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		Form  json.RawMessage
		Cases []struct {
			Name                    string
			Config, Page, Dashboard json.RawMessage
			State                   bool
		}
	}
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	worker := &Plugin{sample: func(context.Context) ([]ProcessInfo, error) { return nil, nil }}
	caller := func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method != sdk.BaseGetInstanceMetadata {
			return nil, errors.New("unexpected SystemProcess host method")
		}
		return json.Marshal(sdk.InstanceMetadata{Name: "系统进程"})
	}
	compare := func(t *testing.T, method string, expected json.RawMessage) {
		t.Helper()
		got, err := worker.Handle(ctx, method, nil, caller)
		if err != nil {
			t.Fatal(method, err)
		}
		var a, b any
		if json.Unmarshal(got, &a) != nil || json.Unmarshal(expected, &b) != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("%s differs from installed source: got %s, want %s", method, got, expected)
		}
	}
	compare(t, "get_form", original.Form)
	for _, c := range original.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if _, err := worker.Handle(ctx, "init", c.Config, caller); err != nil {
				t.Fatal(err)
			}
			state, _ := json.Marshal(c.State)
			compare(t, "get_state", state)
			compare(t, "get_page", c.Page)
			compare(t, "get_dashboard", c.Dashboard)
		})
	}
}
