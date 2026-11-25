package apprise

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSourceGotify199(t *testing.T) {
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
			Status                 int
			Added, Result          bool
			Requests               []observation
		}
	}
	raw, err := os.ReadFile("testdata/gotify-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal("missing original Gotify observations", err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			observed := []observation{}
			client := New(func(_ context.Context, request Request) (Response, error) {
				if len(request.Auth) != 0 {
					t.Fatal("Gotify must not send URL userinfo")
				}
				observed = append(observed, observation{
					Method: request.Method, URL: request.URL, Headers: request.Headers,
					Body: string(request.Data), Verify: request.Verify, Timeout: request.Timeout,
				})
				return Response{Status: row.Status}, nil
			})
			service, added := parseGotifyService(row.URL)
			if added != row.Added {
				t.Fatalf("parsed = %v, want %v", added, row.Added)
			}
			if added {
				client.services = append(client.services, service)
			}
			sent, err := client.Notify(t.Context(), row.Title, row.Body, nil)
			if err != nil || sent != row.Result {
				t.Fatalf("Notify = %v, %v; want %v", sent, err, row.Result)
			}
			if !reflect.DeepEqual(observed, row.Requests) {
				t.Fatalf("requests = %#v; want %#v", observed, row.Requests)
			}
		})
	}
}
