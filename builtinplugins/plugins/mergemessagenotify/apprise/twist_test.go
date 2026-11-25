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

func TestSourceTwist199(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name, URL, Title, Body string
			Attach                 any
			Added                  bool
			Results                []bool
			Requests               []struct {
				attachmentWire
				SourceClock float64 `json:"source_clock"`
			}
			Replies []struct {
				Status int
				Body   string
			}
		}
	}
	raw, err := os.ReadFile("testdata/twist-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing Twist source observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			var last time.Time
			client := New(func(_ context.Context, call Request) (Response, error) {
				now := time.Now()
				i := calls
				calls++
				if i >= len(row.Replies) || i >= len(row.Requests) {
					t.Fatalf("unexpected request %s", call.URL)
				}
				observed, err := prepareAttachmentWire(call, "")
				if err != nil {
					t.Fatal(err)
				}
				want := row.Requests[i]
				if !reflect.DeepEqual(observed, want.attachmentWire) {
					t.Errorf("request %d = %#v, source %#v", i, observed, want.attachmentWire)
				}
				if i > 0 {
					delta := want.SourceClock - row.Requests[i-1].SourceClock
					elapsed := now.Sub(last)
					if delta > 0 && elapsed < 180*time.Millisecond || delta == 0 && elapsed >= 190*time.Millisecond {
						t.Errorf("request %d interval %s differs from source %.3fs", i, elapsed, delta)
					}
				}
				last = now
				reply := row.Replies[i]
				if reply.Status == 0 {
					return Response{}, errors.New("owned connection failure")
				}
				return Response{Status: reply.Status, Content: []byte(reply.Body)}, nil
			})
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, source %v", added, err, row.Added)
			}
			for i, want := range row.Results {
				if sent, err := client.Notify(t.Context(), row.Title, row.Body, row.Attach); err != nil || sent != want {
					t.Errorf("Notify %d = %v, %v, source %v", i, sent, err, want)
				}
			}
			client.Close(t.Context())
			if calls != len(row.Requests) {
				t.Fatalf("request count = %d, source %d", calls, len(row.Requests))
			}
		})
	}
}
