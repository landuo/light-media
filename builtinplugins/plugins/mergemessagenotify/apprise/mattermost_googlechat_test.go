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

func TestSourceMattermostAndGoogleChat199(t *testing.T) {
	testSourceTitlelessProviders199(t, "testdata/mattermost-googlechat-1.9.9.json")
}

func TestSourceTitlelessBoundary199(t *testing.T) {
	testSourceTitlelessProviders199(t, "testdata/titleless-boundary-1.9.9.json")
}

func testSourceTitlelessProviders199(t *testing.T, path string) {
	t.Helper()
	var fixture struct {
		Cases []struct {
			Name, URL, Title, Body string
			Attach                 any
			Added                  bool
			Results                []bool
			Requests               []attachmentWire
			Waits                  []float64
			Replies                []struct {
				Status int
				Body   string
			}
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing Mattermost/Google Chat source observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var wire []attachmentWire
			var last time.Time
			client := New(func(_ context.Context, call Request) (Response, error) {
				i := len(wire)
				if i >= len(row.Replies) {
					t.Fatal("unexpected provider request", call.Method)
				}
				got, err := prepareAttachmentWire(call, "")
				if err != nil {
					t.Error(err)
					return Response{}, err
				}
				now := time.Now()
				if i > 0 && now.Sub(last)+2*time.Millisecond < time.Duration(row.Waits[i-1]*float64(time.Second)) {
					t.Errorf("request %d did not honor the source interval", i)
				}
				last = now
				wire = append(wire, got)
				reply := row.Replies[i]
				if reply.Status == 0 {
					return Response{}, errors.New("owned connection failure")
				}
				return Response{Status: reply.Status, Content: []byte(reply.Body)}, nil
			})
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, source %v", added, err, row.Added)
			}
			for _, want := range row.Results {
				if sent, err := client.Notify(t.Context(), row.Title, row.Body, row.Attach); err != nil || sent != want {
					t.Fatalf("Notify = %v, %v, source %v", sent, err, want)
				}
			}
			if len(wire) != len(row.Requests) {
				t.Fatalf("request count = %d, source %d", len(wire), len(row.Requests))
			}
			for i := range wire {
				if !reflect.DeepEqual(wire[i], row.Requests[i]) {
					t.Errorf("request %d = %#v, source %#v", i, wire[i], row.Requests[i])
				}
			}
		})
	}
}
