package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestGenericCategoryFieldsFollowOriginalValueSemantics(t *testing.T) {
	c := New(Credentials{APIKey: "synthetic-private-key"})
	for _, tc := range []struct {
		raw      string
		present  bool
		values   []string
		integers []string
	}{
		{`"Released"`, true, []string{"Released"}, nil},
		{`" leading "`, true, []string{" leading "}, nil},
		{`0`, false, nil, nil},
		{`"0"`, true, []string{"0"}, []string{"0"}},
		{`"001"`, true, []string{"001"}, nil},
		{`123456789012345678901234567890`, true, []string{"123456789012345678901234567890"}, []string{"123456789012345678901234567890"}},
		{`1000000.0`, true, []string{"1000000.0"}, nil},
		{`1e16`, true, []string{"1e+16"}, nil},
		{`0.00001`, true, []string{"1e-05"}, nil},
		{`true`, true, []string{"True"}, nil},
		{`false`, false, nil, nil},
		{`null`, false, nil, nil},
		{`[]`, false, nil, nil},
		{`{}`, false, nil, nil},
		{`[false,null,"ß",7]`, true, []string{"False", "None", "SS", "7"}, []string{"7"}},
		{`{"name":"合成","id":9}`, true, []string{"{'name': '合成', 'id': 9}"}, nil},
		{`[{"id":9,"name":"O'Brien"},["x",false]]`, true, []string{`{'id': 9, 'name': "O'Brien"}`, "['x', False]"}, nil},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			field, err := c.categoryField(json.RawMessage(tc.raw))
			if err != nil || field.Present != tc.present || len(field.Hashes) != len(tc.values) || strings.Join(field.Integers, ",") != strings.Join(tc.integers, ",") {
				t.Fatal(field, err)
			}
			for i, value := range tc.values {
				if field.Hashes[i] != CategoryValueHash(value) {
					t.Fatalf("original comparison changed for %s; expected str/repr %q", tc.raw, value)
				}
			}
		})
	}
}

func TestGenericCategoryFieldsPersistRequestedAbsentAndRedactCredentials(t *testing.T) {
	body := categoryDetailFixture("movie")
	body["budget"] = 30000000
	body["runtime"] = 120
	body["status"] = "Released"
	body["homepage"] = "https://example.invalid/synthetic-secret"
	c := categoryDetailClient(t, body)
	media, err := c.DetailForCategory(context.Background(), "movie", 123, []string{"budget", "runtime", "status", "homepage", "not_available"})
	if err != nil || !media.CategoryFields["budget"].Present || media.CategoryFields["budget"].Integers[0] != "30000000" {
		t.Fatal(media.CategoryFields, err)
	}
	if absent, known := media.CategoryFields["not_available"]; !known || absent.Present {
		t.Fatal("missing TMDB property was not remembered")
	}
	encoded, err := json.Marshal(media)
	if err != nil || strings.Contains(string(encoded), "synthetic-secret") || strings.Contains(string(encoded), "example.invalid") {
		t.Fatal("raw provider field or credential was cached", err)
	}
	var cached Media
	if err := json.Unmarshal(encoded, &cached); err != nil || len(cached.CategoryFields) != 5 {
		t.Fatal("generic category comparisons were lost in cache", err)
	}
	for _, field := range cached.CategoryFields {
		for _, hash := range field.Hashes {
			if len(hash) != 64 {
				t.Fatal("unbounded comparison payload persisted")
			}
		}
	}
}

func TestCategoryPythonRepresentationRejectsDepthAndPreservesUnicodePrintability(t *testing.T) {
	c := New(Credentials{})
	if _, err := c.categoryField(json.RawMessage(strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40))); err == nil {
		t.Fatal("unbounded nested field accepted")
	}
	if got := pythonCategoryString("a\u00a0b\n"); got != `'a\xa0b\n'` {
		t.Fatal("non-printable repr changed", got)
	}
}

func TestCategoryNumericCredentialEchoCannotEnterCachedIntegers(t *testing.T) {
	c := New(Credentials{APIKey: "123456"})
	field, err := c.categoryField(json.RawMessage(`123456`))
	if err != nil || len(field.Integers) != 0 || len(field.Hashes) != 1 || field.Hashes[0] != CategoryValueHash("[redacted]") {
		t.Fatal("numeric credential echo escaped comparison sanitization", err)
	}
}
