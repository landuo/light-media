package sites

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestSourceUserDataArbitraryMessageRowsRoundTrip(t *testing.T) {
	for _, raw := range []string{
		`[["head","date","body","source",{"extra":9007199254740993}],null,9007199254740993,"abcd",{"head":"value"},true,["last","date","body"]]`,
		`[null]`,
		`[9007199254740993]`,
		`["abcd"]`,
		`[{"head":"value"}]`,
		`[[]]`,
		`[]`,
		`null`,
	} {
		t.Run(raw, func(t *testing.T) {
			var want []any
			if !decodeJSON([]byte(raw), &want) {
				t.Fatal("bad fixture")
			}
			var dto SourceUserData
			if err := json.Unmarshal([]byte(`{"message_unread_contents":`+raw+`}`), &dto); err != nil {
				t.Fatalf("schema-permitted row rejected: %v", err)
			}
			assertSourceRowsEqual(t, dto.SourceMessageRows(), want)
			encoded, err := json.Marshal(dto)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Rows []any `json:"message_unread_contents"`
			}
			if !decodeJSON(encoded, &wire) || !reflect.DeepEqual(wire.Rows, want) {
				t.Fatalf("serialized rows changed: %s", encoded)
			}
			var reopened SourceUserData
			if err := json.Unmarshal(encoded, &reopened); err != nil {
				t.Fatal(err)
			}
			assertSourceRowsEqual(t, reopened.SourceMessageRows(), want)
		})
	}
}

func TestSourceUserDataMessageRowsTypedCompatibility(t *testing.T) {
	dto := SourceUserData{Messages: [][]any{{"head", nil, "body"}}}
	rows := dto.SourceMessageRows()
	if !reflect.DeepEqual(rows, []any{[]any{"head", nil, "body"}}) {
		t.Fatalf("typed parser values changed: %#v", rows)
	}
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var reopened SourceUserData
	if err := json.Unmarshal(encoded, &reopened); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dto.Messages, reopened.Messages) || reopened.rawMessageRows == nil {
		t.Fatalf("ordinary parser row compatibility lost: %#v", reopened)
	}
}

func TestSourceUserDataMessageRowsRejectNonListContainer(t *testing.T) {
	for _, raw := range []string{`"text"`, `123`, `{}`, `true`} {
		var dto SourceUserData
		if err := json.Unmarshal([]byte(`{"message_unread_contents":`+raw+`}`), &dto); err == nil {
			t.Fatalf("non-list container accepted: %s", raw)
		}
	}
}

func assertSourceRowsEqual(t *testing.T, rows []any, want []any) {
	t.Helper()
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []any
	if !decodeJSON(encoded, &decoded) || !reflect.DeepEqual(decoded, want) {
		t.Fatalf("rows changed: %s != %#v", encoded, want)
	}
}

func TestSourceUserDataObjectRowOrderPreserved(t *testing.T) {
	const row = `{"z":"last","a":"first","m":9007199254740993,"d":null}`
	const input = `{"message_unread_contents":[["head","date","body"],` + row + `]}`
	var dto SourceUserData
	if err := json.Unmarshal([]byte(input), &dto); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		rows := dto.SourceMessageRows()
		if len(rows) != 2 {
			t.Fatalf("rows lost: %#v", rows)
		}
		raw, ok := rows[1].(json.RawMessage)
		if !ok || string(raw) != row {
			t.Fatalf("ordered object row changed: %s", raw)
		}
		encoded, err := json.Marshal(dto)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(encoded, []byte(row)) {
			t.Fatalf("object order changed: %s", encoded)
		}
		if err := json.Unmarshal(encoded, &dto); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSourceUserDataNestedObjectMessageOrderPreserved(t *testing.T) {
	const row = `[{"z":"head","a":"first"},"date",{"z":{"y":9007199254740993,"b":false},"a":[{"q":null,"c":true}]},"source",{"x":3,"b":2}]`
	const input = `{"message_unread_contents":[` + row + `]}`
	var dto SourceUserData
	if err := json.Unmarshal([]byte(input), &dto); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if len(dto.Messages) != 1 || len(dto.Messages[0]) != 5 {
			t.Fatalf("typed compatibility view lost: %#v", dto.Messages)
		}
		rows := dto.SourceMessageRows()
		if len(rows) != 1 {
			t.Fatalf("rows lost: %#v", rows)
		}
		raw, ok := rows[0].(json.RawMessage)
		if !ok || string(raw) != row {
			t.Fatalf("nested object order changed: %s", raw)
		}
		encoded, err := json.Marshal(dto)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(encoded, []byte(row)) {
			t.Fatalf("nested object encoding changed: %s", encoded)
		}
		if err := json.Unmarshal(encoded, &dto); err != nil {
			t.Fatal(err)
		}
	}
}
