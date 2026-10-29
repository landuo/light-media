package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The 24,019-byte case is pinned source review case
// html_escaped_payload_under_64k. Its source outcome is write -> reload ->
// notice -> disabled configuration, with movie classification "Normal".
// These are actual standard worker + App/SQLite tests, not marshal-only checks.
func TestCategoryEditorStandardHTMLWireExpansion(t *testing.T) {
	categoryEditorStandardSourceReferences(t)
	for _, sample := range []struct {
		name, content string
		commentOnly   bool
	}{
		{"source_24019_bytes", "#" + strings.Repeat("<", 24000) + "\nmovie:\n  Normal:\n", false},
		{"all_html_characters", "#" + strings.Repeat("<>&", 8000) + "\nmovie:\n  Normal:\n", false},
		{"literal_json_escapes", "#" + strings.Repeat("<", 24000) + `\u003c\u003e\u0026` + "\nmovie:\n  Normal:\n", false},
		{"policy_byte_limit", "#" + strings.Repeat("<", maxCategoryPolicyBytes-19) + "\nmovie:\n  Normal:\n", false},
		// Independent source-first replay: a full-size comment (or caught
		// YAML parser failure for controls) retains warm maps, cold stays empty.
		{"source_quote_byte_limit", "#" + strings.Repeat(`"`, maxCategoryPolicyBytes-1), true},
		{"source_backslash_byte_limit", "#" + strings.Repeat(`\`, maxCategoryPolicyBytes-1), true},
		{"source_control_byte_limit", "#" + strings.Repeat("\x01", maxCategoryPolicyBytes-1), true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			warm := categoryEditorStandardWarm
			f := newCategoryEditorStandardFixture(t, &warm)
			f.categories(t, "缓存电影", "缓存剧集")
			f.save(t, categoryEditorStandardID, map[string]any{"enabled": true, "content": sample.content, "discard": true})
			f.bytes(t, sample.content)
			before := f.disabled(t, categoryEditorStandardID, sample.content)
			f.notification(t)
			if sample.commentOnly {
				f.categories(t, "缓存电影", "缓存剧集")
			} else {
				f.categories(t, "Normal", "")
			}
			f.coldReopen(t)
			f.bytes(t, sample.content)
			if after := f.disabled(t, categoryEditorStandardID, sample.content); after.Revision != before.Revision {
				t.Fatal("cold disabled init rewrote configuration")
			}
			if sample.commentOnly {
				f.categories(t, "", "")
			} else {
				f.categories(t, "Normal", "")
			}
			f.noNotification(t)
			t.Logf("policy_bytes=%d; actual save/write/reload/notice/disable/persist/cold-read passed", len(sample.content))
		})
	}
}

func TestCategoryEditorStandardHTMLWireStillRejectsOversizePolicy(t *testing.T) {
	warm := categoryEditorStandardWarm
	f := newCategoryEditorStandardFixture(t, &warm)
	f.categories(t, "缓存电影", "缓存剧集")
	content := "#" + strings.Repeat("<", maxCategoryPolicyBytes-18) + "\nmovie:\n  Normal:\n"
	if len(content) != maxCategoryPolicyBytes+1 {
		t.Fatal("incorrect boundary fixture")
	}
	config := map[string]any{"enabled": true, "content": content}
	raw, _ := json.Marshal(config)
	response := callTest(t, f.app.Handler(), "PUT", "/api/plugins/CategoryEditor", string(raw), f.cookie, "")
	f.app.wg.Wait()
	if response.Code < 400 {
		t.Fatal("oversize policy bypassed unchanged 64 KiB guard", response.Code)
	}
	f.bytes(t, warm)
	f.config(t, categoryEditorStandardID, config)
	f.categories(t, "缓存电影", "缓存剧集")
	f.noNotification(t)
	var state json.RawMessage
	if err := f.object(t, categoryEditorStandardID).readSession(context.Background(), "get_state", &state); err != nil || string(state) != "true" {
		t.Fatal("oversize failure changed source enabled state", string(state), err)
	}
	// The generic resource decoder's encoded-byte guard is unchanged, too.
	var value any
	if err := originalResourceDecode(json.RawMessage(`{"x":"`+strings.Repeat("x", 128<<10)+`"}`), &value); err == nil {
		t.Fatal("generic 128 KiB resource guard was loosened")
	}
}
