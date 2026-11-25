package apprise

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestSourcePushover199(t *testing.T) {
	var fixture struct {
		Boundary string
		Cases    []struct {
			Name, URL, Title, Body string
			Added, Result          bool
			Attachments            []string
			Waits                  []float64
			Replies                []struct {
				Status int
				Body   string
			}
			Requests []attachmentWire
		}
	}
	data, err := os.ReadFile("testdata/pushover-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing pinned Pushover source observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var wire []attachmentWire
			var postedAt []time.Time
			client := New(func(_ context.Context, req Request) (Response, error) {
				item, err := prepareAttachmentWire(req, fixture.Boundary)
				if err != nil {
					t.Error(err)
					return Response{}, err
				}
				wire = append(wire, item)
				if req.Method == http.MethodGet {
					return pushoverAttachmentFixture(req.URL)
				}
				if len(postedAt) >= len(row.Replies) {
					t.Fatal("unexpected Pushover POST", req.URL)
				}
				reply := row.Replies[len(postedAt)]
				postedAt = append(postedAt, time.Now())
				return Response{Status: reply.Status, Content: []byte(reply.Body)}, nil
			})
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, source %v", added, err, row.Added)
			}
			if sent, err := client.Notify(t.Context(), row.Title, row.Body, row.Attachments); err != nil || sent != row.Result {
				t.Fatalf("Notify = %v, %v, source %v", sent, err, row.Result)
			}
			if len(wire) != len(row.Requests) {
				t.Fatalf("made %d requests, source %d", len(wire), len(row.Requests))
			}
			for i, call := range wire {
				if !reflect.DeepEqual(call, row.Requests[i]) {
					t.Errorf("request %d: got %#v, source %#v", i, call, row.Requests[i])
				}
			}
			if max(0, len(postedAt)-1) != len(row.Waits) {
				t.Fatalf("POST intervals = %d, source %d", max(0, len(postedAt)-1), len(row.Waits))
			}
			for i, wait := range row.Waits {
				minimum := time.Duration(wait*float64(time.Second)) - 20*time.Millisecond
				if elapsed := postedAt[i+1].Sub(postedAt[i]); elapsed < minimum {
					t.Fatalf("POST interval = %v, want at least %v", elapsed, minimum)
				}
			}
		})
	}
}

func pushoverAttachmentFixture(rawURL string) (Response, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return Response{}, err
	}
	image := map[string]string{"Content-Type": "image/png"}
	switch parsed.Path {
	case "/image.png":
		return Response{Status: http.StatusOK, Headers: image, Content: []byte("owned-image")}, nil
	case "/readme.txt":
		return Response{Status: http.StatusOK, Headers: map[string]string{"Content-Type": "text/plain"}, Content: []byte("owned-text")}, nil
	case "/empty.png":
		return Response{Status: http.StatusOK, Headers: image}, nil
	case "/oversized.png":
		return Response{Status: http.StatusOK, Headers: image, Content: bytes.Repeat([]byte("x"), 5242881)}, nil
	default:
		return Response{Status: http.StatusNotFound}, nil
	}
}
