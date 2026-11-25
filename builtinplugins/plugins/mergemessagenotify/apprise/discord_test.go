package apprise

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSourceDiscord199(t *testing.T) {
	var fixture struct {
		Boundary string
		Cases    []struct {
			Name, URL, Title string
			Body             json.RawMessage
			Attachments      []string
			Added, Result    bool
			Waits            []float64
			Replies          []struct {
				Status  int
				Headers map[string]string
			}
			Requests []attachmentWire
		}
	}
	data, err := os.ReadFile("testdata/discord-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing fixed-source Discord observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			var body string
			if err := json.Unmarshal(row.Body, &body); err != nil {
				var repeated struct {
					Repeat string
					Count  int
				}
				if err := json.Unmarshal(row.Body, &repeated); err != nil {
					t.Fatal(err)
				}
				body = strings.Repeat(repeated.Repeat, repeated.Count)
			}
			var seen []attachmentWire
			var postTimes []time.Time
			client := New(func(_ context.Context, request Request) (Response, error) {
				wire, err := prepareAttachmentWire(request, fixture.Boundary)
				if err != nil {
					return Response{}, err
				}
				if wireBody, ok := wire.Body.(string); ok && len(wireBody) > 1200 {
					wire.Body = map[string]any{"sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(wireBody)))}
				}
				seen = append(seen, wire)
				if request.Method == http.MethodGet {
					address, err := url.Parse(request.URL)
					if err != nil {
						return Response{}, err
					}
					switch address.Path {
					case "/picture.png":
						return Response{Status: 200, Headers: map[string]string{"Content-Type": "image/png"}, Content: []byte("owned picture")}, nil
					case "/notes.txt":
						return Response{Status: 200, Headers: map[string]string{"Content-Type": "text/plain"}, Content: []byte("owned text")}, nil
					default:
						return Response{Status: 404}, nil
					}
				}
				index := len(postTimes)
				if index >= len(row.Replies) {
					t.Errorf("unexpected webhook POST: %s", wire.URL)
					return Response{}, errors.New("unexpected POST")
				}
				now := time.Now()
				postTimes = append(postTimes, now)
				reply := row.Replies[index]
				if reply.Status == 0 {
					return Response{}, errors.New("owned synthetic transport error")
				}
				headers := maps.Clone(reply.Headers)
				for key, value := range headers {
					if seconds, ok := strings.CutPrefix(value, "@clock+"); ok {
						offset, err := strconv.ParseInt(seconds, 10, 64)
						if err != nil {
							return Response{}, err
						}
						headers[key] = strconv.FormatInt(now.Unix()+offset, 10)
					}
				}
				return Response{Status: reply.Status, Headers: headers}, nil
			})
			added, err := client.Add(row.URL)
			if err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v, original %v", added, err, row.Added)
			}
			result, err := client.Notify(t.Context(), row.Title, body, row.Attachments)
			if err != nil || result != row.Result {
				t.Fatalf("Notify = %v, %v, original %v", result, err, row.Result)
			}
			if len(seen) != len(row.Requests) {
				t.Fatalf("requests = %d, original %d", len(seen), len(row.Requests))
			}
			for i, actual := range seen {
				if !reflect.DeepEqual(actual, row.Requests[i]) {
					t.Errorf("request %d = %#v, original %#v", i, actual, row.Requests[i])
				}
			}
			if len(row.Waits) > 0 {
				if len(postTimes) != len(row.Waits)+1 {
					t.Fatal("missing rate-limited POST")
				}
				for i, wait := range row.Waits {
					// Reset timestamps have whole-second precision.
					minimum := time.Duration(wait*float64(time.Second)) - time.Second
					if actual := postTimes[i+1].Sub(postTimes[i]); actual < minimum {
						t.Errorf("rate limit wait %v, original minimum %v", actual, minimum)
					}
				}
			}
		})
	}
}
