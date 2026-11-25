package apprise

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestSourceWxPusher199(t *testing.T) {
	testSourceWxPusher199(t, "testdata/wxpusher-1.9.9.json")
}

func TestSourceWxPusherEncodedReplies199(t *testing.T) {
	testSourceWxPusher199(t, "testdata/wxpusher-encoded-replies-1.9.9.json")
}

func testSourceWxPusher199(t *testing.T, path string) {
	t.Helper()
	var fixture struct {
		Cases []struct {
			Name, URL, Title, Body string
			Attach                 any
			Added, Result          bool
			Replies                []struct {
				Status       int
				Content      string
				ContentBytes []byte `json:"content_bytes"`
			}
			Requests []attachmentWire
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing WxPusher source observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var observed []attachmentWire
			client := New(func(_ context.Context, call Request) (Response, error) {
				index := len(observed)
				if index >= len(row.Replies) {
					t.Fatal("unexpected WxPusher request", call.Method)
				}
				wire, err := prepareAttachmentWire(call, "")
				if err != nil {
					t.Error(err)
					return Response{}, err
				}
				observed = append(observed, wire)
				reply := row.Replies[index]
				if reply.Status == 0 {
					return Response{}, errors.New("owned connection failure")
				}
				content := reply.ContentBytes
				if content == nil {
					content = []byte(reply.Content)
				}
				return Response{Status: reply.Status, Content: content}, nil
			})
			if got, err := client.Add(row.URL); err != nil || got != row.Added {
				t.Fatalf("Add = %v, %v, source %v", got, err, row.Added)
			}
			if got, err := client.Notify(t.Context(), row.Title, row.Body, row.Attach); err != nil || got != row.Result {
				t.Fatalf("Notify = %v, %v, source %v", got, err, row.Result)
			}
			if len(observed) != len(row.Requests) {
				t.Fatalf("request count = %d, source %d", len(observed), len(row.Requests))
			}
			for i := range observed {
				if !reflect.DeepEqual(observed[i], row.Requests[i]) {
					t.Errorf("request %d = %#v, source %#v", i, observed[i], row.Requests[i])
				}
			}
		})
	}
}
