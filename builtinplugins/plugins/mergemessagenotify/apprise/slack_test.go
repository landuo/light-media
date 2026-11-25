package apprise

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"testing"
	"time"
)

func TestSourceSlack199(t *testing.T) {
	var fixture struct {
		Boundary string
		Cases    []struct {
			Name, URL, Title, Body string
			Attachments            []string
			Added                  bool
			Results                []bool
			Waits                  []float64
			Replies                []struct {
				Status int
				Body   string
			}
			Requests []attachmentWire
		}
	}
	raw, err := os.ReadFile("testdata/slack-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing fixed-source Slack observations", err)
	}
	timestamp := regexp.MustCompile(`("ts": )[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?`)
	normalize := func(wire attachmentWire) attachmentWire {
		if body, ok := wire.Body.(string); ok {
			wire.Body = timestamp.ReplaceAllString(body, "${1}@time")
		}
		return wire
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var seen []attachmentWire
			var deliveryTimes []time.Time
			client := New(func(_ context.Context, request Request) (Response, error) {
				wire, err := prepareAttachmentWire(request, fixture.Boundary)
				if err != nil {
					return Response{}, err
				}
				seen = append(seen, normalize(wire))
				address, err := url.Parse(request.URL)
				if err != nil {
					return Response{}, err
				}
				if address.Hostname() == "asset.invalid" {
					switch address.Path {
					case "/a.png":
						return Response{Status: 200, Headers: map[string]string{"Content-Type": "image/png"}, Content: []byte("owned image")}, nil
					case "/b.txt":
						return Response{Status: 200, Headers: map[string]string{"Content-Type": "text/plain"}, Content: []byte("owned text")}, nil
					default:
						return Response{Status: 404}, nil
					}
				}
				index := len(deliveryTimes)
				if index >= len(row.Replies) {
					t.Errorf("unexpected Slack request to %s", address.Path)
					return Response{}, errors.New("unexpected Slack request")
				}
				deliveryTimes = append(deliveryTimes, time.Now())
				reply := row.Replies[index]
				if reply.Status == 0 {
					return Response{}, errors.New("owned synthetic transport error")
				}
				return Response{Status: reply.Status, Content: []byte(reply.Body)}, nil
			})
			if added, err := client.Add(row.URL); err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, original %v", added, err, row.Added)
			}
			for i, want := range row.Results {
				if actual, err := client.Notify(t.Context(), row.Title, row.Body, row.Attachments); err != nil || actual != want {
					t.Errorf("Notify %d = %v, %v, original %v", i, actual, err, want)
				}
			}
			if len(seen) != len(row.Requests) {
				t.Fatalf("requests = %d, original %d", len(seen), len(row.Requests))
			}
			for i, actual := range seen {
				want := normalize(row.Requests[i])
				if !reflect.DeepEqual(actual, want) {
					t.Errorf("request %d = %#v, original %#v", i, actual, want)
				}
			}
			if max(len(deliveryTimes)-1, 0) != len(row.Waits) {
				t.Fatal("delivery intervals differ from original")
			}
			for i, wait := range row.Waits {
				minimum := time.Duration(wait*float64(time.Second)) - 20*time.Millisecond
				if elapsed := deliveryTimes[i+1].Sub(deliveryTimes[i]); elapsed < minimum {
					t.Errorf("delivery interval %v, original minimum %v", elapsed, minimum)
				}
			}
		})
	}
}
