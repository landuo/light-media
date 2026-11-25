package larkmessager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type notificationRejectionHost struct{ Host }

func (notificationRejectionHost) ReadFile(context.Context, string) (Blob, error) {
	return Blob{Name: "fixture.txt", Data: []byte("fixture")}, nil
}

func TestOrdinaryNotificationRejectionAndOpaqueAcknowledgment(t *testing.T) {
	for _, tc := range []struct {
		name, cardResponse string
		wantOperations     []string
		wantError          error
		wantLogs           int
	}{
		{
			name: "definite rejection", cardResponse: `{"code":230001,"msg":"provider private detail"}`,
			wantOperations: []string{"interactive", "upload_file", "file"}, wantLogs: 1,
		},
		{
			name: "opaque acknowledgment", cardResponse: `{"code":"230001"}`,
			wantOperations: []string{"interactive"}, wantError: ErrUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operations := make(chan string, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/open-apis/auth/v3/tenant_access_token/internal":
					_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"fixture-token"}`)
				case "/open-apis/im/v1/files":
					operations <- "upload_file"
					_, _ = io.WriteString(w, `{"code":0,"data":{"file_key":"file_owned"}}`)
				case "/open-apis/im/v1/messages":
					var body struct {
						MessageType string `json:"msg_type"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						http.Error(w, "invalid request", http.StatusBadRequest)
						return
					}
					operations <- body.MessageType
					if body.MessageType == "interactive" {
						_, _ = io.WriteString(w, tc.cardResponse)
					} else {
						_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_file"}}`)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			peer, err := newProvider(server.URL+"/open-apis", "fixture-id", "fixture-secret")
			if err != nil {
				t.Fatal(err)
			}
			var logged []string
			plugin := New(PluginHost{
				API:        notificationRejectionHost{Host: peer},
				LogFailure: func(_ context.Context, message string) { logged = append(logged, message) },
			})
			_, err = plugin.Init(Object{"enabled": true, "app_id": "fixture-id", "app_secret": "fixture-secret", "chat_id": "oc_owned"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(t.Context(), invocationKey{}, &invocation{})
			err = plugin.Post(ctx, Notification{Title: "fixture", Image: "feishu://image/img_owned", FilePath: "fixture.txt"})
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("got %v, want %v", err, tc.wantError)
			}
			var got []string
			for len(operations) > 0 {
				got = append(got, <-operations)
			}
			if !reflect.DeepEqual(got, tc.wantOperations) {
				t.Fatalf("provider operations: got %v, want %v", got, tc.wantOperations)
			}
			if len(logged) != tc.wantLogs || len(logged) == 1 && logged[0] != "Lark 通知发送失败" {
				t.Fatalf("expected only source-shaped redacted failure logging, got %v", logged)
			}
		})
	}
}
