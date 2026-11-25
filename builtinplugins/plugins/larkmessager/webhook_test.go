package larkmessager

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

func TestConnectionCardWebhookAcknowledgesFailedReply(t *testing.T) {
	replies := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"fixture-token"}`)
		case "/open-apis/im/v1/messages/om_test/reply":
			replies <- r.Method
			http.Error(w, "no provider response", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	peer, err := newProvider(server.URL+"/open-apis", "fixture-id", "fixture-secret")
	if err != nil {
		t.Fatal(err)
	}
	var forwarded, logged int
	plugin := New(PluginHost{
		API: peer,
		Forward: func(_ context.Context, payload Object) error {
			forwarded++
			if payload["callback_data"] != "test_ok" {
				t.Fatal("unexpected forwarded callback")
			}
			return ErrDependency
		},
		LogFailure: func(context.Context, string) { logged++ },
	})
	raw, err := json.Marshal(sdk.HTTPRequest{
		Key: "webhook", Method: "POST",
		Body: []byte(`{"header":{"event_type":"card.action.trigger"},"event":{"context":{"open_message_id":"om_test"},"action":{"value":{"callback_data":"test_ok"}}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	acknowledge := func() {
		t.Helper()
		ctx := context.WithValue(t.Context(), invocationKey{}, &invocation{})
		out, err := plugin.serveHTTP(ctx, raw)
		var response sdk.HTTPResponse
		if err != nil || json.Unmarshal(out, &response) != nil || response.Status != 200 ||
			string(response.Body) != `{"success":true}` {
			t.Fatalf("source acknowledges the callback even after an internal failure: %s, %v", out, err)
		}
	}
	if _, err := plugin.Init(Object{"enabled": true, "app_id": "fixture-id", "app_secret": "fixture-secret"}); err != nil {
		t.Fatal(err)
	}
	acknowledge()
	if len(replies) != 1 || <-replies != http.MethodPost || forwarded != 0 || logged != 1 {
		t.Fatal("enabled callback must attempt one direct reply, acknowledge and log the failure")
	}
	plugin.Stop()
	acknowledge()
	if len(replies) != 0 || forwarded != 1 || logged != 2 {
		t.Fatal("without a client the callback must go through normal forwarding and acknowledge")
	}
}
