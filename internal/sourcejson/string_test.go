package sourcejson_test

import (
	"encoding/json"
	"testing"

	"light-media/internal/sourcejson"
)

// The source-shaped formatter reproduces Python dict/list str() output because
// plugins and log text compare those strings. These literals are CPython 3.12
// results, written by hand rather than captured from this implementation.
func TestStringMatchesPythonStrSemantics(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		// A top-level string is the string itself, not a repr.
		{`"abc"`, "abc"},
		{"\"line\\nbreak\"", "line\nbreak"},
		// Members use repr: single quotes unless the value holds a quote only.
		{`{"k":"it's"}`, `{'k': "it's"}`},
		{`["it's","say \"hi\""]`, `["it's", 'say "hi"']`},
		// True/False/None spellings, not JSON literals.
		{`[1, 2.5, true, null]`, `[1, 2.5, True, None]`},
		{`{"a": {"b": [1, "x"]}}`, `{'a': {'b': [1, 'x']}}`},
		// Python keeps floats as floats and picks its own exponent notation.
		{`10.0`, "10.0"},
		{`1e16`, "1e+16"},
		{`0.00001`, "1e-05"},
		{`0.0001`, "0.0001"},
		// Duplicate keys keep the first position and the last value.
		{`{"a":1,"a":2}`, `{'a': 2}`},
		// Large integers stay exact instead of becoming floats.
		{`123456789012345678901234567890`, "123456789012345678901234567890"},
		// Escapes follow CPython's repr, not Go quoting.
		{`{"t":"a\tb","q":"back\\slash"}`, `{'t': 'a\tb', 'q': 'back\\slash'}`},
	}
	for _, c := range cases {
		got, err := sourcejson.String(json.RawMessage(c.raw))
		if err != nil {
			t.Errorf("String(%s) returned %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("String(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestStringRejectsTrailingContent(t *testing.T) {
	if _, err := sourcejson.String(json.RawMessage(`{"a":1} trailing`)); err == nil {
		t.Fatal("trailing content must be rejected")
	}
}
