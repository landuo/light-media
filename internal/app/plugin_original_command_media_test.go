package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// This is an HTTP -> actual SDK test using the owned fixture, not a direct
// dispatcher call. Media URLs are observed but must never be requested.
func TestOriginalWebCommandMediaTextRoutingActualSDK(t *testing.T) {
	a, cookie, object := ownedCommandSDKFixture(t)
	h := a.Handler()
	enabled := callTest(t, h, "PUT", "/api/plugins/"+ownedCommandFixtureID, `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, enabled.Code, enabled.Body.Bytes())
	a.wg.Wait()
	var mediaHits atomic.Int32
	mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaHits.Add(1)
		http.Error(w, "media must not be retrieved by the command bridge", http.StatusTeapot)
	}))
	t.Cleanup(mediaServer.Close)
	ref := mediaServer.URL + "/must-not-fetch.png"
	cases := []struct {
		name  string
		media map[string]any
	}{
		{"absent", map[string]any{}},
		{"image-null", map[string]any{"image": nil}},
		{"images-null", map[string]any{"images": nil}},
		{"image-empty-array", map[string]any{"image": []any{}}},
		{"images-empty-array", map[string]any{"images": []any{}}},
		{"image-empty-string", map[string]any{"image": ""}},
		{"images-empty-string", map[string]any{"images": ""}},
		{"both-empty", map[string]any{"image": "", "images": []any{}}},
		{"image-url", map[string]any{"image": ref}},
		{"images-url", map[string]any{"images": ref}},
		{"images-list", map[string]any{"images": []any{ref}}},
		{"image-object", map[string]any{"image": map[string]any{"ref": ref}}},
		{"image-plus-images", map[string]any{"image": ref, "images": []any{ref}}},
	}
	for i, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]any{"text": "/owned_command " + test.name}
			for key, value := range test.media {
				body[key] = value
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			response := callTest(t, h, "POST", "/api/v1/message/web", string(raw), cookie, "")
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"accepted":true`) {
				t.Fatal("image metadata blocked the original command branch", response.Code, response.Body.String())
			}
			a.wg.Wait()
			got := ownedCommandRead(t, object)
			if len(got.Events) != i+1 || len(got.Ingress) != i+1 || got.Calls["not_a_method"] != 0 {
				t.Fatal("request did not traverse actual command event handlers", got)
			}
			for _, raw := range []json.RawMessage{got.Events[i], got.Ingress[i]} {
				var event struct {
					Data map[string]json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(raw, &event); err != nil {
					t.Fatal(err)
				}
				if _, exists := event.Data["image"]; exists {
					t.Fatal("image leaked into command event", string(raw))
				}
				if _, exists := event.Data["images"]; exists {
					t.Fatal("images leaked into command event", string(raw))
				}
				if string(event.Data["user"]) != `"admin"` || string(event.Data["channel"]) != `"Web"` {
					t.Fatal("trusted message identity changed", string(raw))
				}
			}
			if !strings.Contains(string(got.Events[i]), `"arg_str":"`+test.name+`"`) {
				t.Fatal("command text was lost", string(got.Events[i]))
			}
		})
	}
	unknownBody, _ := json.Marshal(map[string]any{"text": "/not_registered", "image": ref})
	unknown := callTest(t, h, "POST", "/api/message/web", string(unknownBody), cookie, "")
	if unknown.Code != http.StatusOK || !strings.Contains(unknown.Body.String(), `"accepted":false`) {
		t.Fatal("unknown slash command with image did not no-op", unknown.Code, unknown.Body.String())
	}
	a.wg.Wait()
	for _, test := range []struct {
		name        string
		text        any
		includeText bool
		status      int
	}{
		{"image-only-without-text", nil, false, 501},
		{"image-only-empty-text", "", true, 501},
		{"image-only-null-text", nil, true, 501},
		{"image-with-non-command", "ordinary message", true, 200},
		{"image-with-ordinary-unknown-command", "/ai describe", true, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]any{"image": ref}
			if test.includeText {
				body["text"] = test.text
			}
			raw, _ := json.Marshal(body)
			response := callTest(t, h, "POST", "/api/message/web", string(raw), cookie, "")
			if response.Code != test.status {
				t.Fatal(response.Code, response.Body.String())
			}
		})
	}
	a.wg.Wait()
	got := ownedCommandRead(t, object)
	if len(got.Events) != len(cases) || len(got.Ingress) != len(cases)+2 {
		t.Fatal("unknown/media-only request executed a declared command", got)
	}
	if mediaHits.Load() != 0 {
		t.Fatal("command ingress fetched media", mediaHits.Load())
	}
}
