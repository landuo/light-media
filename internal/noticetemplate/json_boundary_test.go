package noticetemplate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"light-media/internal/jinja/exec"
	"light-media/internal/notice"
)

func TestPythonJSONLoadsValues(t *testing.T) {
	raw, err := os.ReadFile("testdata/json-values.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Raw, Result, Error string }
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			value, err := DecodeValueJSON([]byte(c.Raw))
			if c.Error != "" {
				if err == nil {
					t.Fatal("source invalid JSON accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := exec.PythonDumps(value, exec.JSONOptions{})
			if err != nil || got != c.Result {
				t.Fatalf("source JSON %q native %q error=%v", c.Result, got, err)
			}
		})
	}
}

func TestNativeUnsupportedTemplateBoundary(t *testing.T) {
	for _, text := range []string{`{{ lipsum(1) }}`, `{{ cycler('a','b').next() }}`, `{% include '/__inline__' %}`, `{% if false %}{% extends 'unused' %}{% endif %}`, `{{ value + 1 }}`} {
		_, err := RenderWithContext(t.Context(), text, Object{"value": json.Number("1000000000000000000000000000000")})
		if !errors.Is(err, exec.ErrUnsupported) {
			t.Fatalf("unported source construct claimed a result: %q %v", text, err)
		}
	}
	for _, text := range []string{`{% do 1 %}`, `{% for x in [1] %}{% break %}{% endfor %}`, `{% trans %}x{% endtrans %}`} {
		_, err := RenderWithContext(t.Context(), text, Object{})
		var source *exec.SourceError
		if !errors.As(err, &source) || source.Class != "TemplateSyntaxError" {
			t.Fatalf("source extension syntax error lost: %v", err)
		}
	}
	_, err := RenderWithContext(t.Context(), `plain`, Object{"unused": json.Number("1000000000000000000000000000000")})
	if err != nil {
		t.Fatal("unused source value changed render", err)
	}
}

func TestMessageNativeDependencyIsNotCaughtAsSourceReturn(t *testing.T) {
	for _, kind := range []string{"lookup", "unsupported", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			e, err := notice.NewEnvelope(notice.Object{"ctype": "subscribeAdded"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			expected := errors.New("owned storage failure")
			result, err := NewHelper(nil).RenderMessage(ctx, e, ContextInput{Extra: Object{"present": true}}, func(context.Context) (any, error) {
				if kind == "lookup" {
					return nil, expected
				}
				if kind == "cancel" {
					cancel()
				}
				return Object{"subscribeAdded": `{'title':'{{ lipsum(1) }}'}`}, nil
			})
			if kind == "lookup" && !errors.Is(err, expected) {
				t.Fatal(err)
			}
			if kind == "unsupported" && !errors.Is(err, exec.ErrUnsupported) {
				t.Fatal(err)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if result.SourceError != "" || result.Message.Get("title") != nil {
				t.Fatal("native failure became a caught source return")
			}
		})
	}
}
