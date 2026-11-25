package apprise

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestSourcePushy199(t *testing.T) {
	data, err := os.ReadFile("testdata/pushy-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, URL, Title, Body string
			Attach                 any
			Added, Result          bool
			Requests               []attachmentWire
			Waits                  []float64
			Replies                []struct {
				Status       int
				Content      string
				ContentBytes []byte `json:"content_bytes"`
			}
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatalf("invalid Pushy source observations: %v", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			seen := 0
			var last time.Time
			client := New(func(_ context.Context, request Request) (Response, error) {
				index := seen
				seen++
				if index >= len(row.Replies) || index >= len(row.Requests) {
					t.Fatalf("unexpected Pushy request %d", index)
				}
				now := time.Now()
				if index > 0 && index-1 < len(row.Waits) &&
					now.Sub(last)+20*time.Millisecond < time.Duration(row.Waits[index-1]*float64(time.Second)) {
					t.Errorf("request %d did not honor the source interval", index)
				}
				last = now
				wire, err := prepareAttachmentWire(request, "")
				if err != nil || !reflect.DeepEqual(wire, row.Requests[index]) {
					t.Errorf("request %d = %#v, %v, source %#v", index, wire, err, row.Requests[index])
				}
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
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, source %v", added, err, row.Added)
			}
			if sent, err := client.Notify(t.Context(), row.Title, row.Body, row.Attach); err != nil || sent != row.Result {
				t.Fatalf("Notify = %v, %v, source %v", sent, err, row.Result)
			}
			if seen != len(row.Requests) {
				t.Fatalf("request count = %d, source %d", seen, len(row.Requests))
			}
		})
	}
}
