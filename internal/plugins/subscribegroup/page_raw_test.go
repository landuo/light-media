package subscribegroup

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRawHistoryPageNormalRowsAreOriginalPage(t *testing.T) {
	history := []History{{Name: "first", Type: "source", Content: `{"include": "\\u4e2d"}`, Time: "2026-10-15"}, {Name: "second", Content: "second", Time: "2026-10-16"}, {Name: "same-time", Content: "same", Time: "2026-10-16"}}
	raw, _ := json.Marshal(history)
	actual, err := RawPage(raw)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := Page(history)
	if err != nil {
		t.Fatal(err)
	}
	expectedRaw, _ := json.Marshal(expected)
	equalJSON(t, actual, expectedRaw)
}

func TestRawHistoryPageLegacyNullFieldsAndNumericKeys(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `0`, `0.0`, `1e-999999999`, `""`, `{}`, `[]`} {
		page, err := RawPage(json.RawMessage(raw))
		if err != nil {
			t.Fatal("source falsey history rejected", raw, err)
		}
		expected, _ := Page(nil)
		encoded, _ := json.Marshal(expected)
		equalJSON(t, page, encoded)
	}
	page, err := RawPage(json.RawMessage(`{"name":17,"content":false,"unknown":{"huge":` + strings.Repeat("9", 400) + `}}`))
	if err != nil {
		t.Fatal("single legacy object rejected", err)
	}
	encoded, _ := json.Marshal(page)
	if !strings.Contains(string(encoded), `"text":17`) || strings.Count(string(encoded), `"text":null`) != 2 || !strings.Contains(string(encoded), `"text":""`) {
		t.Fatal("missing/null/scalar display fields were coerced", string(encoded))
	}
	page, err = RawPage(json.RawMessage(`[{"time":0,"name":"zero"},{"time":1e400,"name":"infinite"},{"time":` + strings.Repeat("9", 400) + `,"name":"huge-integer"},{"time":true,"name":"one"}]`))
	if err != nil {
		t.Fatal("original numeric time sorting rejected", err)
	}
	encoded, _ = json.Marshal(page)
	s := string(encoded)
	if strings.Index(s, `"text":"infinite"`) > strings.Index(s, `"text":"huge-integer"`) || strings.Index(s, `"text":"huge-integer"`) > strings.Index(s, `"text":"one"`) || strings.Index(s, `"text":"one"`) > strings.Index(s, `"text":"zero"`) {
		t.Fatal("numeric comparison changed original reverse ordering", s)
	}
}

func TestRawHistoryPageExceptionsRemainErrors(t *testing.T) {
	for _, test := range []struct{ raw, code string }{
		{`not-json`, "JSONDecodeError"}, {`"old"`, "AttributeError"}, {`[null]`, "AttributeError"},
		{`[{"time":"2026"},{}]`, "TypeError"}, {`[{"time":{"x":1}},{"time":{"x":1}}]`, "TypeError"},
		{`{"content":true}`, "AttributeError"}, {`{"content":"\\u123"}`, "UnicodeDecodeError"},
	} {
		if page, err := RawPage(json.RawMessage(test.raw)); err == nil || err.Error() != test.code || page != nil {
			t.Fatal("source page exception hidden", test.raw, page, err)
		}
	}
}
