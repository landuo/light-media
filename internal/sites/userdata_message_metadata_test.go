package sites

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSourceUserDataMessageMetadataRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		widths    []int
	}{
		{"legacy", `{"message_unread_contents":[["heading","date","body"],[null,null,null]]}`, []int{3, 3}},
		{"source_and_extras", `{"message_unread_contents":[["heading","date","body","source-id",{"extra":9007199254740993},false],["h","d","c",null]]}`, []int{6, 4}},
		{"numeric_source", `{"message_unread_contents":[["heading","date","body",9007199254740993]]}`, []int{4}},
		{"malformed_preserved", `{"message_unread_contents":[[],["heading","date"]]}`, []int{0, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dto SourceUserData
			if err := json.Unmarshal([]byte(tc.raw), &dto); err != nil {
				t.Fatal(err)
			}
			for i, width := range tc.widths {
				if len(dto.Messages[i]) != width {
					t.Fatalf("row %d lost metadata: %#v", i, dto.Messages[i])
				}
			}
			encoded, err := json.Marshal(dto)
			if err != nil {
				t.Fatal(err)
			}
			var reopened SourceUserData
			if err = json.Unmarshal(encoded, &reopened); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(dto.Messages, reopened.Messages) {
				t.Fatalf("roundtrip changed message data: %#v -> %#v", dto.Messages, reopened.Messages)
			}
			var original struct {
				Messages [][]any `json:"message_unread_contents"`
			}
			if !decodeJSON([]byte(tc.raw), &original) {
				t.Fatal("fixture invalid")
			}
			if !reflect.DeepEqual(original.Messages, reopened.Messages) {
				t.Fatalf("source message data changed: %#v -> %#v", original.Messages, reopened.Messages)
			}
		})
	}
}

func TestSourceUserDataLegacyMessageJSONShape(t *testing.T) {
	dto := emptySourceUserData()
	dto.Messages = append(dto.Messages, []any{"heading", nil, "body"})
	raw, err := json.Marshal(dto.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `[["heading",null,"body"]]` {
		t.Fatalf("legacy shape changed: %s", raw)
	}
	dto = emptySourceUserData()
	raw, err = json.Marshal(dto.Messages)
	if err != nil || string(raw) != "[]" {
		t.Fatalf("empty default changed: %s %v", raw, err)
	}
}
