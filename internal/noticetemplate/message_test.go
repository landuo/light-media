package noticetemplate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"light-media/internal/notice"
)

func TestOriginalMessageTemplateHelper(t *testing.T) {
	raw, err := os.ReadFile("testdata/message.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name                  string
		Fields, Extra, Result json.RawMessage
		Templates             json.RawMessage
		Error                 *string
		Reads                 int
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			v, err := noticeJSONValue(c.Fields)
			if err != nil {
				t.Fatal(err)
			}
			e, err := notice.NewEnvelope(v.(map[string]any))
			if err != nil {
				t.Fatal(err)
			}
			v, err = noticeJSONValue(c.Extra)
			if err != nil {
				t.Fatal(err)
			}
			extra := v.(map[string]any)
			templates, err := noticeJSONValue(c.Templates)
			if err != nil {
				t.Fatal(err)
			}
			reads := 0
			got, err := NewHelper(nil).RenderMessage(t.Context(), e, ContextInput{Extra: extra}, func(context.Context) (any, error) { reads++; return templates, nil })
			if err != nil {
				t.Fatal(err)
			}
			wantError := ""
			if c.Error != nil {
				wantError = *c.Error
			}
			want, err := noticeJSONValue(c.Result)
			if err != nil {
				t.Fatal(err)
			}
			if got.SourceError != wantError || reads != c.Reads || !reflect.DeepEqual(got.Message.Dump(), want) {
				t.Fatalf("message source error=%q reads=%d fields=%#v; want error=%q reads=%d fields=%#v", got.SourceError, reads, got.Message.Dump(), wantError, c.Reads, want)
			}
		})
	}
}

func TestOriginalTemplateEnumReentry(t *testing.T) {
	raw, err := os.ReadFile("testdata/enum-reentry.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Template      string
		First, Result json.RawMessage
		Reads         int
		Error         *string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		e, err := notice.NewEnvelope(notice.Object{"ctype": "subscribeAdded"})
		if err != nil {
			t.Fatal(err)
		}
		h := NewHelper(nil)
		reads := 0
		lookup := func(context.Context) (any, error) { reads++; return Object{"subscribeAdded": c.Template}, nil }
		first, err := h.RenderMessage(t.Context(), e, ContextInput{Extra: Object{"present": true}}, lookup)
		if err != nil || first.SourceError != "" {
			t.Fatal(first.SourceError, err)
		}
		want, err := noticeJSONValue(c.First)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first.Message.Dump(), want) {
			t.Fatal("first assignment differs", first.Message.Dump(), want)
		}
		reads = 0
		second, err := h.RenderMessage(t.Context(), e, ContextInput{Extra: Object{"present": true}}, lookup)
		if err != nil {
			t.Fatal(err)
		}
		wantError := ""
		if c.Error != nil {
			wantError = *c.Error
		}
		want, err = noticeJSONValue(c.Result)
		if err != nil {
			t.Fatal(err)
		}
		if second.SourceError != wantError || reads != c.Reads || !reflect.DeepEqual(second.Message.Dump(), want) {
			t.Fatalf("second render error=%q reads=%d fields=%#v; source error=%q reads=%d fields=%#v", second.SourceError, reads, second.Message.Dump(), wantError, c.Reads, want)
		}
	}
	// Router.Post makes a private clone before rendering. That clone must not
	// erase a prior unvalidated enum assignment into a freshly validated enum.
	e, _ := notice.NewEnvelope(notice.Object{"ctype": "subscribeAdded"})
	if err := e.ApplyTemplateFields([]notice.TemplateField{{Name: "ctype", Value: "subscribeAdded"}}); err != nil {
		t.Fatal(err)
	}
	seen := false
	r := notice.Router{Dependencies: notice.Dependencies{Render: func(_ context.Context, copy *notice.Envelope, _ notice.Business, _ notice.Object) (*notice.Envelope, error) {
		_, err := copy.TemplateEnumValue("ctype")
		var source notice.SourceError
		if !errors.As(err, &source) || source.Class != "AttributeError" {
			t.Fatal("clone erased source enum provenance", err)
		}
		seen = true
		return nil, nil
	}}}
	if err := r.Post(t.Context(), e, notice.Business{}, nil); err != nil || !seen {
		t.Fatal("render clone not observed", err)
	}
}

func TestAssignedEnumQueueNeedsNativeRouting(t *testing.T) {
	e, _ := notice.NewEnvelope(notice.Object{"title": "fixture", "mtype": "订阅"})
	if err := e.ApplyTemplateFields([]notice.TemplateField{{Name: "mtype", Value: "订阅"}}); err != nil {
		t.Fatal(err)
	}
	if !e.RequiresNativeEnumRouting() {
		t.Fatal("plain assigned string confused with validated enum")
	}
	r := notice.Router{}
	if err := r.Post(t.Context(), e, notice.Business{}, nil); !errors.Is(err, notice.ErrTemplateDependency) {
		t.Fatal("unported routing claimed normal source return", err)
	}
}
