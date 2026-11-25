package apprise

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestSourcePushBullet199(t *testing.T) {
	testSourcePushBullet199(t, "testdata/pushbullet-1.9.9.json")
}

func TestSourcePushBulletBoundary199(t *testing.T) {
	testSourcePushBullet199(t, "testdata/pushbullet-boundary-1.9.9.json")
}

func testSourcePushBullet199(t *testing.T, path string) {
	t.Helper()
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
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing pinned Pushbullet source observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var wire []attachmentWire
			var lastPost time.Time
			posted := 0
			client := New(func(_ context.Context, call Request) (Response, error) {
				observed, err := prepareAttachmentWire(call, fixture.Boundary)
				if err != nil {
					t.Error(err)
					return Response{}, err
				}
				wire = append(wire, observed)
				if call.Method == "GET" {
					path, err := url.Parse(call.URL)
					if err != nil {
						return Response{}, err
					}
					switch path.Path {
					case "/a.png":
						return Response{Status: 200, Headers: map[string]string{"Content-Type": "image/png"}, Content: []byte("owned-image")}, nil
					case "/b.txt":
						return Response{Status: 200, Headers: map[string]string{"Content-Type": "text/plain"}, Content: []byte("owned-text")}, nil
					default:
						return Response{Status: 404}, nil
					}
				}
				now := time.Now()
				if posted >= len(row.Replies) {
					t.Fatal("unexpected Pushbullet POST", call.URL)
				}
				if posted > 0 {
					minimum := time.Duration(row.Waits[posted-1] * float64(time.Second))
					if got := now.Sub(lastPost); got+10*time.Millisecond < minimum {
						t.Errorf("POST interval %s, source %s", got, minimum)
					}
				}
				lastPost = now
				reply := row.Replies[posted]
				posted++
				return Response{Status: reply.Status, Content: []byte(reply.Body)}, nil
			})
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add() = %v, %v, source %v", added, err, row.Added)
			}
			got, err := client.Notify(t.Context(), row.Title, row.Body, row.Attachments)
			if err != nil || got != row.Result {
				t.Fatalf("Notify() = %v, %v, source %v", got, err, row.Result)
			}
			if len(wire) != len(row.Requests) {
				t.Fatalf("sent %d requests, source %d", len(wire), len(row.Requests))
			}
			for i, call := range wire {
				if !reflect.DeepEqual(call, row.Requests[i]) {
					t.Errorf("request %d: got %#v, source %#v", i, call, row.Requests[i])
				}
			}
		})
	}
}
