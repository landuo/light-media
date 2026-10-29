package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCategoryEditorApplyWireBoundIsSpecificAndStrict(t *testing.T) {
	for _, value := range []string{"<", ">", "&", `"`, `\`, "\x01"} {
		t.Run(value, func(t *testing.T) {
			content := "#" + strings.Repeat(value, maxCategoryPolicyBytes-1)
			raw, err := json.Marshal(map[string]string{"content": content})
			if err != nil || len(raw) <= 128<<10 {
				t.Fatal("fixture must exceed generic resource encoded bound", len(raw), err)
			}
			var decoded categoryEditorApplyInput
			if err := categoryEditorApplyDecode(raw, &decoded); err != nil {
				t.Fatal("policy's legal JSON expansion was rejected", err)
			}
			var got string
			if err := json.Unmarshal(decoded.Content, &got); err != nil || got != content {
				t.Fatal("resource changed content", err)
			}
			if err := originalResourceDecode(raw, &decoded); err == nil {
				t.Fatal("generic 128 KiB encoded bound was loosened")
			}
		})
	}
	for _, raw := range []json.RawMessage{
		nil, json.RawMessage(`null`), json.RawMessage(`[]`),
		json.RawMessage(`{"content":"x","extra":true}`),
		json.RawMessage(`{"content":"x"} null`),
		json.RawMessage(`{"content":"x"} !`),
		json.RawMessage(`{"content":"\q"}`),
		json.RawMessage(strings.Repeat(" ", maxCategoryApplyJSONBytes-1) + `{}`),
	} {
		var decoded categoryEditorApplyInput
		if err := categoryEditorApplyDecode(raw, &decoded); err == nil {
			t.Fatalf("accepted invalid or oversized %d-byte resource", len(raw))
		}
	}
	// The JSON syntax ceiling is independent of decoded content validation,
	// which still happens before opening/truncating the file in the resource.
	var decoded categoryEditorApplyInput
	boundary := json.RawMessage(strings.Repeat(" ", maxCategoryApplyJSONBytes-2) + `{}`)
	if err := categoryEditorApplyDecode(boundary, &decoded); err != nil {
		t.Fatal("exact encoded ceiling rejected", err)
	}
	// Keep original strict decoder semantics: duplicate recognized fields use
	// the last value, non-string values reach the existing type guard, and raw
	// lone surrogates remain available to the post-truncate UTF-8 failure check.
	for _, sample := range []struct{ raw, want string }{
		{`{"content":"first","content":"last"}`, `"last"`},
		{`{"content":123456789012345678901234567890}`, `123456789012345678901234567890`},
		{`{"content":{"x":"<"}}`, `{"x":"<"}`},
		{`{"content":"\ud800<>&\\u003c\"\\"}`, `"\ud800<>&\\u003c\"\\"`},
	} {
		if err := categoryEditorApplyDecode(json.RawMessage(sample.raw), &decoded); err != nil || !bytes.Equal(decoded.Content, []byte(sample.want)) {
			t.Fatalf("changed original raw semantics: %s => %s: %v", sample.raw, decoded.Content, err)
		}
	}
}
