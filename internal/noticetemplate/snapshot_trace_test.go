package noticetemplate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"light-media/internal/jinja/exec"
	"light-media/internal/notice"
)

func TestNotificationSnapshotKeepsEverySourceAssignment(t *testing.T) {
	raw, err := os.ReadFile("testdata/assignment.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name  string
		Pairs [][]json.RawMessage
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			e, _ := notice.NewEnvelope(notice.Object{})
			fields := []notice.TemplateField{}
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
			_ = e.ApplyTemplateFields(fields)
			snapshot, err := e.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			v, err := noticeJSONValue(raw)
			if err != nil {
				t.Fatal(err)
			}
			// Convert fields without losing json.Number's int/float distinction.
			var restored notice.EnvelopeSnapshot
			m := v.(map[string]any)
			restored.Version = 1
			restored.Fields = m["fields"].(map[string]any)
			for _, name := range m["assigned_enums"].([]any) {
				restored.AssignedEnums = append(restored.AssignedEnums, name.(string))
			}
			copy, err := notice.RestoreSnapshot(restored)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(copy.Dump(), e.Dump()) {
				t.Fatal("snapshot revalidated source assignment")
			}
			if copy.RequiresNativeEnumRouting() != e.RequiresNativeEnumRouting() {
				t.Fatal("routing provenance lost")
			}
			for _, field := range []string{"channel", "mtype", "ctype"} {
				a, ae := e.TemplateEnumValue(field)
				b, be := copy.TemplateEnumValue(field)
				if a != b || (ae == nil) != (be == nil) {
					t.Fatal("enum provenance lost", field, ae, be)
				}
			}
			copyFields := copy.Dump()
			copyFields["title"] = "changed"
			if e.Get("title") == "changed" {
				t.Fatal("snapshot aliases caller fields")
			}
		})
	}
}

func TestNotificationSnapshotRejectsInvalidStructure(t *testing.T) {
	e, _ := notice.NewEnvelope(notice.Object{"ctype": "subscribeAdded"})
	for _, kind := range []string{"version", "missing", "extra", "unknown-flag", "duplicate-flag", "invalid-unassigned-enum"} {
		t.Run(kind, func(t *testing.T) {
			snapshot, _ := e.Snapshot()
			switch kind {
			case "version":
				snapshot.Version = 2
			case "missing":
				delete(snapshot.Fields, "text")
			case "extra":
				snapshot.Fields["unknown"] = true
			case "unknown-flag":
				snapshot.AssignedEnums = []string{"title"}
			case "duplicate-flag":
				snapshot.AssignedEnums = []string{"ctype", "ctype"}
			case "invalid-unassigned-enum":
				snapshot.Fields["ctype"] = "unknown"
			}
			if _, err := notice.RestoreSnapshot(snapshot); !errors.Is(err, notice.ErrInvalidEnvelope) {
				t.Fatal("invalid private snapshot accepted", err)
			}
		})
	}
}

func TestOriginalMessageCacheTrace(t *testing.T) {
	raw, err := os.ReadFile("testdata/message.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name                     string
		Fields, Extra, Templates json.RawMessage
		Cache                    []struct {
			Key     string
			Context any
		}
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			fields, err := noticeJSONValue(c.Fields)
			if err != nil {
				t.Fatal(err)
			}
			e, err := notice.NewEnvelope(fields.(map[string]any))
			if err != nil {
				t.Fatal(err)
			}
			extra, err := DecodeContextJSON(c.Extra)
			if err != nil {
				t.Fatal(err)
			}
			templates, err := noticeJSONValue(c.Templates)
			if err != nil {
				t.Fatal(err)
			}
			h := NewHelper(nil)
			result, err := h.RenderMessage(t.Context(), e, ContextInput{Extra: extra}, func(context.Context) (any, error) { return templates, nil })
			if err != nil {
				t.Fatal(err)
			}
			if len(c.Cache) == 0 {
				if result.SourceCache != nil {
					t.Fatal("trace invents a source cache write")
				}
				return
			}
			if len(c.Cache) != 1 || result.SourceCache == nil || result.SourceCache.Key != c.Cache[0].Key {
				t.Fatal("cache trace does not reflect actual rendered value")
			}
			want, err := exec.PythonDumps(c.Cache[0].Context, exec.JSONOptions{SortKeys: true})
			if err != nil {
				t.Fatal(err)
			}
			got, err := exec.PythonDumps(result.SourceCache.Context, exec.JSONOptions{SortKeys: true})
			if err != nil || got != want {
				t.Fatal("source cache context differs", got, want, err)
			}
			cached := h.Cache.Get(result.SourceCache.Key)
			if cached == nil {
				t.Fatal("trace did not correspond to actual cache write")
			}
		})
	}
}
