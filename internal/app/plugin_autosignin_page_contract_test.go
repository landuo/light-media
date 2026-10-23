package app

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestAutoSignInHostPageDialogEnvelope(t *testing.T) {
	for _, nodes := range [][]any{{}, {map[string]any{"component": "VRow", "props": map[string]any{"n": json.Number("9007199254740993"), "disabled": false}, "content": []any{map[string]any{"component": "span", "text": "<owned>&测试"}}}}} {
		got, err := autoSignInPageResponse(nodes)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			RenderMode string          `json:"render_mode"`
			Page       json.RawMessage `json:"page"`
		}
		if json.Unmarshal(got, &envelope) != nil || envelope.RenderMode != "vuetify" {
			t.Fatal("Dialog envelope", string(got))
		}
		var before, after any
		raw, _ := json.Marshal(nodes)
		decode := func(raw []byte, out *any) {
			t.Helper()
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if e := dec.Decode(out); e != nil {
				t.Fatal(e)
			}
		}
		decode(raw, &before)
		decode(envelope.Page, &after)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("node array changed")
		}
	}
	for _, nodes := range [][]any{nil, {math.NaN()}, {make(chan int)}} {
		if got, err := autoSignInPageResponse(nodes); err == nil || got != nil {
			t.Fatal("invalid page became successful", string(got), err)
		}
	}
}
