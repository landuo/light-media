package sitestatistic

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	plugins "light-media/builtinplugins/internal/sdk"
)

// The expected wire value comes from the actual installed/fixed source enum,
// independently captured by static AST. A translated display label is invalid
// at the ordinary host's Notification envelope boundary.
func TestSourceSiteMessageEnumWire(t *testing.T) {
	raw, err := os.ReadFile("reference/site-message-enum.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Member string `json:"member"`
		Value  string `json:"value"`
	}
	if err = json.Unmarshal(raw, &source); err != nil || source.Member != "NotificationType.SiteMessage" || source.Value == "" {
		t.Fatal("invalid independent source enum fixture", err)
	}
	calls := 0
	host := sdkHost{caller: func(_ context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != plugins.PostMessageCapability {
			t.Fatalf("unexpected host method %q", method)
		}
		var fields map[string]string
		if err = json.Unmarshal(input, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["mtype"] != source.Value || fields["title"] != "owned title" || fields["text"] != "owned body" || len(fields) != 3 {
			t.Fatalf("source notification wire mismatch: got %v, enum value %q", fields, source.Value)
		}
		return json.RawMessage(`{"queued":0,"attempted":0,"failed":0,"errors":[]}`), nil
	}}
	if err = host.Post(context.Background(), Notification{Title: "owned title", Text: "owned body"}); err != nil || calls != 1 {
		t.Fatal("ordinary notification dispatch", calls, err)
	}
}
