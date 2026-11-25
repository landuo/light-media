package apprise

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestSourcePushSafer199(t *testing.T) {
	type observation struct {
		Method, URL string
		Headers     map[string]string
		Body        string
		Verify      bool
		Timeout     [2]float64
	}
	var fixture struct {
		Cases []struct {
			Name, URL, Title, Body string
			Attachments            []string
			Replies                [][2]json.RawMessage
			Waits                  []float64
			Added, Result          bool
			Requests               []observation
		}
	}
	raw, err := os.ReadFile("testdata/pushsafer-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing original PushSafer observations", err)
	}
	assets := map[string]struct{ mime, content string }{
		"/a.png":     {"image/png", "owned png"},
		"/b.jpg":     {"image/jpeg", "owned jpg"},
		"/c.gif":     {"image/gif", "owned gif"},
		"/d.png":     {"image/png", "owned four"},
		"/owned.txt": {"text/plain", "owned text"},
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			observed := []observation{}
			var sentAt []time.Time
			client := New(func(_ context.Context, request Request) (Response, error) {
				if len(request.Auth) != 0 {
					t.Fatal("PushSafer must not send URL userinfo")
				}
				observed = append(observed, observation{
					Method: request.Method, URL: request.URL, Headers: request.Headers,
					Body: string(request.Data), Verify: request.Verify, Timeout: request.Timeout,
				})
				if request.Method == "GET" {
					target, err := url.Parse(request.URL)
					if err != nil {
						t.Fatal(err)
					}
					if asset, ok := assets[target.Path]; ok {
						return Response{Status: 200, Headers: map[string]string{"Content-Type": asset.mime}, Content: []byte(asset.content)}, nil
					}
					return Response{Status: 404}, nil
				}
				post := len(sentAt)
				sentAt = append(sentAt, time.Now())
				if post >= len(row.Replies) {
					t.Fatal("unexpected PushSafer POST", post)
				}
				var status int
				var body string
				if json.Unmarshal(row.Replies[post][0], &status) != nil || json.Unmarshal(row.Replies[post][1], &body) != nil {
					t.Fatal("invalid source response input")
				}
				if status == 0 {
					return Response{}, errors.New("owned synthetic transport error")
				}
				return Response{Status: status, Content: []byte(body)}, nil
			})
			added, err := client.Add(row.URL)
			if err != nil || added != row.Added {
				t.Fatalf("Add = %v, %v; want %v", added, err, row.Added)
			}
			sent, err := client.Notify(t.Context(), row.Title, row.Body, row.Attachments)
			if err != nil || sent != row.Result {
				t.Fatalf("Notify = %v, %v; want %v", sent, err, row.Result)
			}
			if !reflect.DeepEqual(observed, row.Requests) {
				t.Fatalf("requests = %#v; want %#v", observed, row.Requests)
			}
			for i, seconds := range row.Waits {
				minimum := time.Duration(seconds*float64(time.Second)) - 15*time.Millisecond
				if elapsed := sentAt[i+1].Sub(sentAt[i]); elapsed < minimum {
					t.Fatalf("POST interval = %s; original minimum = %.1fs", elapsed, seconds)
				}
			}
		})
	}
}
