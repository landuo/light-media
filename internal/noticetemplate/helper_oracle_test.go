package noticetemplate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"light-media/internal/jinja/exec"
)

func TestParseSourceOracle(t *testing.T) {
	cases(t, "parse.jsonl.gz", func(t *testing.T, raw []byte) {
		var c struct {
			Input  json.RawMessage
			Kind   *string
			Result *string
		}
		if e := decode(raw, &c); e != nil {
			t.Fatal(e)
		}
		input, e := DecodeValueJSON(c.Input)
		if e != nil {
			t.Fatal(e)
		}
		kind := ""
		if c.Kind != nil {
			kind = *c.Kind
		}
		out, e := ParseTemplateContent(input, kind)
		if e != nil || out == nil != (c.Result == nil) || out != nil && *out != *c.Result {
			t.Fatalf("parse mismatch expected=%v got=%v error=%v", c.Result, out, e)
		}
	})
}
func TestCacheKeySourceOracle(t *testing.T) {
	cases(t, "cache-key.jsonl.gz", func(t *testing.T, raw []byte) {
		var c struct {
			Input         json.RawMessage
			Result, Error string
		}
		if e := decode(raw, &c); e != nil {
			t.Fatal(e)
		}
		input, e := DecodeValueJSON(c.Input)
		if e != nil {
			t.Fatal(e)
		}
		out, e := CacheKey(input)
		if c.Error != "" {
			var se SourceError
			if !errors.As(e, &se) || se.Class != c.Error {
				t.Fatalf("source %s native error=%v", c.Error, e)
			}
			return
		}
		if e != nil || out != c.Result {
			t.Fatalf("cache key expected=%q native=%q err=%v", c.Result, out, e)
		}
	})
}
func TestHelperRenderSourceOracle(t *testing.T) {
	cases(t, "helper.jsonl.gz", func(t *testing.T, raw []byte) {
		var c struct {
			Input          json.RawMessage
			Kind           *string
			Extra          json.RawMessage
			Result, Cached any
			Error          string
		}
		if e := decode(raw, &c); e != nil {
			t.Fatal(e)
		}
		input, e := DecodeValueJSON(c.Input)
		if e != nil {
			t.Fatal(e)
		}
		extra, e := DecodeContextJSON(c.Extra)
		if e != nil {
			t.Fatal(e)
		}
		kind := ""
		if c.Kind != nil {
			kind = *c.Kind
		}
		h := NewHelper(nil)
		out, e := h.Render(context.Background(), input, kind, ContextInput{Extra: extra})
		if c.Error != "" {
			var se SourceError
			if !errors.As(e, &se) || se.Class != c.Error {
				t.Fatalf("source %s native error=%v", c.Error, e)
			}
			return
		}
		if e != nil {
			t.Fatal(e)
		}
		want, e1 := exec.PythonDumps(c.Result, exec.JSONOptions{SortKeys: true})
		got, e2 := exec.PythonDumps(out, exec.JSONOptions{SortKeys: true})
		if e1 != nil || e2 != nil || want != got {
			t.Fatalf("helper result expected=%q native=%q errors=%v/%v", want, got, e1, e2)
		}
		if c.Cached != nil {
			cached, e := h.GetCacheContext(out)
			if e != nil {
				t.Fatal(e)
			}
			want, e1 = exec.PythonDumps(c.Cached, exec.JSONOptions{SortKeys: true})
			got, e2 = exec.PythonDumps(cached, exec.JSONOptions{SortKeys: true})
			if e1 != nil || e2 != nil || want != got {
				t.Fatalf("cached context mismatch expected=%q native=%q errors=%v/%v", want, got, e1, e2)
			}
		}
	})
}
